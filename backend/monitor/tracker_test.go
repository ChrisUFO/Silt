package monitor

import (
	"testing"
	"time"
)

func TestWriteTracker_ImmediateCheck(t *testing.T) {
	wt := NewWriteTracker()
	filePath := "test_file.md"

	// Check on non-existent file
	if wt.IsSelfGenerated(filePath) {
		t.Errorf("Expected false for unregistered file write")
	}

	// Register write
	wt.RegisterWrite(filePath)

	// Immediate check should be true
	if !wt.IsSelfGenerated(filePath) {
		t.Errorf("Expected true for immediate check")
	}

	// Second check immediately after should be false (since it deletes on match)
	if wt.IsSelfGenerated(filePath) {
		t.Errorf("Expected false for second immediate check")
	}
}

func TestWriteTracker_CooldownTimeout(t *testing.T) {
	wt := NewWriteTracker()
	filePath := "test_file.md"

	wt.RegisterWrite(filePath)

	// Wait 350ms (longer than the 300ms cooldown)
	time.Sleep(350 * time.Millisecond)

	// Check after timeout should be false
	if wt.IsSelfGenerated(filePath) {
		t.Errorf("Expected false after 350ms cooldown timeout")
	}
}
