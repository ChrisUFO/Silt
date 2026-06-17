package main

import (
	"path/filepath"
	"testing"
)

func TestFindLineByBlockID(t *testing.T) {
	lines := []string{
		"# Header <!-- id: aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa -->",
		"- [ ] sample <!-- id: bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb -->",
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
		{"a..b..c", "a..b..c"},
		{"2.0..2.1", "2.0..2.1"},
		{".", ""},
		{"..", ""},
		{"..foo", "foo"},
		{"....foo", "foo"},
		{"foo/..bar", "foo..bar"},
		{"foo..bar", "foo..bar"},
		{"foo.bar", "foo.bar"},
		{"   spaced   ", "spaced"},
		{"  ..", ""},
		{" ..foo", "foo"},
		{" .. foo", "foo"},
		{"\t..evil", "evil"},
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

	if !isPathWithinRoot(filepath.Join(vault, "Work", "Journal", "2026-06-13.md"), vault) {
		t.Errorf("nested path inside vault should be allowed")
	}
	if !isPathWithinRoot(vault, vault) {
		t.Errorf("vault root itself should be allowed")
	}
	// A traversal that escapes must be rejected.
	if isPathWithinRoot(filepath.Join(vault, "..", "..", "etc", "passwd"), vault) {
		t.Errorf("path escaping vault should be rejected")
	}
	// A sibling directory must not be allowed.
	sibling := t.TempDir()
	if isPathWithinRoot(filepath.Join(sibling, "secret.md"), vault) {
		t.Errorf("sibling path should be rejected")
	}
}
