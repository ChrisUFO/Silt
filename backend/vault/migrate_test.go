package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silt/backend/parser"
)

func TestMigratePerDayFiles_MergesDateFilesIntoPageFile(t *testing.T) {
	vaultPath := t.TempDir()
	pageDir := filepath.Join(vaultPath, "Work", "Journal", "Daily")
	if err := os.MkdirAll(pageDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	day1 := "---\nnotebook: Work\nsection: Journal\npage: Daily\ndate: 2026-06-01\ntags: []\n---\n- first day note <!-- id: 11111111-1111-4111-8111-111111111111 -->\n"
	day2 := "---\nnotebook: Work\nsection: Journal\npage: Daily\ndate: 2026-06-02\ntags: []\n---\n- second day note <!-- id: 22222222-2222-4222-8222-222222222222 -->\n"
	if err := os.WriteFile(filepath.Join(pageDir, "2026-06-01.md"), []byte(day1), 0644); err != nil {
		t.Fatalf("write day1: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pageDir, "2026-06-02.md"), []byte(day2), 0644); err != nil {
		t.Fatalf("write day2: %v", err)
	}

	warnings := MigratePerDayFiles(vaultPath, 4)

	targetPath := filepath.Join(vaultPath, "Work", "Journal", "Daily.md")
	contentBytes, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("expected merged file at %q: %v", targetPath, err)
	}
	content := string(contentBytes)

	if !strings.Contains(content, "11111111-1111-4111-8111-111111111111") {
		t.Errorf("merged file missing first block id:\n%s", content)
	}
	if !strings.Contains(content, "22222222-2222-4222-8222-222222222222") {
		t.Errorf("merged file missing second block id:\n%s", content)
	}

	if _, err := os.Stat(pageDir); !os.IsNotExist(err) {
		t.Errorf("expected old directory %q to be removed", pageDir)
	}

	if len(warnings) == 0 {
		t.Errorf("expected at least one migration warning (success notice)")
	}

	blocks, _, _, _, parseErr := parser.ParseFileContent(content, "Work", "Journal", "Daily", "2026-06-02", 4)
	if parseErr != nil {
		t.Fatalf("merged file failed to parse: %v", parseErr)
	}
	if len(blocks) != 2 {
		t.Errorf("expected 2 blocks in merged file, got %d", len(blocks))
	}
}

func TestMigratePerDayFiles_IdempotentSecondRunIsNoOp(t *testing.T) {
	vaultPath := t.TempDir()

	warnings1 := MigratePerDayFiles(vaultPath, 4)
	if len(warnings1) != 0 {
		t.Errorf("expected no warnings on first run with empty vault, got %v", warnings1)
	}

	warnings2 := MigratePerDayFiles(vaultPath, 4)
	if len(warnings2) != 0 {
		t.Errorf("expected no warnings on second run, got %v", warnings2)
	}
}

func TestMigratePerDayFiles_SkipsWhenTargetExists(t *testing.T) {
	vaultPath := t.TempDir()
	pageDir := filepath.Join(vaultPath, "Work", "Daily")
	if err := os.MkdirAll(pageDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pageDir, "2026-06-01.md"), []byte("- old note\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	targetPath := filepath.Join(vaultPath, "Work", "Daily.md")
	if err := os.WriteFile(targetPath, []byte("- already migrated\n"), 0644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	warnings := MigratePerDayFiles(vaultPath, 4)

	found := false
	for _, w := range warnings {
		if strings.Contains(w, "already exists") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a 'target already exists' warning, got %v", warnings)
	}

	if _, err := os.Stat(filepath.Join(pageDir, "2026-06-01.md")); err != nil {
		t.Errorf("original date file should be preserved when target exists")
	}
}
