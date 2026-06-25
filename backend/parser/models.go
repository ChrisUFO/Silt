package parser

type BlockType string

const (
	BlockTask   BlockType = "TASK"
	BlockNote   BlockType = "NOTE"
	BlockHeader BlockType = "HEADER"
	// BlockCode is a managed fenced code block (#189). Unlike NOTE/TASK/HEADER
	// it is inherently multi-line: CleanText retains internal newlines and
	// renderBlock emits them verbatim (no `\n`→space collapse). The on-disk
	// form is the GFM fence plus a trailing block-identity comment line:
	//
	//   ```lang
	//   <code>
	//   ```
	//   <!-- id: uuid @ date -->
	//
	// The trailing comment sits on its own line so the closing fence stays
	// strictly GFM (no trailing content) and the block round-trips through
	// Obsidian / GitHub / VS Code unchanged. Language carries the info string
	// from the opening fence ("" for a bare ```).
	BlockCode BlockType = "CODE"
	// BlockTable is a managed GFM table (#310). Like BlockCode it is inherently
	// multi-line: CleanText retains the full GFM pipe syntax (header, separator,
	// data rows) with internal newlines, and renderBlock emits them verbatim.
	// The on-disk form is standard GFM plus a trailing block-identity comment:
	//
	//   | a | b |
	//   |---|---|
	//   | 1 | 2 |
	//   <!-- id: uuid @ date -->
	//
	// The trailing comment sits on its own line so the table stays strictly GFM
	// and the block round-trips through Obsidian / GitHub / VS Code unchanged.
	// This generalizes the code-block model: every multi-line block is ONE
	// managed entity at every layer.
	BlockTable BlockType = "TABLE"
	// BlockDetails is a managed foldable <details> HTML region (#310). Like
	// BlockCode/BlockTable it is inherently multi-line: CleanText retains the
	// full <details>…</details> HTML with internal newlines, and renderBlock
	// emits it verbatim. The on-disk form is standard HTML plus a trailing
	// block-identity comment:
	//
	//   <details>
	//   <summary>Title</summary>
	//   body
	//   </details>
	//   <!-- id: uuid @ date -->
	//
	// Nested <details> are depth-counted through the matching </details>.
	BlockDetails BlockType = "DETAILS"
	// BlockCallout is a managed Obsidian-style callout / admonition (#308).
	// Like the other multi-line types it is inherently multi-line: CleanText
	// retains the full `> [!variant] message` + subsequent `>` body lines with
	// internal newlines. The on-disk form is standard Obsidian callout syntax
	// plus a trailing block-identity comment:
	//
	//   > [!note] Title
	//   > Body paragraph
	//   >
	//   > Second paragraph
	//   <!-- id: uuid @ date -->
	//
	// The region opens at `> [!variant]` and absorbs all subsequent `>` lines
	// (including bare `>` for paragraph breaks). It ends at the first non-`>`
	// line. A plain `> text` (without `[!`) is NOT a callout opener — it stays
	// a NOTE block with a quote prefix.
	BlockCallout BlockType = "CALLOUT"
)

type ParsedBlock struct {
	ID         string    `json:"id"`
	ParentID   string    `json:"parent_id"`
	Type       BlockType `json:"type"`
	Depth      int       `json:"depth"`
	RawText    string    `json:"raw_text"`
	CleanText  string    `json:"clean_text"`
	Status     string    `json:"status,omitempty"`
	Owner      string    `json:"owner,omitempty"`
	StartDate  string    `json:"start_date,omitempty"`
	DueDate    string    `json:"due_date,omitempty"`
	Priority   int       `json:"priority,omitempty"`
	// Pinned is the user-set "sticky" flag surfaced in the Kanban card
	// chrome (`[pin:: true]` / `[pinned:: true]` in the markdown inline task
	// syntax). It is a TRI-STATE pointer so the renderer can distinguish
	// "no pin token present" (nil → omit the token) from an explicit
	// `[pin:: false]` (&false → emit the token). This preserves a typed
	// `[pin:: false]` across parse → render without polluting ExtraTokens
	// (#123). It is user intent and lives only in the file; the SQLite
	// index caches a 0/1 projection for query speed but the file is the
	// source of truth.
	Pinned *bool `json:"pinned,omitempty"`
	// Progress is a 0-100 user-set progress indicator (`[progress:: N]`
	// or `[prog:: N]` in the markdown inline task syntax). 0 = not set
	// (renderer omits the marker). Lives only in the file; SQLite caches
	// for query speed.
	Progress int `json:"progress,omitempty"`
	// ExtraTokens preserves unknown [key:: value] Dataview tokens that the
	// parser doesn't recognise (e.g. `[project:: alpha]`, `[estimate:: 3h]`).
	// These round-trip through parse → render so files stay interoperable
	// with Dataview-compatible (SPECS.md §4.1). Each entry is the full
	// `[key:: value]` string as it appeared in the source.
	ExtraTokens []string `json:"extra_tokens,omitempty"`
	LineNumber  int      `json:"line_number"`
	FileDate   string `json:"file_date,omitempty"`
	// Language is the info string of a fenced code block's opening fence
	// (BlockCode only, #189). "" for a bare ``` fence. It is the Shiki grammar
	// identifier and round-trips as the ```{lang} prefix.
	Language string `json:"language,omitempty"`
}

type FileMetadata struct {
	Notebook string   `yaml:"notebook"`
	Section  string   `yaml:"section"`
	Page     string   `yaml:"page"`
	Date     string   `yaml:"date"` // YYYY-MM-DD
	Tags     []string `yaml:"tags"`

	// Warnings carries non-fatal parse diagnostics (for example, a
	// malformed YAML frontmatter that was treated as "no metadata").
	// Callers should surface these so users can fix the source note
	// rather than silently inheriting path-derived defaults.
	Warnings []string `yaml:"-"`
}

type TaskQueryFilter struct {
	Owner     string   `json:"owner"`
	Priority  int      `json:"priority"`
	Tags      []string `json:"tags"`
	StartDate string   `json:"start_date"`
	EndDate   string   `json:"end_date"`
}

// NavigationTree describes the Notebook > Section > Page hierarchy for the
// sidebar navigator. Counts are block counts so the UI can render badges.
// Sections nest via `Children` (#88) so deeply-nested folders like
// `Work/Projects/Active/Site.md` appear as a tree rather than being lost
// behind a flat single-level list.
type NavigationPage struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type NavigationSection struct {
	Name     string              `json:"name"`
	Path     string              `json:"path,omitempty"`
	Pages    []NavigationPage    `json:"pages"`
	Children []NavigationSection `json:"children,omitempty"`
}

type NavigationNotebook struct {
	Name     string               `json:"name"`
	Sections []NavigationSection  `json:"sections"`
	// Source is 'vault' for an in-vault notebook or 'linked:<id>' for an
	// external/linked notebook (#100). The frontend badges linked notebooks
	// and the editor passes it back so writes resolve the correct root.
	Source    string `json:"source,omitempty"`
	// RootPath is the absolute content root (vault notebook dir or linked
	// root); surfaced for tooltips. Empty for vault notebooks (derivable).
	RootPath  string `json:"root_path,omitempty"`
	// Disconnected is true when a linked notebook's root could not be read
	// (offline mount); its last-synced index rows still show. Vault-only.
	Disconnected bool `json:"disconnected,omitempty"`
}

type NavigationTree struct {
	Notebooks []NavigationNotebook `json:"notebooks"`
}

// BlockReference is the resolved location+content of a ((uuid)) block
// reference, used for hover previews and scroll-to-source navigation.
type BlockReference struct {
	ID         string `json:"id"`
	Exists     bool   `json:"exists"`
	CleanText  string `json:"clean_text"`
	RawText    string `json:"raw_text"`
	Type       string `json:"type"`
	Notebook   string `json:"notebook"`
	Section    string `json:"section"`
	Page       string `json:"page"`
	FileDate   string `json:"file_date"`
	LineNumber int    `json:"line_number"`
}

// BlockChangedEvent is the payload of the "block:changed" Wails event,
// broadcast after any block mutation so live embeds/references can refresh.
type BlockChangedEvent struct {
	ID       string `json:"id"`
	Notebook string `json:"notebook"`
	Section  string `json:"section"`
	Page     string `json:"page"`
	FileDate string `json:"file_date"`
}

// TagNode is one node of the hierarchical tag tree returned by QueryTagHierarchy.
type TagNode struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"` // full slash path to this node
	Count    int       `json:"count"` // distinct blocks at or beneath this node
	Children []TagNode `json:"children"`
}

// PluginRegistry mirrors the `plugins:` block of .system/config.yaml.
type PluginRegistry struct {
	Active   []string                `json:"active"`
	Disabled []string                `json:"disabled"`
	Settings map[string]any          `json:"settings"`
}

// PluginInfo describes a discovered plugin folder under .system/plugins/.
type PluginInfo struct {
	ID          string         `json:"id"`
	HasManifest bool           `json:"has_manifest"`
	HasIndex    bool           `json:"has_index"`
	Disabled    bool           `json:"disabled"`
	Name        string         `json:"name,omitempty"`
	Version     string         `json:"version,omitempty"`
	Author      string         `json:"author,omitempty"`
	Description string         `json:"description,omitempty"`
	Icon        string         `json:"icon,omitempty"`
	// Capabilities is the v2 SDK capability declaration (#113) read from the
	// installed plugin.json (capability id → true | scope string). The plugin
	// manager surfaces requested-vs-granted in Settings → Plugins. Absent for
	// plugins that use only the read-only SDK.
	Capabilities map[string]any `json:"capabilities,omitempty"`
	// Settings is the declarative settings schema (#103) read from the
	// installed plugin.json. The plugin manager renders the settings form from
	// it generically.
	Settings []map[string]any `json:"settings,omitempty"`
	// Homepage / UpdateURL are optional distribution-v2 fields (#111).
	Homepage  string `json:"homepage,omitempty"`
	UpdateURL string `json:"updateUrl,omitempty"`
	// ContentSHA256 is the sha256 of the installed index.js (#161), used by
	// the frontend loader to verify runtime integrity before Blob import.
	ContentSHA256 string `json:"contentSha256,omitempty"`
}

// PluginManifest is the plugin.json schema carried inside a .silt-plugin
// archive (mirrors backend/plugins.Manifest, re-declared here so it crosses
// the Wails IPC boundary without an import cycle).
type PluginManifest struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	Version        string         `json:"version"`
	Author         string         `json:"author,omitempty"`
	Description    string         `json:"description,omitempty"`
	Icon           string         `json:"icon,omitempty"`
	Main           string         `json:"main,omitempty"`
	MinSiltVersion string         `json:"minSiltVersion,omitempty"`
	// Capabilities mirrors the v2 SDK capability declaration (#113).
	Capabilities map[string]any `json:"capabilities,omitempty"`
	// Settings mirrors the declarative settings schema (#103).
	Settings []map[string]any `json:"settings,omitempty"`
	// Homepage is an optional URL for the plugin's homepage (#111).
	Homepage string `json:"homepage,omitempty"`
	// UpdateURL is an optional URL for update checks (#111).
	UpdateURL string `json:"updateUrl,omitempty"`
	// ContentSHA256 is the sha256 of index.js, set at install time (#161).
	ContentSHA256 string `json:"contentSha256,omitempty"`
}

// PluginValidationResult bundles a validated plugin manifest with the
// non-fatal warnings produced during validation, so both cross the Wails IPC
// boundary in a single return value (Wails bindings only expose the first
// non-error return value).
type PluginValidationResult struct {
	Manifest PluginManifest `json:"manifest"`
	Warnings []string       `json:"warnings"`
}

type TaskResult struct {
	ID           string    `json:"id"`
	ParentID     string    `json:"parent_id"`
	Notebook     string    `json:"notebook"`
	Section      string    `json:"section"`
	Page         string    `json:"page"`
	FileDate     string    `json:"file_date"`
	Depth        int       `json:"depth"`
	RawContent   string    `json:"raw_content"`
	CleanContent string    `json:"clean_content"`
	LineNumber   int       `json:"line_number"`
	Status       string    `json:"status"` // TODO, DOING, DONE
	Owner        string    `json:"owner,omitempty"`
	StartDate    string    `json:"start_date,omitempty"`
	DueDate      string    `json:"due_date,omitempty"`
	Priority     int       `json:"priority,omitempty"`
	// Pinned + Progress mirror the ParsedBlock fields (see ARCHITECTURE.md
	// §0 "Storage-of-Truth Tiers" — these are file-resident user intent;
	// the SQLite index is allowed to cache them, not to own them). Pinned
	// is a tri-state pointer for parity with ParsedBlock.Pinned (#123).
	Pinned       *bool    `json:"pinned,omitempty"`
	Progress     int       `json:"progress,omitempty"`
	// CommentsCount is the number of indented child NOTE blocks beneath
	// this task (the "comments on a task" UX from the Stitch reference).
	// It is computed at index time from `blocks.parent_id` and cached on
	// the task row in SQLite so the Kanban query can serve it in O(1).
	CommentsCount int `json:"comments_count,omitempty"`
	// LinksCount is the number of `((uuid))` block references in this
	// task's body. Computed at index time from `blocks.raw_content` and
	// cached on the task row; re-derivable from the markdown on every
	// re-index, so SQLite holding it is consistent with the
	// "working-memory only" tier rule.
	LinksCount    int `json:"links_count,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	// Snippet is the FTS5 snippet (with <mark>...</mark> highlights) for
	// search results; empty for non-search queries.
	Snippet      string    `json:"snippet,omitempty"`
}

// SearchResult is the paginated envelope returned by SearchBlocksPaged: the
// ranked results, the total match count (for "showing N of M"), and a HasMore
// flag so the frontend can stop fetching once everything is loaded.
type SearchResult struct {
	Results []TaskResult `json:"results"`
	Total   int          `json:"total"`
	Offset  int          `json:"offset"`
	Limit   int          `json:"limit"`
	HasMore bool         `json:"has_more"`
}
