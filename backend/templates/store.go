package templates

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"silt/backend/parser"
	"gopkg.in/yaml.v3"
)

// SerializeTemplate re-emits a template in its canonical on-disk form: a YAML
// frontmatter block (the metadata fields, Body/Source omitted via yaml:"-")
// followed by the Markdown body. This is what SaveTemplate writes atomically,
// so a re-saved template is byte-stable and round-trips through
// ParseTemplateBytes. yaml.Marshal on a *Template produces exactly the
// frontmatter fields because Body and Source carry yaml:"-".
func SerializeTemplate(t *Template) []byte {
	fm, err := yaml.Marshal(t)
	if err != nil {
		// A marshal failure here can only mean the struct itself is
		// unrepresentable, which is a programmer error (not user input).
		fm = []byte(fmt.Sprintf("id: %q\ntitle: %q\n", t.ID, t.Title))
	}
	// Ensure exactly one trailing newline after the frontmatter block, then the
	// closing fence, then the body with a single blank line separator.
	fmText := strings.TrimRight(string(fm), "\n")
	body := strings.TrimPrefix(t.Body, "\n")
	return []byte("---\n" + fmText + "\n---\n" + body + "\n")
}

// SaveTemplate validates t, rejects any attempt to overwrite an embedded
// first-class template (builtin:// ids are read-only), and writes the canonical
// form atomically to <templatesDir>/<id>.md. The atomic write (temp + rename)
// guarantees the file is either the previous version or the new one in full,
// never a half-written file truncated by power loss — the same durability
// guarantee as every other writer in Silt (SPECS §7.1).
//
// Self-write suppression (so the file watcher does not reload on Silt's own
// save) is the caller's responsibility: App.SaveUserTemplate calls the
// watcher's RegisterSelfWrite before this function, mirroring how
// SaveSystemConfig interacts with the config watcher. The package stays
// decoupled from the watcher to avoid an import cycle.
func SaveTemplate(templatesDir string, t *Template) error {
	if templatesDir == "" {
		return fmt.Errorf("templates directory is empty (vault not loaded)")
	}
	if t == nil {
		return fmt.Errorf("template is nil")
	}
	if err := Validate(t); err != nil {
		return err
	}
	if IsBuiltinID(t.ID) {
		return fmt.Errorf("cannot overwrite built-in template %q (built-ins are read-only; choose a different id)", t.ID)
	}
	if !IsValidID(t.ID) {
		return fmt.Errorf("invalid template id %q (must be lowercase [a-z0-9_-])", t.ID)
	}
	if err := os.MkdirAll(templatesDir, 0o755); err != nil {
		return fmt.Errorf("failed to ensure templates dir %s: %w", templatesDir, err)
	}
	dst := filepath.Join(templatesDir, t.ID+".md")
	if err := parser.WriteFileAtomic(dst, SerializeTemplate(t)); err != nil {
		return fmt.Errorf("failed to write template file: %w", err)
	}
	return nil
}

// DeleteTemplate removes the on-disk user template with the given id. Builtin
// ids are rejected (read-only). A missing file is not an error — deleting an
// already-deleted template is a no-op success (idempotent), matching the
// user's mental model of "make it go away".
func DeleteTemplate(templatesDir, id string) error {
	if templatesDir == "" {
		return fmt.Errorf("templates directory is empty (vault not loaded)")
	}
	if id == "" {
		return fmt.Errorf("template id is required")
	}
	if IsBuiltinID(id) {
		return fmt.Errorf("cannot delete built-in template %q (built-ins are read-only)", id)
	}
	if !IsValidID(id) {
		return fmt.Errorf("invalid template id %q", id)
	}
	dst := filepath.Join(templatesDir, id+".md")
	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete template %q: %w", id, err)
	}
	return nil
}
