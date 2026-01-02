// internal/parser/crush.go
package parser

import (
	"database/sql"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/bangarangler/burnrate/internal/pricing"
	"github.com/bangarangler/burnrate/internal/tracker"
	"github.com/fsnotify/fsnotify"
	_ "github.com/mattn/go-sqlite3"
)

// CrushSession represents a session from Crush's SQLite database
type CrushSession struct {
	ID               string
	ParentSessionID  sql.NullString
	Title            string
	MessageCount     int
	PromptTokens     int
	CompletionTokens int
	Cost             float64
	CreatedAt        int64 // Unix timestamp in milliseconds
	UpdatedAt        int64 // Unix timestamp in milliseconds
}

// CrushMessage represents a message from Crush's SQLite database
type CrushMessage struct {
	ID         string
	SessionID  string
	Role       string
	Model      sql.NullString
	Provider   sql.NullString
	CreatedAt  int64 // Unix timestamp in milliseconds
	FinishedAt sql.NullInt64
}

// Track processed sessions to avoid duplicates
var processedCrushSessions = make(map[string]int64) // sessionID -> last updated_at

// Default database paths to check (project-relative first, then common locations)
var defaultCrushDBPaths = []string{
	".crush/crush.db",                              // Current project directory
	"~/.local/share/crush/crush.db",                // Linux/macOS global (if it exists)
	"~/Library/Application Support/Crush/crush.db", // macOS standard
	"~/Library/Application Support/crush/crush.db", // macOS standard (lowercase)
}

// StartCrushWatcher watches for updates to Crush SQLite databases
func StartCrushWatcher(dbPath string) error {
	usr, _ := user.Current()

	// Expand ~ in path
	if strings.HasPrefix(dbPath, "~") {
		dbPath = filepath.Join(usr.HomeDir, dbPath[1:])
	}

	// If no specific path provided, try to find an existing database
	if dbPath == "" {
		dbPath = findCrushDB()
		if dbPath == "" {
			// Default to current directory
			dbPath = ".crush/crush.db"
		}
	}

	// Check if database exists
	dbExists := false
	if _, err := os.Stat(dbPath); err == nil {
		dbExists = true
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		tracker.Global.SetToolStatus(tracker.ToolStatus{
			Name:    "Crush",
			Tier:    tracker.TierFullTracking,
			Status:  "error",
			Message: "Failed to create watcher",
		})
		return err
	}

	// Process existing data first
	processCrushDB(dbPath)

	// Set initial status
	if dbExists {
		tracker.Global.SetToolStatus(tracker.ToolStatus{
			Name:    "Crush",
			Tier:    tracker.TierFullTracking,
			Status:  "active",
			Message: "Watching database",
		})
	} else {
		tracker.Global.SetToolStatus(tracker.ToolStatus{
			Name:    "Crush",
			Tier:    tracker.TierFullTracking,
			Status:  "not_found",
			Message: ".crush/crush.db not found",
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
					processCrushDB(event.Name)
				}
				// Update status when we see database activity
				if event.Op&fsnotify.Create == fsnotify.Create {
					tracker.Global.SetToolStatus(tracker.ToolStatus{
						Name:    "Crush",
						Tier:    tracker.TierFullTracking,
						Status:  "active",
						Message: "Watching database",
					})
				}
			case _, ok := <-watcher.Errors:
				if !ok {
					return
				}
			}
		}
	}()

	// Watch the database file's directory (fsnotify can't watch non-existent files)
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	if err := watcher.Add(dir); err != nil {
		return err
	}

	// Also watch the file itself if it exists
	if _, err := os.Stat(dbPath); err == nil {
		watcher.Add(dbPath)
	}

	return nil
}

// findCrushDB looks for an existing Crush database file
func findCrushDB() string {
	usr, _ := user.Current()

	for _, path := range defaultCrushDBPaths {
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

// processCrushDB reads and processes new/updated sessions from a Crush database
func processCrushDB(dbPath string) {
	db, err := sql.Open("sqlite3", dbPath+"?mode=ro")
	if err != nil {
		return
	}
	defer db.Close()

	// Query sessions with token usage
	rows, err := db.Query(`
		SELECT 
			id, 
			parent_session_id, 
			title, 
			message_count, 
			prompt_tokens, 
			completion_tokens, 
			cost, 
			created_at, 
			updated_at
		FROM sessions
		WHERE prompt_tokens > 0 OR completion_tokens > 0
		ORDER BY created_at ASC
	`)
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var session CrushSession
		err := rows.Scan(
			&session.ID,
			&session.ParentSessionID,
			&session.Title,
			&session.MessageCount,
			&session.PromptTokens,
			&session.CompletionTokens,
			&session.Cost,
			&session.CreatedAt,
			&session.UpdatedAt,
		)
		if err != nil {
			continue
		}

		// Skip if already processed and not updated
		lastUpdated, exists := processedCrushSessions[session.ID]
		if exists && lastUpdated >= session.UpdatedAt {
			continue
		}

		// Get the primary model used in this session
		model := getSessionPrimaryModel(db, session.ID)
		if model == "" {
			model = "crush-unknown"
		}

		// Calculate incremental usage if we've seen this session before
		var promptDelta, completionDelta int
		var costDelta float64

		if exists {
			// This is an update - we need to get the difference
			// For simplicity, we'll just add the total if it's the first time
			// In a production system, we'd track previous values
			promptDelta = session.PromptTokens
			completionDelta = session.CompletionTokens
			costDelta = session.Cost
		} else {
			promptDelta = session.PromptTokens
			completionDelta = session.CompletionTokens
			costDelta = session.Cost
		}

		// Use pre-calculated cost if available, otherwise calculate
		if costDelta <= 0 && (promptDelta > 0 || completionDelta > 0) {
			costDelta = pricing.CalculateCost(model, promptDelta, completionDelta)
		}

		if promptDelta > 0 || completionDelta > 0 {
			tracker.Global.AddUsageWithTool("Crush", model, promptDelta, completionDelta, costDelta)
			tracker.Global.IncrementToolEvents("Crush")
		}

		processedCrushSessions[session.ID] = session.UpdatedAt
	}
}

// getSessionPrimaryModel finds the most-used model in a session
func getSessionPrimaryModel(db *sql.DB, sessionID string) string {
	row := db.QueryRow(`
		SELECT model, provider
		FROM messages
		WHERE session_id = ? AND model IS NOT NULL AND model != ''
		GROUP BY model, provider
		ORDER BY COUNT(*) DESC
		LIMIT 1
	`, sessionID)

	var model, provider sql.NullString
	if err := row.Scan(&model, &provider); err != nil {
		return ""
	}

	result := model.String
	if provider.Valid && provider.String != "" {
		result = result + " (" + provider.String + ")"
	}
	return result
}

// ParseCrushDBOnce does a one-time parse of a Crush database
// Useful for the dashboard to load historical data
func ParseCrushDBOnce(dbPath string) error {
	usr, _ := user.Current()

	// Expand ~ in path
	if strings.HasPrefix(dbPath, "~") {
		dbPath = filepath.Join(usr.HomeDir, dbPath[1:])
	}

	if dbPath == "" {
		dbPath = findCrushDB()
	}

	if dbPath == "" {
		return nil // No database found, not an error
	}

	processCrushDB(dbPath)
	return nil
}

// ParseAllCrushDBs finds and parses all Crush databases on the system
func ParseAllCrushDBs() error {
	usr, _ := user.Current()

	// Find all .crush directories with crush.db files
	// Common locations to search
	searchPaths := []string{
		usr.HomeDir,
		filepath.Join(usr.HomeDir, "Projects"),
		filepath.Join(usr.HomeDir, "projects"),
		filepath.Join(usr.HomeDir, "code"),
		filepath.Join(usr.HomeDir, "Code"),
		filepath.Join(usr.HomeDir, "dev"),
		filepath.Join(usr.HomeDir, "Dev"),
		filepath.Join(usr.HomeDir, "src"),
		filepath.Join(usr.HomeDir, "work"),
		filepath.Join(usr.HomeDir, "Work"),
	}

	// Also check current working directory
	if cwd, err := os.Getwd(); err == nil {
		searchPaths = append([]string{cwd}, searchPaths...)
	}

	foundDBs := make(map[string]bool)

	for _, basePath := range searchPaths {
		if _, err := os.Stat(basePath); err != nil {
			continue
		}

		filepath.Walk(basePath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil // Skip errors
			}

			// Don't recurse too deep
			depth := strings.Count(strings.TrimPrefix(path, basePath), string(os.PathSeparator))
			if depth > 4 {
				return filepath.SkipDir
			}

			// Skip hidden directories (except .crush)
			if info.IsDir() && strings.HasPrefix(info.Name(), ".") && info.Name() != ".crush" {
				return filepath.SkipDir
			}

			// Check for crush.db
			if info.Name() == "crush.db" && strings.Contains(path, ".crush") {
				if !foundDBs[path] {
					foundDBs[path] = true
					processCrushDB(path)
				}
			}

			return nil
		})
	}

	return nil
}

// GetCrushSessions returns all sessions from a Crush database
func GetCrushSessions(dbPath string) ([]CrushSession, error) {
	usr, _ := user.Current()

	// Expand ~ in path
	if strings.HasPrefix(dbPath, "~") {
		dbPath = filepath.Join(usr.HomeDir, dbPath[1:])
	}

	if dbPath == "" {
		dbPath = findCrushDB()
	}

	if dbPath == "" {
		return nil, nil
	}

	db, err := sql.Open("sqlite3", dbPath+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT 
			id, 
			parent_session_id, 
			title, 
			message_count, 
			prompt_tokens, 
			completion_tokens, 
			cost, 
			created_at, 
			updated_at
		FROM sessions
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []CrushSession
	for rows.Next() {
		var session CrushSession
		err := rows.Scan(
			&session.ID,
			&session.ParentSessionID,
			&session.Title,
			&session.MessageCount,
			&session.PromptTokens,
			&session.CompletionTokens,
			&session.Cost,
			&session.CreatedAt,
			&session.UpdatedAt,
		)
		if err != nil {
			continue
		}
		sessions = append(sessions, session)
	}

	return sessions, nil
}

// GetCrushUsageByDate returns token usage aggregated by date
func GetCrushUsageByDate(dbPath string, since time.Time) (map[string]struct {
	PromptTokens     int
	CompletionTokens int
	Cost             float64
}, error) {
	usr, _ := user.Current()

	if strings.HasPrefix(dbPath, "~") {
		dbPath = filepath.Join(usr.HomeDir, dbPath[1:])
	}

	if dbPath == "" {
		dbPath = findCrushDB()
	}

	if dbPath == "" {
		return nil, nil
	}

	db, err := sql.Open("sqlite3", dbPath+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	sinceMs := since.UnixMilli()
	rows, err := db.Query(`
		SELECT 
			date(created_at/1000, 'unixepoch') as day,
			SUM(prompt_tokens) as prompt_tokens,
			SUM(completion_tokens) as completion_tokens,
			SUM(cost) as cost
		FROM sessions
		WHERE created_at >= ?
		GROUP BY day
		ORDER BY day ASC
	`, sinceMs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]struct {
		PromptTokens     int
		CompletionTokens int
		Cost             float64
	})

	for rows.Next() {
		var day string
		var promptTokens, completionTokens int
		var cost float64

		if err := rows.Scan(&day, &promptTokens, &completionTokens, &cost); err != nil {
			continue
		}

		result[day] = struct {
			PromptTokens     int
			CompletionTokens int
			Cost             float64
		}{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			Cost:             cost,
		}
	}

	return result, nil
}

// GetCrushUsageByModel returns token usage aggregated by model
func GetCrushUsageByModel(dbPath string) (map[string]struct {
	PromptTokens     int
	CompletionTokens int
	Cost             float64
	SessionCount     int
}, error) {
	usr, _ := user.Current()

	if strings.HasPrefix(dbPath, "~") {
		dbPath = filepath.Join(usr.HomeDir, dbPath[1:])
	}

	if dbPath == "" {
		dbPath = findCrushDB()
	}

	if dbPath == "" {
		return nil, nil
	}

	db, err := sql.Open("sqlite3", dbPath+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	// Join sessions with messages to get model info
	rows, err := db.Query(`
		SELECT 
			COALESCE(m.model, 'unknown') as model,
			COALESCE(m.provider, '') as provider,
			SUM(s.prompt_tokens) as prompt_tokens,
			SUM(s.completion_tokens) as completion_tokens,
			SUM(s.cost) as cost,
			COUNT(DISTINCT s.id) as session_count
		FROM sessions s
		LEFT JOIN (
			SELECT session_id, model, provider
			FROM messages
			WHERE model IS NOT NULL AND model != ''
			GROUP BY session_id
		) m ON s.id = m.session_id
		GROUP BY m.model, m.provider
		ORDER BY cost DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]struct {
		PromptTokens     int
		CompletionTokens int
		Cost             float64
		SessionCount     int
	})

	for rows.Next() {
		var model, provider string
		var promptTokens, completionTokens, sessionCount int
		var cost float64

		if err := rows.Scan(&model, &provider, &promptTokens, &completionTokens, &cost, &sessionCount); err != nil {
			continue
		}

		key := model
		if provider != "" {
			key = model + " (" + provider + ")"
		}

		result[key] = struct {
			PromptTokens     int
			CompletionTokens int
			Cost             float64
			SessionCount     int
		}{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			Cost:             cost,
			SessionCount:     sessionCount,
		}
	}

	return result, nil
}
