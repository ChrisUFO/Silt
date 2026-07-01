package main

import (
	"fmt"
	"silt/backend/config"
	"silt/backend/parser"
)

// --- Sidebar width / nav order IPC (#63, #68) -----------------------------

// GetSidebarWidth returns the persisted sidebar width from config.yaml.
// Defaults to 256 when unset or below the minimum.
func (a *App) GetSidebarWidth() int {
	a.configMu.RLock()
	defer a.configMu.RUnlock()
	w := a.cfg.UI.SidebarWidth
	if w < 200 {
		return 256
	}
	return w
}

// SetSidebarWidth persists a new sidebar width to config.yaml, clamped to
// [200, 480]. Uses RegisterSelfWrite to suppress the config watcher's
// self-write loop.
func (a *App) SetSidebarWidth(px int) error {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if px < 200 {
		px = 200
	}
	if px > 480 {
		px = 480
	}
	a.configMu.Lock()
	a.cfg.UI.SidebarWidth = px
	cfg := a.cfg
	a.configMu.Unlock()

	if a.configWatcher != nil {
		a.configWatcher.RegisterSelfWrite()
	}
	return config.Save(a.vaultPath, cfg)
}

// GetNavOrder returns the persisted navigation ordering from config.yaml.
func (a *App) GetNavOrder() (config.NavOrder, error) {
	a.configMu.RLock()
	defer a.configMu.RUnlock()
	return a.cfg.UI.NavOrder, nil
}

// SetNavOrder persists a new navigation ordering to config.yaml.
func (a *App) SetNavOrder(order config.NavOrder) error {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	a.configMu.Lock()
	a.cfg.UI.NavOrder = order
	cfg := a.cfg
	a.configMu.Unlock()

	if a.configWatcher != nil {
		a.configWatcher.RegisterSelfWrite()
	}
	return config.Save(a.vaultPath, cfg)
}

// --- Open tabs IPC (#142) ------------------------------------------------

// OpenTabsResult is the GetOpenTabs IPC envelope. Wails v2 generates
// bindings for only the first non-error return value, so a multi-return
// Go signature (tabs, active, err) would lose the active tab on the JS side.
// A single struct return serializes cleanly to a JSON object.
type OpenTabsResult struct {
	OpenTabs  []config.TabRef `json:"open_tabs"`
	ActiveTab *config.TabRef  `json:"active_tab"`
}

// GetOpenTabs returns the persisted open-tab set + active tab from
// config.yaml. Only pinned tabs are persisted (preview tabs are ephemeral —
// industry-standard parity). Stale tabs — references to pages that no longer exist on
// disk (deleted/renamed since last launch) — are pruned silently against the
// live ListNavigation tree before returning, so the frontend never mounts a
// tab for a missing page. The on-disk tree is the source of truth, not the
// persisted tab list (same philosophy as the SQLite index).
func (a *App) GetOpenTabs() (OpenTabsResult, error) {
	// Lock order: vaultMu before configMu (app.go invariant). Read vaultPath
	// first under vaultMu, then the persisted tabs under configMu. The two
	// reads are independent, so the order is free — establish it consistently
	// to honour the invariant and avoid an AB-BA inversion with any binding
	// that takes both locks.
	a.vaultMu.RLock()
	vaultPath := a.vaultPath
	a.vaultMu.RUnlock()

	a.configMu.RLock()
	tabs := append([]config.TabRef(nil), a.cfg.UI.OpenTabs...)
	active := a.cfg.UI.ActiveTab
	a.configMu.RUnlock()

	if vaultPath == "" {
		return OpenTabsResult{OpenTabs: []config.TabRef{}}, nil
	}
	if len(tabs) == 0 {
		return OpenTabsResult{OpenTabs: tabs, ActiveTab: active}, nil
	}

	// Prune stale tabs against the live navigation tree. A tab is stale if
	// its (notebook, section, page) triple does not appear anywhere in the
	// tree. Best-effort: if ListNavigation fails (e.g. a temporarily
	// unreadable directory), return the persisted set unpruned rather than
	// blocking the UI. ListNavigation acquires its own vaultMu.RLock, so we
	// must not hold it here (no recursive RLock — deadlock risk if a writer
	// is waiting).
	tree, navErr := a.ListNavigation()
	if navErr != nil {
		return OpenTabsResult{OpenTabs: tabs, ActiveTab: active}, nil
	}
	validPages := navPageSet(tree)

	pruned := make([]config.TabRef, 0, len(tabs))
	for _, t := range tabs {
		// Drop entries with an empty Page (malformed YAML) or a missing
		// page on disk.
		if t.Page == "" {
			continue
		}
		key := t.Notebook + "\x00" + t.Section + "\x00" + t.Page
		if !validPages[key] {
			continue
		}
		pruned = append(pruned, t)
	}

	// If the active tab was pruned, clear it.
	if active != nil && active.Page != "" {
		key := active.Notebook + "\x00" + active.Section + "\x00" + active.Page
		if !validPages[key] {
			active = nil
		}
	}

	return OpenTabsResult{OpenTabs: pruned, ActiveTab: active}, nil
}

// SetOpenTabs persists the open-tab set + active tab to config.yaml. The
// frontend filters to pinned tabs before calling (preview tabs are
// ephemeral). Holds vaultMu.RLock across the entire call to block lifecycle
// transitions (CloseVault / MoveVault) during the disk write — matching the
// SetSidebarWidth / SetNavOrder sibling pattern. RLock does not block other
// RLock readers, so ListNavigation and GetOpenTabs continue to operate
// concurrently. The vaultMu → configMu ordering is preserved.
func (a *App) SetOpenTabs(openTabs []config.TabRef, activeTab *config.TabRef) error {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.vaultPath == "" {
		return fmt.Errorf("vault not loaded")
	}
	if openTabs == nil {
		openTabs = []config.TabRef{}
	}

	a.configMu.Lock()
	a.cfg.UI.OpenTabs = openTabs
	a.cfg.UI.ActiveTab = activeTab
	cfg := a.cfg
	a.configMu.Unlock()

	if a.configWatcher != nil {
		a.configWatcher.RegisterSelfWrite()
	}
	return config.Save(a.vaultPath, cfg)
}

// navPageSet flattens the NavigationTree into a set of
// "notebook\x00section\x00page" strings for O(1) existence checks. The
// section key mirrors the frontend's sectionKey derivation: section.path if
// present, otherwise section.name. Section-less pages match section "".
func navPageSet(tree parser.NavigationTree) map[string]bool {
	out := make(map[string]bool)
	for _, nb := range tree.Notebooks {
		collectPages(out, nb.Name, nb.Sections)
	}
	return out
}

func collectPages(out map[string]bool, notebook string, sections []parser.NavigationSection) {
	for _, sec := range sections {
		sectionKey := sec.Path
		if sectionKey == "" {
			sectionKey = sec.Name
		}
		for _, pg := range sec.Pages {
			out[notebook+"\x00"+sectionKey+"\x00"+pg.Name] = true
		}
		if len(sec.Children) > 0 {
			collectPages(out, notebook, sec.Children)
		}
	}
}
