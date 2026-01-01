// internal/parser/opencode.go
package parser

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"

	"github.com/bangarangler/burnrate/internal/pricing"
	"github.com/bangarangler/burnrate/internal/tracker"
	"github.com/fsnotify/fsnotify"
)

// Real structure from OpenCode message files (2026)
type Message struct {
	ID         string  `json:"id"`
	SessionID  string  `json:"sessionID"`
	Role       string  `json:"role"`
	ModelID    string  `json:"modelID"`
	ProviderID string  `json:"providerID"`
	Cost       float64 `json:"cost,omitempty"` // Sometimes pre-calculated
	Tokens     struct {
		Input     int `json:"input"`
		Output    int `json:"output"`
		Reasoning int `json:"reasoning"`
		Cache     struct {
			Read  int `json:"read"`
			Write int `json:"write"`
		} `json:"cache"`
	} `json:"tokens"`
	Timestamp int64 `json:"time.created"` // Unix milli
}

var watchedPaths = make(map[string]bool)
var processedMessageIDs = make(map[string]bool) // Track processed messages to avoid duplicates
var processedMu sync.Mutex                      // Protect the map

// StartOpenCodeWatcher watches for new/updated message files
func StartOpenCodeWatcher() error {
	usr, _ := user.Current()
	basePath := filepath.Join(usr.HomeDir, ".local", "share", "opencode", "storage", "message")

	// Check if the storage directory exists
	if _, err := os.Stat(basePath); os.IsNotExist(err) {
		tracker.Global.SetToolStatus(tracker.ToolStatus{
			Name:    "OpenCode",
			Tier:    tracker.TierFullTracking,
			Status:  "not_found",
			Message: "Storage directory not found",
		})
		return err
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		tracker.Global.SetToolStatus(tracker.ToolStatus{
			Name:    "OpenCode",
			Tier:    tracker.TierFullTracking,
			Status:  "error",
			Message: "Failed to create watcher",
		})
		return err
	}

	// Report active status
	tracker.Global.SetToolStatus(tracker.ToolStatus{
		Name:    "OpenCode",
		Tier:    tracker.TierFullTracking,
		Status:  "active",
		Message: "Watching storage",
	})

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				// Handle new session directories - add them to the watcher
				if event.Op&fsnotify.Create == fsnotify.Create {
					if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
						if strings.HasPrefix(filepath.Base(event.Name), "ses_") {
							if !watchedPaths[event.Name] {
								watcher.Add(event.Name)
								watchedPaths[event.Name] = true
							}
						}
					}
				}
				// Handle both Create and Write events - deduplication handles duplicates
				if event.Op&fsnotify.Write == fsnotify.Write || event.Op&fsnotify.Create == fsnotify.Create {
					if strings.HasPrefix(filepath.Base(event.Name), "msg_") && strings.HasSuffix(event.Name, ".json") {
						parseMessageFile(event.Name)
					}
				}
			case _, ok := <-watcher.Errors:
				if !ok {
					return
				}
			}
		}
	}()

	// Add existing session directories
	filepath.Walk(basePath, func(path string, info fs.FileInfo, _ error) error {
		if info.IsDir() && path != basePath {
			if !watchedPaths[path] {
				watcher.Add(path)
				watchedPaths[path] = true
			}
		}
		return nil
	})

	// Watch base for new sessions
	watcher.Add(basePath)

	return nil
}

// parseMessageFile processes a single message file
func parseMessageFile(filename string) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return
	}

	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		return
	}

	// Skip messages without real token usage (user messages, incomplete writes)
	// Don't mark as processed yet - we may get a WRITE event with actual data later
	if msg.Tokens.Input == 0 && msg.Tokens.Output == 0 {
		return
	}

	// Skip if already processed (deduplication with mutex for thread safety)
	processedMu.Lock()
	if processedMessageIDs[msg.ID] {
		processedMu.Unlock()
		return
	}
	processedMessageIDs[msg.ID] = true
	processedMu.Unlock()

	model := msg.ModelID
	if msg.ProviderID != "" {
		model = model + " (" + msg.ProviderID + ")"
	}

	input := msg.Tokens.Input + msg.Tokens.Cache.Read
	output := msg.Tokens.Output + msg.Tokens.Reasoning + msg.Tokens.Cache.Write

	// Prefer pre-calculated cost if available and reasonable
	var cost float64
	if msg.Cost > 0 && msg.Cost < 100 { // Sanity check
		cost = msg.Cost
	} else {
		cost = pricing.CalculateCost(msg.ModelID, input, output)
	}

	tracker.Global.AddUsage(model, input, output, cost)
	tracker.Global.IncrementToolEvents("OpenCode")
}
