// internal/parser/aider.go
package parser

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/bangarangler/burnrate/internal/tracker"
	"github.com/fsnotify/fsnotify"
)

// AiderAnalyticsEvent represents a single event from Aider's analytics JSONL log
// The format is: {"event": "...", "properties": {...}, "user_id": "...", "time": ...}
type AiderAnalyticsEvent struct {
	Event      string               `json:"event"`
	Properties AiderEventProperties `json:"properties"`
	UserID     string               `json:"user_id"`
	Time       int64                `json:"time"` // Unix timestamp (seconds)
}

// AiderEventProperties contains the relevant fields from message_send events
type AiderEventProperties struct {
	MainModel        string  `json:"main_model"`
	WeakModel        string  `json:"weak_model"`
	EditorModel      string  `json:"editor_model"`
	EditFormat       string  `json:"edit_format"`
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	Cost             float64 `json:"cost"`       // Per-request cost
	TotalCost        float64 `json:"total_cost"` // Cumulative session cost
}

// Track processed events to avoid duplicates
var processedAiderEvents = make(map[string]bool)

// Default analytics log paths to check
var defaultAiderLogPaths = []string{
	"~/.aider/usage.jsonl",
	"~/.aider/analytics.jsonl",
	".aider.analytics.jsonl", // Current directory
}

// StartAiderWatcher watches for updates to Aider analytics log files
func StartAiderWatcher(logPath string) error {
	usr, _ := user.Current()

	// Expand ~ in path
	if strings.HasPrefix(logPath, "~") {
		logPath = filepath.Join(usr.HomeDir, logPath[1:])
	}

	// If no specific path provided, try to find an existing log
	if logPath == "" {
		logPath = findAiderLogFile()
		if logPath == "" {
			// Default to ~/.aider/usage.jsonl
			logPath = filepath.Join(usr.HomeDir, ".aider", "usage.jsonl")
		}
	}

	// Check if log file or directory exists
	logExists := false
	if _, err := os.Stat(logPath); err == nil {
		logExists = true
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		tracker.Global.SetToolStatus(tracker.ToolStatus{
			Name:    "Aider",
			Tier:    tracker.TierFullTracking,
			Status:  "error",
			Message: "Failed to create watcher",
		})
		return err
	}

	// Process existing events first
	processAiderLogFile(logPath)

	// Set initial status based on whether we found a log
	if logExists {
		tracker.Global.SetToolStatus(tracker.ToolStatus{
			Name:    "Aider",
			Tier:    tracker.TierFullTracking,
			Status:  "active",
			Message: "Watching analytics log",
		})
	} else {
		tracker.Global.SetToolStatus(tracker.ToolStatus{
			Name:    "Aider",
			Tier:    tracker.TierFullTracking,
			Status:  "waiting",
			Message: "Waiting for log file",
		})
	}

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Op&fsnotify.Write == fsnotify.Write {
					processAiderLogFile(event.Name)
				}
				// Update status to active when we see file activity
				if event.Op&fsnotify.Create == fsnotify.Create || event.Op&fsnotify.Write == fsnotify.Write {
					tracker.Global.SetToolStatus(tracker.ToolStatus{
						Name:    "Aider",
						Tier:    tracker.TierFullTracking,
						Status:  "active",
						Message: "Watching analytics log",
					})
				}
			case _, ok := <-watcher.Errors:
				if !ok {
					return
				}
			}
		}
	}()

	// Watch the log file directory (fsnotify can't watch non-existent files)
	dir := filepath.Dir(logPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	if err := watcher.Add(dir); err != nil {
		return err
	}

	// Also watch the file itself if it exists
	if _, err := os.Stat(logPath); err == nil {
		watcher.Add(logPath)
	}

	return nil
}

// findAiderLogFile looks for an existing Aider analytics log file
func findAiderLogFile() string {
	usr, _ := user.Current()

	for _, path := range defaultAiderLogPaths {
		expanded := path
		if strings.HasPrefix(path, "~") {
			expanded = filepath.Join(usr.HomeDir, path[1:])
		}
		if _, err := os.Stat(expanded); err == nil {
			return expanded
		}
	}
	return ""
}

// processAiderLogFile reads and processes new events from an Aider analytics log
func processAiderLogFile(filename string) {
	file, err := os.Open(filename)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	// Increase buffer size for potentially long lines
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var event AiderAnalyticsEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}

		// Only process message_send events (they contain token/cost data)
		if event.Event != "message_send" {
			continue
		}

		// Skip if already processed (using timestamp + model as unique key)
		eventKey := makeAiderEventKey(event)
		if processedAiderEvents[eventKey] {
			continue
		}
		processedAiderEvents[eventKey] = true

		// Skip events with no token usage
		if event.Properties.TotalTokens == 0 {
			continue
		}

		// Use the main model for display
		model := event.Properties.MainModel
		if model == "" {
			model = "aider-unknown"
		}

		// Use the pre-calculated cost from Aider if available
		cost := event.Properties.Cost

		tracker.Global.AddUsage(
			model,
			event.Properties.PromptTokens,
			event.Properties.CompletionTokens,
			cost,
		)
		tracker.Global.IncrementToolEvents("Aider")
	}
}

// makeAiderEventKey creates a unique key for deduplication
func makeAiderEventKey(event AiderAnalyticsEvent) string {
	return fmt.Sprintf("%s:%s:%s:%d",
		event.UserID,
		event.Properties.MainModel,
		time.Unix(event.Time, 0).Format(time.RFC3339),
		event.Properties.TotalTokens)
}

// ParseAiderLogOnce does a one-time parse of an Aider analytics log file
// Useful for the dashboard to load historical data
func ParseAiderLogOnce(logPath string) error {
	usr, _ := user.Current()

	// Expand ~ in path
	if strings.HasPrefix(logPath, "~") {
		logPath = filepath.Join(usr.HomeDir, logPath[1:])
	}

	if logPath == "" {
		logPath = findAiderLogFile()
	}

	if logPath == "" {
		return nil // No log file found, not an error
	}

	processAiderLogFile(logPath)
	return nil
}
