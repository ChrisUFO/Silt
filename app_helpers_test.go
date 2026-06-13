package main

import (
	"path/filepath"
	"testing"
)

func TestFindLineByBlockID(t *testing.T) {
	lines := []string{
		"# Header <!-- id: aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa -->",
		"- [ ] TODO TASK sample <!-- id: bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb -->",
		"    nested note <!-- id: cccccccc-cccc-cccc-cccc-cccccccccccc -->",
		"unrelated line",
	}

	if got := findLineByBlockID(lines, "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"); got != 1 {
		t.Errorf("expected task line at index 1, got %d", got)
	}
	if got := findLineByBlockID(lines, "cccccccc-cccc-cccc-cccc-cccccccccccc"); got != 2 {
		t.Errorf("expected nested note at index 2, got %d", got)
	}
	if got := findLineByBlockID(lines, "deadbeef-dead-beef-dead-beefdeadbeef"); got != -1 {
		t.Errorf("expected -1 for missing block, got %d", got)
	}
	if got := findLineByBlockID(nil, "any"); got != -1 {
		t.Errorf("expected -1 for empty lines, got %d", got)
	}
}

func TestSanitizePathSegment(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Engineering", "Engineering"},
		{"../etc/passwd", "etcpasswd"},
		{"..\\windows\\system", "windowssystem"},
		{"Work/Journal", "WorkJournal"},
		{"with\x00null", "withnull"},
		{"with\nnewline", "withnewline"},
		{"with\rreturn", "withreturn"},
		{"with\ttab", "withtab"},
		{"with\x01ctrl", "withctrl"},
		{"with\x1funit", "withunit"},
		{"a..b..c", "abc"},
		{"   spaced   ", "spaced"},
		{"", ""},
	}
	for _, c := range cases {
		if got := sanitizePathSegment(c.in); got != c.want {
			t.Errorf("sanitizePathSegment(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIsPathWithinVault(t *testing.T) {
	vault := t.TempDir()

	if !isPathWithinVault(filepath.Join(vault, "Work", "Journal", "2026-06-13.md"), vault) {
		t.Errorf("nested path inside vault should be allowed")
	}
	if !isPathWithinVault(vault, vault) {
		t.Errorf("vault root itself should be allowed")
	}
	// A traversal that escapes must be rejected.
	if isPathWithinVault(filepath.Join(vault, "..", "..", "etc", "passwd"), vault) {
		t.Errorf("path escaping vault should be rejected")
	}
	// A sibling directory must not be allowed.
	sibling := t.TempDir()
	if isPathWithinVault(filepath.Join(sibling, "secret.md"), vault) {
		t.Errorf("sibling path should be rejected")
	}
}
