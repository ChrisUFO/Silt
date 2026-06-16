package parser

type BlockType string

const (
	BlockTask   BlockType = "TASK"
	BlockNote   BlockType = "NOTE"
	BlockHeader BlockType = "HEADER"
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
	// chrome (`!pin` in the markdown inline task syntax). It is user
	// intent and lives only in the file; the SQLite index is allowed to
	// cache it for query speed but the file is the source of truth.
	Pinned bool `json:"pinned,omitempty"`
	// Progress is a 0-100 user-set progress indicator (`[p:N]` in the
	// markdown inline task syntax). 0 = not set (renderer omits the
	// marker). Lives only in the file; SQLite caches for query speed.
	Progress int `json:"progress,omitempty"`
	LineNumber int `json:"line_number"`
	FileDate   string `json:"file_date,omitempty"`
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
type NavigationPage struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type NavigationSection struct {
	Name  string             `json:"name"`
	Pages []NavigationPage   `json:"pages"`
}

type NavigationNotebook struct {
	Name     string               `json:"name"`
	Sections []NavigationSection  `json:"sections"`
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
	ID          string `json:"id"`
	HasManifest bool   `json:"has_manifest"`
	HasIndex    bool   `json:"has_index"`
	Disabled    bool   `json:"disabled"`
	Name        string `json:"name,omitempty"`
	Version     string `json:"version,omitempty"`
	Author      string `json:"author,omitempty"`
	Description string `json:"description,omitempty"`
	Icon        string `json:"icon,omitempty"`
}

// PluginManifest is the plugin.json schema carried inside a .silt-plugin
// archive (mirrors backend/plugins.Manifest, re-declared here so it crosses
// the Wails IPC boundary without an import cycle).
type PluginManifest struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Version        string `json:"version"`
	Author         string `json:"author,omitempty"`
	Description    string `json:"description,omitempty"`
	Icon           string `json:"icon,omitempty"`
	Main           string `json:"main,omitempty"`
	MinSiltVersion string `json:"minSiltVersion,omitempty"`
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
	// the SQLite index is allowed to cache them, not to own them).
	Pinned       bool      `json:"pinned,omitempty"`
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
