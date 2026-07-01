package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestLockOrderVaultBeforeConfig (#344) is a static guard for the lock-ordering
// invariant declared at app.go:126 — "vaultMu is always acquired BEFORE
// configMu." Acquiring them in the opposite order in two different goroutines
// is a classic AB-BA deadlock; this test catches the most common regression
// (someone adding or editing a binding that takes configMu before vaultMu) at
// review time, before it ships.
//
// What it checks: for every method on *App in app_*.go (non-test), whenever a
// function body contains BOTH a direct a.vaultMu.{,R}Lock() and a
// a.configMu.{,R}Lock() acquisition, the first vaultMu acquisition must
// precede the first configMu acquisition in source order.
//
// Limitations (deliberate, documented): this is a LEXICAL check of direct
// acquisitions only. It does NOT model nested calls — e.g. a function that
// holds configMu and then calls a helper which itself takes vaultMu would
// invert the order at runtime but not be caught here. The direct-acquisition
// case is the one that regresses in practice when a binding is added or
// edited; the nested case is covered by code review against the invariant
// comment. If a function legitimately needs configMu without vaultMu (e.g.
// app_capabilities.go's per-host grants store, which touches no vault state),
// the rule is vacuously satisfied because vaultMu never appears.
func TestLockOrderVaultBeforeConfig(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	// Match the app_*.go files in this package (the Wails-bound App methods).
	entries, err := filepath.Glob(filepath.Join(root, "app_*.go"))
	if err != nil {
		t.Fatalf("glob app_*.go: %v", err)
	}
	// Filter out test files (app_*_test.go).
	var files []string
	for _, f := range entries {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		t.Fatal("no app_*.go files found — test is running in the wrong directory")
	}
	sort.Strings(files)

	violations := 0
	for _, path := range files {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			t.Errorf("parse %s: %v", filepath.Base(path), err)
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				return true
			}
			if !isAppMethod(fn) {
				return true
			}
			var firstVault, firstConfig token.Position
			var vaultSeen, configSeen bool
			ast.Inspect(fn.Body, func(e ast.Node) bool {
				call, ok := e.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				name := sel.Sel.Name
				if name != "Lock" && name != "RLock" {
					return true
				}
				field, ok := sel.X.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				muName := field.Sel
				pos := fset.Position(call.Pos())
				switch muName.Name {
				case "vaultMu":
					if !vaultSeen {
						firstVault = pos
						vaultSeen = true
					}
				case "configMu":
					if !configSeen {
						firstConfig = pos
						configSeen = true
					}
				}
				return true
			})
			// Only enforce when BOTH appear in this function body.
			if vaultSeen && configSeen && firstConfig.Line < firstVault.Line {
				violations++
				t.Errorf(
					"lock-order violation in %s.%s: configMu acquired at %s BEFORE vaultMu at %s (app.go invariant: vaultMu before configMu)",
					filepath.Base(path), fn.Name.Name, firstConfig, firstVault,
				)
			}
			return true
		})
	}
	if violations == 0 {
		t.Logf("audited %d app_*.go files: all dual-acquiring *App methods take vaultMu before configMu", len(files))
	}
}

// isAppMethod reports whether fn is a method on the App type.
func isAppMethod(fn *ast.FuncDecl) bool {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return false
	}
	star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
	if ok {
		id, ok := star.X.(*ast.Ident)
		return ok && id.Name == "App"
	}
	id, ok := fn.Recv.List[0].Type.(*ast.Ident)
	return ok && id.Name == "App"
}
