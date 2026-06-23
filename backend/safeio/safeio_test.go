package safeio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFileMax_UnderCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	want := strings.Repeat("x", 100)
	if err := os.WriteFile(path, []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFileMax(path, 256)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != want {
		t.Errorf("content mismatch")
	}
}

// A file of exactly max bytes is accepted — the +1 sentinel byte is the
// overflow detector, not part of the budget.
func TestReadFileMax_ExactCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 100)), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFileMax(path, 100)
	if err != nil {
		t.Fatalf("file at exactly the cap must be accepted: %v", err)
	}
	if len(got) != 100 {
		t.Errorf("got %d bytes, want 100", len(got))
	}
}

func TestReadFileMax_OverCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 200)), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ReadFileMax(path, 100)
	if err == nil {
		t.Fatal("expected overflow error, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds the") || !strings.Contains(err.Error(), "100-byte cap") {
		t.Errorf("error %q must name the byte cap", err.Error())
	}
}

func TestReadFileMax_MissingFile(t *testing.T) {
	_, err := ReadFileMax(filepath.Join(t.TempDir(), "nope"), 64)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

// A large hostile file is rejected without slurping it whole: the cap is
// enforced at read time, bounding memory (audit F12).
func TestReadFileMax_LargeFileRejectedFast(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.bin")
	if err := os.WriteFile(path, make([]byte, 2<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ReadFileMax(path, 1<<10)
	if err == nil {
		t.Fatal("expected overflow error for oversized file")
	}
}
