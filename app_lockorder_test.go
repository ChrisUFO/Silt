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
// acquisitions only. The nested-call inversion case (a function holding
// configMu and then calling a helper which itself takes vaultMu) is covered by
// the companion TestLockOrderNoVaultMuCalleeUnderConfigMu. If a function
// legitimately needs configMu without vaultMu (e.g. app_capabilities.go's
// per-host grants store, which touches no vault state), the rule is vacuously
// satisfied because vaultMu never appears.
func TestLockOrderVaultBeforeConfig(t *testing.T) {
	files := collectAppGoFiles(t)
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

// TestLockOrderNoVaultMuCalleeUnderConfigMu (#344) closes the nested-call gap
// the lexical direct-acquisition test cannot see: a method that HOLDS configMu
// and then CALLS a vaultMu-acquiring method (e.g. SaveFileBlocks, ListNavigation)
// inverts the lock order at runtime and can deadlock, yet writes no direct
// vaultMu acquisition for the first test to catch.
//
// Approach: (1) build the set of *App method names that directly acquire
// vaultMu, then (2) walk every *App method body tracking a configMu-held
// counter (inc on Lock/RLock, dec on Unlock/RUnlock) in lexical order and flag
// any call to a vaultMu-acquirer while the counter is > 0. The counter is
// sound even with `defer ...Unlock()`: a deferred unlock means the lock is
// genuinely held until function return, so any later call IS made while held.
//
// Limitation (documented): this is a static, lexical approximation. It does
// not model goroutine boundaries or conditional release-before-call patterns
// beyond what source order reveals; a genuine disjoint-sections case can be
// reviewed against the app.go invariant comment. It catches the realistic
// regression the first test misses.
func TestLockOrderNoVaultMuCalleeUnderConfigMu(t *testing.T) {
	files := collectAppGoFiles(t)

	// Pass 1: which *App method names directly acquire a.vaultMu.{,R}Lock?
	vaultMuAcquirers := map[string]bool{}
	for _, path := range files {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Errorf("parse %s: %v", filepath.Base(path), err)
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok || fn.Body == nil || !isAppMethod(fn) {
				return true
			}
			acquires := false
			ast.Inspect(fn.Body, func(e ast.Node) bool {
				if acquires {
					return false
				}
				sel, ok := e.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if sel.Sel.Name != "Lock" && sel.Sel.Name != "RLock" {
					return true
				}
				field, ok := sel.X.(*ast.SelectorExpr)
				if ok && field.Sel.Name == "vaultMu" {
					acquires = true
				}
				return true
			})
			if acquires {
				vaultMuAcquirers[fn.Name.Name] = true
			}
			return true
		})
	}

	// Pass 2: flag a call to a vaultMu-acquirer while configMu is held.
	violations := 0
	for _, path := range files {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok || fn.Body == nil || !isAppMethod(fn) {
				return true
			}
			configHeld := 0
			var heldAt token.Position
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
				field, _ := sel.X.(*ast.SelectorExpr)
				muName := ""
				if field != nil {
					muName = field.Sel.Name
				}
				callPos := fset.Position(call.Pos())
				switch {
				case muName == "configMu" && (name == "Lock" || name == "RLock"):
					if configHeld == 0 {
						heldAt = callPos
					}
					configHeld++
					return true
				case muName == "configMu" && (name == "Unlock" || name == "RUnlock"):
					if configHeld > 0 {
						configHeld--
					}
					return true
				}
				// A call to a vaultMu-acquiring method while configMu is held.
				if configHeld > 0 && vaultMuAcquirers[name] {
					// a.<name>(...) — receiver must be the App (a.).
					if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "a" {
						violations++
						t.Errorf(
							"nested lock-order violation in %s.%s: calls vaultMu-acquiring %s at %s while configMu held since %s (app.go invariant: vaultMu before configMu)",
							filepath.Base(path), fn.Name.Name, name, callPos, heldAt,
						)
					}
				}
				return true
			})
			return true
		})
	}
	if len(vaultMuAcquirers) == 0 {
		t.Fatal("no vaultMu-acquiring *App methods found — allowlist is empty; test cannot enforce nested ordering")
	}
	if violations == 0 {
		t.Logf("audited %d app_*.go files: no configMu-holding method calls a vaultMu-acquiring callee", len(files))
	}
}

// collectAppGoFiles returns app.go plus all app_*.go files in this package,
// excluding tests. Fails the test if none are found.
func collectAppGoFiles(t *testing.T) []string {
	t.Helper()
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	patterns := []string{
		filepath.Join(root, "app.go"),
		filepath.Join(root, "app_*.go"),
	}
	var files []string
	for _, p := range patterns {
		matches, err := filepath.Glob(p)
		if err != nil {
			t.Fatalf("glob %s: %v", p, err)
		}
		for _, m := range matches {
			if strings.HasSuffix(m, "_test.go") {
				continue
			}
			files = append(files, m)
		}
	}
	if len(files) == 0 {
		t.Fatal("no app.go / app_*.go files found — test is running in the wrong directory")
	}
	sort.Strings(files)
	return files
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
