package monitor

import (
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"notes-sharp/backend/db"
	"notes-sharp/backend/parser"

	"github.com/fsnotify/fsnotify"
)

type DirectoryWatcher struct {
	watcher      *fsnotify.Watcher
	vaultPath    string
	dm           *db.DatabaseManager
	tracker      *WriteTracker
	spacesPerTab int
	closeChan    chan struct{}
}

func NewDirectoryWatcher(vaultPath string, dm *db.DatabaseManager, tracker *WriteTracker, spacesPerTab int) (*DirectoryWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create fsnotify watcher: %w", err)
	}

	return &DirectoryWatcher{
		watcher:      watcher,
		vaultPath:    vaultPath,
		dm:           dm,
		tracker:      tracker,
		spacesPerTab: spacesPerTab,
		closeChan:    make(chan struct{}),
	}, nil
}

func (dw *DirectoryWatcher) Close() error {
	close(dw.closeChan)
	return dw.watcher.Close()
}

func (dw *DirectoryWatcher) AddRecursive(path string) error {
	return filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			// Skip system and hidden directories
			if strings.HasPrefix(name, ".") && name != "." {
				return filepath.SkipDir
			}
			if err := dw.watcher.Add(p); err != nil {
				return fmt.Errorf("failed to add path to watcher %s: %w", p, err)
			}
		}
		return nil
	})
}

func (dw *DirectoryWatcher) Start() error {
	if err := dw.AddRecursive(dw.vaultPath); err != nil {
		return err
	}

	go dw.listenLoop()
	return nil
}

func (dw *DirectoryWatcher) resolveFileMetadata(path string) (string, string, string) {
	relPath, err := filepath.Rel(dw.vaultPath, path)
	if err != nil {
		return "General", "General", time.Now().Format("2006-01-02")
	}

	relPathClean := filepath.ToSlash(relPath)
	parts := strings.Split(relPathClean, "/")

	notebook := "General"
	section := "General"
	filename := parts[len(parts)-1]

	if len(parts) >= 3 {
		notebook = parts[0]
		section = strings.Join(parts[1:len(parts)-1], "/")
	} else if len(parts) == 2 {
		notebook = parts[0]
	}

	dateStr := ""
	if matches := parser.DateFileRegex.FindStringSubmatch(filename); len(matches) > 1 {
		dateStr = matches[1]
	} else {
		info, err := os.Stat(path)
		if err == nil {
			dateStr = info.ModTime().Format("2006-01-02")
		} else {
			dateStr = time.Now().Format("2006-01-02")
		}
	}

	return notebook, section, dateStr
}

func (dw *DirectoryWatcher) listenLoop() {
	for {
		select {
		case event, ok := <-dw.watcher.Events:
			if !ok {
				return
			}

			path := filepath.Clean(event.Name)

			// Check if directory
			info, err := os.Stat(path)
			isDir := false
			if err == nil {
				isDir = info.IsDir()
			}

			if isDir {
				// If new directory is created, watch it recursively
				if event.Has(fsnotify.Create) {
					_ = dw.AddRecursive(path)
				}
				continue
			}

			// Only process markdown files
			if strings.ToLower(filepath.Ext(path)) != ".md" {
				continue
			}

			// Ignore self-generated writes
			if dw.tracker.IsSelfGenerated(path) {
				continue
			}

			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
				dw.reindexFile(path)
			} else if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
				dw.clearIndexForFile(path)
			}

		case err, ok := <-dw.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("DirectoryWatcher error: %v", err)

		case <-dw.closeChan:
			return
		}
	}
}

func (dw *DirectoryWatcher) reindexFile(path string) {
	notebook, section, dateStr := dw.resolveFileMetadata(path)
	contentBytes, err := os.ReadFile(path)
	if err != nil {
		return
	}

	blocks, meta, newContent, modified, err := parser.ParseFileContent(string(contentBytes), notebook, section, dateStr, dw.spacesPerTab)
	if err != nil {
		return
	}

	if modified {
		// Register write before modifying the file to prevent loop triggers
		dw.tracker.RegisterWrite(path)
		_ = parser.WriteFileAtomic(path, []byte(newContent))
	}

	_ = dw.dm.IndexFileBlocks(meta.Notebook, meta.Section, meta.Date, blocks, meta.Tags)
}

func (dw *DirectoryWatcher) clearIndexForFile(path string) {
	notebook, section, dateStr := dw.resolveFileMetadata(path)
	_ = dw.dm.ClearFileBlocks(nil, notebook, section, dateStr)
}
