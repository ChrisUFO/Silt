package vault

// fingerprint.go implements the F20 settings.json integrity tripwire. A
// co-tenant (or malware running with the user's credentials) who can write to
// the OS-config dir could redirect the vault path to a hostile folder or
// poison the trusted-publishers list. The fingerprint file is a SHA-256 of
// the trust-anchor fields (vault_path + trusted_publishers); on every launch
// it is recomputed and compared. On mismatch, Silt surfaces a confirmation
// dialog rather than silently loading the new values.
//
// This is explicitly a TRIPWIRE, not a hard boundary — an attacker who can
// write settings.json can also write the fingerprint file. The hard boundary
// is the filesystem permission on the OS-config dir (the user's home, which
// only they should own), which the 0o600 atomic write enforces on POSIX.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"silt/backend/parser"
)

// ErrSettingsFingerprintMismatch is returned by LoadSettings alongside the
// loaded settings when the on-disk fingerprint does not match the just-loaded
// trust-anchor fields. Callers SHOULD still use the returned settings (they
// are valid JSON with a valid schema — the fingerprint just flags a possible
// tampering) but surface a confirmation dialog so the user can acknowledge or
// reject the change. The fingerprint is NOT updated by LoadSettings; only
// SaveSettings (Silt's own trusted write) or ConfirmSettingsChange (explicit
// user ack) updates it.
var ErrSettingsFingerprintMismatch = errors.New("settings fingerprint mismatch")

// settingsFingerprintPath returns the path to the companion fingerprint file.
// It lives alongside settings.json in the OS-config dir so the pair travels
// together and a reader never has to look in two places.
func settingsFingerprintPath() (string, error) {
	path, err := GetSettingsPath()
	if err != nil {
		return "", err
	}
	return path + ".fingerprint", nil
}

// trustAnchorJSON is the canonical JSON form of the trust-anchor fields. Only
// vault_path and trusted_publishers are hashed — a theme-mode or active-theme
// edit is innocuous and must NOT trip the wire. The field set is frozen so a
// future Silt version adding a field to AppSettings does not silently change
// the hash input. Canonical key ordering (json.Marshal of a struct) keeps the
// hash stable across map-iteration orders.
type trustAnchorJSON struct {
	VaultPath         string   `json:"vault_path"`
	TrustedPublishers []string `json:"trusted_publishers,omitempty"`
}

// computeSettingsFingerprint returns the hex-encoded SHA-256 of the canonical
// JSON of the trust-anchor fields. The same input always produces the same
// hash; a nil trusted-publishers slice is normalized to an empty array so the
// hash is stable whether the field was omitted or explicitly empty.
func computeSettingsFingerprint(s *AppSettings) string {
	anchor := trustAnchorJSON{
		VaultPath:         s.VaultPath,
		TrustedPublishers: s.TrustedPublishers,
	}
	if anchor.TrustedPublishers == nil {
		anchor.TrustedPublishers = []string{}
	}
	data, err := json.Marshal(anchor)
	if err != nil {
		// Should be impossible for a struct of two simple fields; if it
		// somehow fails, hash a constant so the mismatch fires loudly
		// rather than panicking at startup.
		return "invalid-anchor-unhashable"
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// writeSettingsFingerprint records the current trust-anchor fingerprint to the
// companion file atomically with 0o600 perms (WriteFileAtomic uses
// os.CreateTemp which defaults to 0o600). Called by SaveSettings (Silt's own
// trusted write) and ConfirmSettingsChange (explicit user ack). NOT called by
// LoadSettings on mismatch — the mismatch must persist so a relaunch
// re-prompts if the user dismissed the dialog.
func writeSettingsFingerprint(s *AppSettings) error {
	fpPath, err := settingsFingerprintPath()
	if err != nil {
		return err
	}
	fp := computeSettingsFingerprint(s)
	if err := os.MkdirAll(filepath.Dir(fpPath), 0o700); err != nil {
		return err
	}
	return parser.WriteFileAtomic(fpPath, []byte(fp))
}

// readSettingsFingerprint returns the stored fingerprint, or ("", false) if no
// fingerprint file exists yet (first launch / fresh install). A read error
// other than not-exist is returned.
func readSettingsFingerprint() (string, bool, error) {
	fpPath, err := settingsFingerprintPath()
	if err != nil {
		return "", false, err
	}
	data, err := os.ReadFile(fpPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	return string(data), true, nil
}

// ConfirmSettingsChange is the Wails binding called by the frontend when the
// user acknowledges the settings-changed confirmation dialog. It updates the
// on-disk fingerprint to match the currently-loaded trust-anchor values,
// clearing the mismatch sentinel so subsequent launches proceed without a
// prompt. Returns the settings (useful for the frontend to refresh its state).
func ConfirmSettingsChange() (*AppSettings, error) {
	settings, err := LoadSettings()
	if err != nil && !errors.Is(err, ErrSettingsFingerprintMismatch) {
		return nil, fmt.Errorf("ConfirmSettingsChange: load: %w", err)
	}
	if err := writeSettingsFingerprint(settings); err != nil {
		return nil, fmt.Errorf("ConfirmSettingsChange: write fingerprint: %w", err)
	}
	return settings, nil
}
