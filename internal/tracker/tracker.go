// internal/tracker/tracker.go
package tracker

import (
	"fmt"
	"github.com/bangarangler/burnrate/internal/storage"
	"sort"
	"sync"
	"time"
)

// ToolTier represents the support level for a tool
type ToolTier int

const (
	TierFullTracking  ToolTier = 1 // Log parsers with full token/cost tracking
	TierDetectionOnly ToolTier = 2 // Detection only, links to external dashboard
)

// ToolStatus represents the current state of a tracked tool
type ToolStatus struct {
	Name          string    // Display name: "OpenCode", "Copilot", etc.
	Tier          ToolTier  // Support tier
	Status        string    // "active", "partial", "configured", "not_found"
	Message       string    // Human-readable explanation
	DashboardURL  string    // External dashboard URL (Tier 2 tools)
	EventCount    int       // Number of events tracked this session
	LastEventTime time.Time // Timestamp of last event
}

type Usage struct {
	Model            string    `json:"model"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	TotalTokens      int       `json:"total_tokens"`
	Cost             float64   `json:"cost"`
	Timestamp        time.Time `json:"timestamp"`
}

type Tracker struct {
	mu            sync.RWMutex
	SessionCost   float64
	SessionUsages []Usage
	StartTime     time.Time
	ToolStatuses  map[string]*ToolStatus
}

var Global = &Tracker{
	StartTime:    time.Now(),
	ToolStatuses: make(map[string]*ToolStatus),
}

// AddUsage adds a new usage entry and updates the session cost
func (t *Tracker) AddUsage(model string, prompt, completion int, cost float64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Try to determine the tool name (this is a bit hacky, better to pass it in)
	// For now, we'll infer it or default to "Unknown" if we can't find a better way easily without breaking API
	// Ideally, AddUsage should take a toolName parameter.
	// Since we can't change the signature easily without updating all callers, let's defer DB writing to a new method
	// or update the signature. Given we control the codebase, let's update the signature.

	usage := Usage{
		Model:            model,
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      prompt + completion,
		Cost:             cost,
		Timestamp:        time.Now(),
	}

	t.SessionUsages = append(t.SessionUsages, usage)
	t.SessionCost += cost

	fmt.Printf("💸 +$%.4f (%s) | Total: $%.4f\n", cost, model, t.SessionCost)
}

// AddUsageWithTool adds usage and records it to the database
func (t *Tracker) AddUsageWithTool(tool, model string, prompt, completion int, cost float64) {
	t.AddUsage(model, prompt, completion, cost)

	// Record to history DB
	// We ignore errors here to avoid disrupting the UI flow, but we could log them
	_ = storage.RecordUsage(tool, model, prompt, completion, cost)
}

// GetSessionCost returns the current session cost safely
func (t *Tracker) GetSessionCost() float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.SessionCost
}

// GetBurnRatePerHour returns the current burn rate in $/hour safely
func (t *Tracker) GetBurnRatePerHour() float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if len(t.SessionUsages) == 0 {
		return 0
	}

	duration := time.Since(t.StartTime).Hours()
	if duration <= 0 {
		return 0
	}

	return t.SessionCost / duration
}

// GetUsages returns a safe copy of the current usages for display in the TUI
func (t *Tracker) GetUsages() []Usage {
	t.mu.RLock()
	defer t.mu.RUnlock()

	// Return a copy to avoid race conditions while rendering
	copyUsages := make([]Usage, len(t.SessionUsages))
	copy(copyUsages, t.SessionUsages)
	return copyUsages
}

// Reset clears the current session
func (t *Tracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.SessionCost = 0
	t.SessionUsages = nil
	t.StartTime = time.Now()
}

// GetSummary returns a formatted string summary (useful for future commands)
func (t *Tracker) GetSummary() string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	rate := t.GetBurnRatePerHour()
	return fmt.Sprintf("Session: $%.4f | Burn rate: $%.2f/hr | Calls: %d",
		t.SessionCost, rate, len(t.SessionUsages))
}

// SetToolStatus sets or updates the status for a tool
func (t *Tracker) SetToolStatus(status ToolStatus) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.ToolStatuses == nil {
		t.ToolStatuses = make(map[string]*ToolStatus)
	}
	t.ToolStatuses[status.Name] = &status
}

// GetToolStatuses returns all tool statuses sorted by name
func (t *Tracker) GetToolStatuses() []*ToolStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()

	statuses := make([]*ToolStatus, 0, len(t.ToolStatuses))
	for _, s := range t.ToolStatuses {
		statuses = append(statuses, s)
	}

	// Sort by name for consistent display
	sort.Slice(statuses, func(i, j int) bool {
		return statuses[i].Name < statuses[j].Name
	})

	return statuses
}

// IncrementToolEvents increments the event count and updates last event time for a tool
func (t *Tracker) IncrementToolEvents(toolName string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.ToolStatuses == nil {
		return
	}
	if status, ok := t.ToolStatuses[toolName]; ok {
		status.EventCount++
		status.LastEventTime = time.Now()
	}
}

// GetToolStatus returns the status for a specific tool
func (t *Tracker) GetToolStatus(toolName string) *ToolStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.ToolStatuses == nil {
		return nil
	}
	return t.ToolStatuses[toolName]
}

// GetHistoricalUsage returns usage summary for Today or Week from DB
func (t *Tracker) GetHistoricalUsage(window string) ([]Usage, float64, error) {
	var since int64
	now := time.Now()

	switch window {
	case "today":
		// Midnight today
		since = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
	case "week":
		// 7 days ago
		since = now.AddDate(0, 0, -7).Unix()
	default:
		return nil, 0, fmt.Errorf("invalid window: %s", window)
	}

	summary, total, err := storage.GetUsageSummary(since)
	if err != nil {
		return nil, 0, err
	}

	// Convert to []Usage for UI
	var usages []Usage
	for model, data := range summary {
		usages = append(usages, Usage{
			Model:            model,
			PromptTokens:     data.PromptTokens,
			CompletionTokens: data.CompletionTokens,
			TotalTokens:      data.PromptTokens + data.CompletionTokens,
			Cost:             data.Cost,
		})
	}

	// Sort by cost desc
	sort.Slice(usages, func(i, j int) bool {
		return usages[i].Cost > usages[j].Cost
	})

	return usages, total, nil
}
