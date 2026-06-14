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
	LineNumber int       `json:"line_number"`
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

type DayGroup struct {
	Date          string        `json:"date"` // YYYY-MM-DD
	FormattedDate string        `json:"formattedDate"`
	Blocks        []ParsedBlock `json:"blocks"`
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
	Tags         []string  `json:"tags,omitempty"`
}
