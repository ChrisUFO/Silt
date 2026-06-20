// Package plugins handles .silt-plugin archive validation, installation,
// and removal. A .silt-plugin archive is a ZIP whose entries live at the
// archive root: a plugin.json manifest plus the entry module (index.js) and
// optional asset files.
package plugins

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// Manifest is the plugin.json schema inside a .silt-plugin archive.
type Manifest struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	Version        string         `json:"version"`
	Author         string         `json:"author,omitempty"`
	Description    string         `json:"description,omitempty"`
	Icon           string         `json:"icon,omitempty"`
	Main           string         `json:"main,omitempty"` // defaults to "index.js"
	MinSiltVersion string         `json:"minSiltVersion,omitempty"`
	// Capabilities is the v2 SDK capability declaration (#113): a map of
	// capability id → true | "notebook" | "vault". Normalized at validation
	// time (unknown capabilities rejected) and surfaced to the user at install
	// so a grant decision is informed. Absent for plugins that use only the
	// read-only SDK (sqliteQuery + the existing mutators).
	Capabilities map[string]any `json:"capabilities,omitempty"`
	// Settings is the declarative settings schema (#103): each entry declares
	// a typed field (key, label, type, default) the host renders generically in
	// Settings → Plugins. Carried through opaquely (the frontend is the typed
	// consumer); the installer only validates structural shape.
	Settings []map[string]any `json:"settings,omitempty"`
	// Homepage is an optional URL for the plugin's homepage / docs (#111).
	Homepage string `json:"homepage,omitempty"`
	// UpdateURL is an optional URL that returns a JSON manifest with the latest
	// version + download URL, for update checks (#111).
	UpdateURL string `json:"updateUrl,omitempty"`
	// Ratelimit is an optional per-plugin fetch rate-limit override (#153).
	// If absent, the host default (1 rps, burst 10) applies. If present, rps
	// must be > 0 and <= the hard cap (10); burst must be > 0 and <= 100.
	Ratelimit *RatelimitConfig `json:"ratelimit,omitempty"`
	// ContentSHA256 is the sha256 of the installed index.js, computed at
	// install time so the loader can verify runtime integrity (#161). Written
	// into the on-disk plugin.json by Install; absent from the original archive.
	ContentSHA256 string `json:"contentSha256,omitempty"`
}

// RatelimitConfig is the manifest-declared per-plugin fetch rate limit (#153).
type RatelimitConfig struct {
	RPS   float64 `json:"rps"`
	Burst int     `json:"burst"`
}

var idRegex = regexp.MustCompile(`^[a-z0-9-]+$`)

// IsValidID reports whether pluginID is a syntactically valid plugin
// identifier (lowercase alphanumeric + hyphens). Used at every IPC entry
// point that accepts a caller-supplied pluginID so path-traversal payloads
// like "../../etc/passwd" are rejected before reaching filepath.Join.
func IsValidID(pluginID string) bool {
	return idRegex.MatchString(pluginID)
}

// maxArchiveUncompressedSize bounds the total extracted size of a .silt-plugin
// archive so a hostile or accidental huge file can't exhaust the user's disk.
const maxArchiveUncompressedSize = 100 * 1024 * 1024 // 100 MB

// Validate opens a .silt-plugin archive and checks it is safe and complete
// without extracting. It returns the parsed manifest and any non-fatal
// warnings (e.g. missing optional fields).
func Validate(archivePath string) (Manifest, []string, error) {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return Manifest{}, nil, fmt.Errorf("failed to open archive: %w", err)
	}
	defer r.Close()

	var warnings []string
	manifest := Manifest{}
	foundManifest := false
	foundMain := false
	mainName := "index.js"
	var entryNames []string
	for _, f := range r.File {
		entryNames = append(entryNames, f.Name)
	}

	// First pass: locate + parse the manifest.
	for _, f := range r.File {
		if filepath.ToSlash(f.Name) != "plugin.json" {
			continue
		}
		foundManifest = true
		rc, err := f.Open()
		if err != nil {
			return Manifest{}, warnings, fmt.Errorf("failed to read plugin.json: %w", err)
		}
		dec := json.NewDecoder(rc)
		if err := dec.Decode(&manifest); err != nil {
			rc.Close()
			return Manifest{}, warnings, fmt.Errorf("invalid plugin.json: %w", err)
		}
		rc.Close()
		break
	}
	if !foundManifest {
		return Manifest{}, warnings, fmt.Errorf("archive is missing plugin.json")
	}

	// Required-field + id-format validation.
	if manifest.ID == "" {
		return Manifest{}, warnings, fmt.Errorf("manifest is missing required field: id")
	}
	if !idRegex.MatchString(manifest.ID) {
		return Manifest{}, warnings, fmt.Errorf("manifest id %q must be lowercase letters, digits, and hyphens only", manifest.ID)
	}
	if manifest.Name == "" {
		manifest.Name = manifest.ID
		warnings = append(warnings, "manifest has no name; using id as name")
	}
	if manifest.Version == "" {
		manifest.Version = "0.0.0"
		warnings = append(warnings, "manifest has no version; defaulting to 0.0.0")
	}
	// The loader reads a fixed index.js; the manifest Main field must match
	// that contract. Reject anything else so a plugin author who sets a
	// custom main gets a clear install-time error instead of a silent
	// load-time failure.
	if manifest.Main != "" && filepath.ToSlash(manifest.Main) != "index.js" {
		return Manifest{}, warnings, fmt.Errorf("manifest main must be \"index.js\" (got %q)", manifest.Main)
	}

	// Normalize the v2 capability declaration (#113): reject unknown
	// capabilities and malformed qualifiers at install so the host never
	// silently grants a right it does not understand, and so the capability
	// summary shown to the user is accurate. The normalized map is stashed
	// back onto the manifest for downstream consumers (ListPlugins surfaces
	// it; the grant UI reads it).
	if norm, cerr := NormalizeCapabilities(manifest.Capabilities); cerr != nil {
		return Manifest{}, warnings, fmt.Errorf("invalid capabilities: %w", cerr)
	} else {
		manifest.Capabilities = normalizedToAny(norm)
	}

	// Validate the declarative settings schema (#103): each entry must have a
	// key, label, and a recognized type. Reject malformed schemas at install so
	// the generated settings form never renders a broken field.
	if serr := validateSettingsSchema(manifest.Settings); serr != nil {
		return Manifest{}, warnings, fmt.Errorf("invalid settings schema: %w", serr)
	}

	// Validate the optional per-plugin fetch rate-limit override (#153).
	// rps must be > 0 and <= the hard cap; burst must be > 0 and <= 100.
	if manifest.Ratelimit != nil {
		if manifest.Ratelimit.RPS <= 0 || manifest.Ratelimit.RPS > 10 {
			return Manifest{}, warnings, fmt.Errorf("ratelimit.rps must be between 0 (exclusive) and 10 (inclusive), got %g", manifest.Ratelimit.RPS)
		}
		if manifest.Ratelimit.Burst <= 0 || manifest.Ratelimit.Burst > 100 {
			return Manifest{}, warnings, fmt.Errorf("ratelimit.burst must be between 1 and 100, got %d", manifest.Ratelimit.Burst)
		}
	}

	// Second pass: zip-slip / absolute-path guard + main-file presence +
	// uncompressed-size cap.
	var totalUncompressed uint64
	for _, f := range r.File {
		name := filepath.ToSlash(f.Name)
		if strings.HasPrefix(name, "/") || filepath.IsAbs(name) {
			return Manifest{}, warnings, fmt.Errorf("archive entry %q is absolute; refusing", name)
		}
		if strings.Contains(name, "..") {
			return Manifest{}, warnings, fmt.Errorf("archive entry %q escapes the archive root (zip-slip); refusing", name)
		}
		if name == mainName {
			foundMain = true
		}
		totalUncompressed += f.UncompressedSize64
	}
	if !foundMain {
		return Manifest{}, warnings, fmt.Errorf("archive is missing the entry module %q", mainName)
	}
	if totalUncompressed > maxArchiveUncompressedSize {
		return Manifest{}, warnings, fmt.Errorf("archive uncompressed size %d bytes exceeds the %d-byte limit", totalUncompressed, maxArchiveUncompressedSize)
	}

	return manifest, warnings, nil
}

// Install validates then atomically extracts the archive into
// <vault>/.system/plugins/<id>/. Returns the parsed manifest.
// It refuses to overwrite an existing plugin id.
func Install(vaultPath, archivePath string) (Manifest, error) {
	manifest, _, err := Validate(archivePath)
	if err != nil {
		return Manifest{}, err
	}

	dest := filepath.Join(vaultPath, ".system", "plugins", manifest.ID)
	if _, err := os.Stat(dest); err == nil {
		return Manifest{}, fmt.Errorf("plugin %q is already installed", manifest.ID)
	}

	// Extract into a sibling temp dir, then rename for atomicity.
	tmp, err := os.MkdirTemp(filepath.Join(vaultPath, ".system", "plugins"), "."+manifest.ID+".-*")
	if err != nil {
		return Manifest{}, fmt.Errorf("failed to create staging dir: %w", err)
	}
	// Best-effort cleanup on failure.
	defer os.RemoveAll(tmp)

	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return Manifest{}, fmt.Errorf("failed to open archive: %w", err)
	}
	defer r.Close()

	for _, f := range r.File {
		name := filepath.ToSlash(f.Name)
		// Re-check safety (defense in depth, mirrors Validate).
		if strings.HasPrefix(name, "/") || filepath.IsAbs(name) || strings.Contains(name, "..") {
			return Manifest{}, fmt.Errorf("archive entry %q is unsafe", name)
		}
		target := filepath.Join(tmp, name)
		// Final containment check on the joined path.
		if !isWithin(target, tmp) {
			return Manifest{}, fmt.Errorf("archive entry %q escapes the plugin directory", name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return Manifest{}, err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return Manifest{}, err
		}
		if err := copyZipEntry(f, target); err != nil {
			return Manifest{}, err
		}
	}

	// Compute sha256 of the extracted index.js and write it into the on-disk
	// plugin.json so the loader can verify integrity at load time (#161).
	indexPath := filepath.Join(tmp, "index.js")
	indexData, err := os.ReadFile(indexPath)
	if err != nil {
		return Manifest{}, fmt.Errorf("failed to read extracted index.js for hashing: %w", err)
	}
	hash := sha256.Sum256(indexData)
	manifest.ContentSHA256 = hex.EncodeToString(hash[:])
	// Inject contentSha256 into the on-disk plugin.json via a generic map so
	// custom/unknown fields the author included (repository, bugs, keywords,
	// ...) are preserved instead of being dropped by a struct round-trip.
	manifestOnDisk := filepath.Join(tmp, "plugin.json")
	manifestData, err := os.ReadFile(manifestOnDisk)
	if err != nil {
		return Manifest{}, fmt.Errorf("failed to read plugin.json for sha256 injection: %w", err)
	}
	var rawManifest map[string]any
	if err := json.Unmarshal(manifestData, &rawManifest); err != nil {
		return Manifest{}, fmt.Errorf("failed to parse plugin.json for sha256 injection: %w", err)
	}
	rawManifest["contentSha256"] = manifest.ContentSHA256
	manifestJSONBytes, err := json.Marshal(rawManifest)
	if err != nil {
		return Manifest{}, fmt.Errorf("failed to serialize manifest with sha256: %w", err)
	}
	if err := os.WriteFile(manifestOnDisk, manifestJSONBytes, 0o644); err != nil {
		return Manifest{}, fmt.Errorf("failed to write manifest with sha256: %w", err)
	}

	if err := os.Rename(tmp, dest); err != nil {
		return Manifest{}, fmt.Errorf("failed to finalize install: %w", err)
	}
	return manifest, nil
}

// Uninstall removes a plugin directory from the vault. The id is validated
// against idRegex (not sanitized) so dot-segment tricks like "..." can never
// resolve to the parent plugins directory.
func Uninstall(vaultPath, pluginID string) error {
	if !idRegex.MatchString(pluginID) {
		return fmt.Errorf("invalid plugin id %q", pluginID)
	}
	dir := filepath.Join(vaultPath, ".system", "plugins", pluginID)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("plugin %q is not installed", pluginID)
	}
	return os.RemoveAll(dir)
}

// SetDisabled toggles a sentinel ".disabled" file inside the plugin folder;
// the loader skips plugins that have it. Avoids fragile config.yaml edits.
func SetDisabled(vaultPath, pluginID string, disabled bool) error {
	if !idRegex.MatchString(pluginID) {
		return fmt.Errorf("invalid plugin id %q", pluginID)
	}
	dir := filepath.Join(vaultPath, ".system", "plugins", pluginID)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("plugin %q is not installed", pluginID)
	}
	sentinel := filepath.Join(dir, ".disabled")
	if disabled {
		f, err := os.Create(sentinel)
		if err != nil {
			return err
		}
		f.Close()
	} else {
		_ = os.Remove(sentinel)
	}
	return nil
}

// IsDisabled reports whether the sentinel is present for a plugin folder.
func IsDisabled(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".disabled"))
	return err == nil
}

// maxPluginFileSize bounds a single extracted file. The per-file cap is
// defense-in-depth alongside the total-archive cap; it also bounds the
// io.LimitReader so a forged-header zip-bomb cannot expand past the declared
// size during extraction.
const maxPluginFileSize = 10 * 1024 * 1024 // 10 MB

func copyZipEntry(f *zip.File, target string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	// Bound the decompressed stream to the declared uncompressed size (plus a
	// 1 KB margin for trailing data). A zip-bomb with forged headers that
	// claims 1 KB but decompresses to 10 GB will be cut off here.
	limit := int64(f.UncompressedSize64) + 1024
	if limit > maxPluginFileSize+1024 {
		return fmt.Errorf("plugin file %q exceeds the %d-byte per-file limit", f.Name, maxPluginFileSize)
	}
	_, err = io.Copy(out, io.LimitReader(rc, limit))
	return err
}

// normalizedToAny converts a normalized capability→qualifier map back into the
// manifest's `map[string]any` shape so the field round-trips through JSON
// unchanged after validation. A "granted" qualifier is emitted as `true` (the
// idiomatic manifest form); scope qualifiers are emitted as their string.
func normalizedToAny(norm map[string]string) map[string]any {
	out := make(map[string]any, len(norm))
	for k, q := range norm {
		if q == QualGranted {
			out[k] = true
		} else {
			out[k] = q
		}
	}
	return out
}

// isWithin reports whether target is contained within base. It cleans both
// paths, resolves symlinks when possible (so a symlink in the vault cannot
// mask an escape), and compares case-insensitively on Windows where the
// filesystem itself is case-insensitive.
func isWithin(target, base string) bool {
	absTarget, err := filepath.Abs(filepath.Clean(target))
	if err != nil {
		return false
	}
	absBase, err := filepath.Abs(filepath.Clean(base))
	if err != nil {
		return false
	}
	// Resolve symlinks for defense in depth: a symlink inside the vault
	// pointing outside it must not satisfy a containment check that only
	// inspects the lexical path. Ignore errors (e.g. non-existent paths
	// during construction) and fall back to the lexical form.
	if resolved, err := filepath.EvalSymlinks(absTarget); err == nil {
		absTarget = resolved
	}
	if resolved, err := filepath.EvalSymlinks(absBase); err == nil {
		absBase = resolved
	}
	sep := string(os.PathSeparator)
	if absTarget == absBase {
		return true
	}
	if runtime.GOOS == "windows" {
		return strings.HasPrefix(strings.ToLower(absTarget), strings.ToLower(absBase+sep))
	}
	return strings.HasPrefix(absTarget, absBase+sep)
}
