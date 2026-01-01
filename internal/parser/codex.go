// internal/parser/codex.go
package parser

import (
	"bufio"
	"encoding/json"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/bangarangler/burnrate/internal/pricing"
	"github.com/bangarangler/burnrate/internal/tracker"
	"github.com/fsnotify/fsnotify"
)

// CodexHistoryEntry represents an entry from ~/.codex/history.jsonl
// Note: This does NOT contain token counts - just conversation history
type CodexHistoryEntry struct {
	SessionID string `json:"session_id"`
	Timestamp int64  `json:"ts"` // Unix seconds
	Text      string `json:"text"`
}

// CodexRolloutEntry represents an entry from session rollout files
// Location: ~/.codex/sessions/YYYY/MM/DD/rollout-<timestamp>-<uuid>.jsonl
type CodexRolloutEntry struct {
	Timestamp string          `json:"timestamp"` // ISO 8601
	Item      json.RawMessage `json:"item"`
}

// CodexSessionMeta is the SessionMeta item type in rollout files
type CodexSessionMeta struct {
	SessionMeta struct {
		Meta struct {
			ID            string `json:"id"`
			Cwd           string `json:"cwd"`
			Originator    string `json:"originator"`
			CliVersion    string `json:"cli_version"`
			Source        string `json:"source"`
			ModelProvider string `json:"model_provider"`
		} `json:"meta"`
	} `json:"SessionMeta"`
}

// CodexMessage is a Message item type in rollout files
type CodexMessage struct {
	Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
		Model   string `json:"model,omitempty"`
	} `json:"Message"`
}

// CodexUsageEvent represents token usage from OTEL events (if enabled)
// Event name: codex.sse_event
type CodexUsageEvent struct {
	InputTokenCount     int    `json:"input_token_count"`
	OutputTokenCount    int    `json:"output_token_count"`
	CachedTokenCount    int    `json:"cached_token_count,omitempty"`
	ReasoningTokenCount int    `json:"reasoning_token_count,omitempty"`
	ToolTokenCount      int    `json:"tool_token_count,omitempty"`
	Model               string `json:"model,omitempty"`
}

// Track processed entries to avoid duplicates
var processedCodexSessions = make(map[string]bool)
var processedCodexRollouts = make(map[string]int64) // filename -> last processed offset

// CodexDataDir returns the Codex data directory
func CodexDataDir() string {
	// Check CODEX_HOME environment variable first
	if codexHome := os.Getenv("CODEX_HOME"); codexHome != "" {
		return codexHome
	}

	usr, _ := user.Current()
	return filepath.Join(usr.HomeDir, ".codex")
}

// StartCodexWatcher watches for new/updated Codex session files
func StartCodexWatcher() error {
	baseDir := CodexDataDir()
	sessionsDir := filepath.Join(baseDir, "sessions")

	// Check if codex directory exists
	if _, err := os.Stat(baseDir); os.IsNotExist(err) {
		tracker.Global.SetToolStatus(tracker.ToolStatus{
			Name:    "Codex",
			Tier:    tracker.TierFullTracking,
			Status:  "not_found",
			Message: "~/.codex directory not found",
		})
		return err
	}

	// Ensure sessions directory exists
	if err := os.MkdirAll(sessionsDir, 0755); err != nil {
		return err
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		tracker.Global.SetToolStatus(tracker.ToolStatus{
			Name:    "Codex",
			Tier:    tracker.TierFullTracking,
			Status:  "error",
			Message: "Failed to create watcher",
		})
		return err
	}

	// Check if OTEL is enabled for full token tracking
	otelEnabled, otelStatus := CheckCodexOTELEnabled()
	if otelEnabled {
		tracker.Global.SetToolStatus(tracker.ToolStatus{
			Name:    "Codex",
			Tier:    tracker.TierFullTracking,
			Status:  "active",
			Message: "OTEL enabled",
		})
	} else {
		tracker.Global.SetToolStatus(tracker.ToolStatus{
			Name:    "Codex",
			Tier:    tracker.TierFullTracking,
			Status:  "partial",
			Message: otelStatus,
		})
	}

	// Process existing rollout files first
	processExistingCodexSessions(sessionsDir)

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Op&fsnotify.Write == fsnotify.Write || event.Op&fsnotify.Create == fsnotify.Create {
					// Watch for new rollout files
					if strings.HasPrefix(filepath.Base(event.Name), "rollout-") && strings.HasSuffix(event.Name, ".jsonl") {
						processCodexRolloutFile(event.Name)
						tracker.Global.IncrementToolEvents("Codex")
					}
					// Also watch for new directories (date-based)
					if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
						watcher.Add(event.Name)
					}
				}
			case _, ok := <-watcher.Errors:
				if !ok {
					return
				}
			}
		}
	}()

	// Walk and watch all session directories
	filepath.Walk(sessionsDir, func(path string, info os.FileInfo, _ error) error {
		if info != nil && info.IsDir() {
			watcher.Add(path)
		}
		return nil
	})

	return nil
}

// processExistingCodexSessions walks through existing session directories
func processExistingCodexSessions(sessionsDir string) {
	filepath.Walk(sessionsDir, func(path string, info os.FileInfo, _ error) error {
		if info == nil || info.IsDir() {
			return nil
		}
		if strings.HasPrefix(filepath.Base(path), "rollout-") && strings.HasSuffix(path, ".jsonl") {
			processCodexRolloutFile(path)
		}
		return nil
	})
}

// processCodexRolloutFile parses a Codex rollout JSONL file
func processCodexRolloutFile(filename string) {
	file, err := os.Open(filename)
	if err != nil {
		return
	}
	defer file.Close()

	// Get file info for tracking
	stat, err := file.Stat()
	if err != nil {
		return
	}

	// Skip if we've already processed this file at this size
	lastOffset, exists := processedCodexRollouts[filename]
	if exists && lastOffset >= stat.Size() {
		return
	}

	// Seek to last processed position
	if exists && lastOffset > 0 {
		file.Seek(lastOffset, 0)
	}

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	var currentModel string
	var currentProvider string

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var entry CodexRolloutEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}

		// Try to extract session metadata for model info
		var sessionMeta CodexSessionMeta
		if err := json.Unmarshal(entry.Item, &sessionMeta); err == nil {
			if sessionMeta.SessionMeta.Meta.ID != "" {
				currentProvider = sessionMeta.SessionMeta.Meta.ModelProvider
				// Skip if already processed
				if processedCodexSessions[sessionMeta.SessionMeta.Meta.ID] {
					continue
				}
				processedCodexSessions[sessionMeta.SessionMeta.Meta.ID] = true
			}
			continue
		}

		// Try to extract message with model info
		var message CodexMessage
		if err := json.Unmarshal(entry.Item, &message); err == nil {
			if message.Message.Model != "" {
				currentModel = message.Message.Model
			}
			// Note: Rollout files don't contain token counts
			// Token usage only available via OTEL (if enabled)
			continue
		}
	}

	// Update processed offset
	newOffset, _ := file.Seek(0, 1) // Get current position
	if newOffset == 0 {
		newOffset = stat.Size()
	}
	processedCodexRollouts[filename] = newOffset

	// Store model info for potential future OTEL integration
	_ = currentModel
	_ = currentProvider
}

// ParseCodexOTELEvent processes an OpenTelemetry event from Codex
// This is useful if the user has OTEL export enabled
func ParseCodexOTELEvent(eventData []byte) error {
	var event CodexUsageEvent
	if err := json.Unmarshal(eventData, &event); err != nil {
		return err
	}

	if event.InputTokenCount == 0 && event.OutputTokenCount == 0 {
		return nil // No usage data
	}

	model := event.Model
	if model == "" {
		model = "codex-unknown"
	}

	// Calculate total input (including cached tokens)
	input := event.InputTokenCount + event.CachedTokenCount
	// Calculate total output (including reasoning and tool tokens)
	output := event.OutputTokenCount + event.ReasoningTokenCount + event.ToolTokenCount

	cost := pricing.CalculateCost(model, input, output)

	tracker.Global.AddUsage(model, input, output, cost)
	return nil
}

// ParseCodexHistoryOnce does a one-time parse of the Codex history file
// Note: history.jsonl does NOT contain token counts, only conversation text
// This is useful for getting session context but not for cost tracking
func ParseCodexHistoryOnce() ([]CodexHistoryEntry, error) {
	historyPath := filepath.Join(CodexDataDir(), "history.jsonl")

	file, err := os.Open(historyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No history file, not an error
		}
		return nil, err
	}
	defer file.Close()

	var entries []CodexHistoryEntry
	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var entry CodexHistoryEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		entries = append(entries, entry)
	}

	return entries, scanner.Err()
}

// GetCodexSessions returns a list of recent Codex sessions from rollout files
// Returns session IDs and their start times
func GetCodexSessions() ([]struct {
	ID        string
	StartTime time.Time
	Model     string
	Provider  string
}, error) {
	sessionsDir := filepath.Join(CodexDataDir(), "sessions")

	var sessions []struct {
		ID        string
		StartTime time.Time
		Model     string
		Provider  string
	}

	filepath.Walk(sessionsDir, func(path string, info os.FileInfo, _ error) error {
		if info == nil || info.IsDir() {
			return nil
		}
		if !strings.HasPrefix(filepath.Base(path), "rollout-") || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}

			var entry CodexRolloutEntry
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				continue
			}

			var sessionMeta CodexSessionMeta
			if err := json.Unmarshal(entry.Item, &sessionMeta); err != nil {
				continue
			}

			if sessionMeta.SessionMeta.Meta.ID != "" {
				ts, _ := time.Parse(time.RFC3339, entry.Timestamp)
				sessions = append(sessions, struct {
					ID        string
					StartTime time.Time
					Model     string
					Provider  string
				}{
					ID:        sessionMeta.SessionMeta.Meta.ID,
					StartTime: ts,
					Provider:  sessionMeta.SessionMeta.Meta.ModelProvider,
				})
				break // Only need first entry per file
			}
		}
		return nil
	})

	return sessions, nil
}

// CheckCodexOTELEnabled checks if OTEL export is enabled in Codex config
func CheckCodexOTELEnabled() (bool, string) {
	configPath := filepath.Join(CodexDataDir(), "config.toml")

	data, err := os.ReadFile(configPath)
	if err != nil {
		return false, ""
	}

	content := string(data)

	// Simple check for OTEL exporter setting
	// A proper implementation would use a TOML parser
	if strings.Contains(content, "[otel]") {
		if strings.Contains(content, `exporter = "otlp-http"`) ||
			strings.Contains(content, `exporter = "otlp-grpc"`) {
			return true, "enabled"
		}
	}

	return false, "disabled (set [otel] exporter in config.toml)"
}
