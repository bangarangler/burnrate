package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

var DB *sql.DB

// InitDB initializes the SQLite database for historical tracking
func InitDB() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	dbDir := filepath.Join(home, ".burnrate")
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		return fmt.Errorf("failed to create db directory: %w", err)
	}

	dbPath := filepath.Join(dbDir, "history.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	DB = db
	return createTables()
}

func createTables() error {
	query := `
	CREATE TABLE IF NOT EXISTS usage_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp INTEGER NOT NULL,
		tool TEXT NOT NULL,
		model TEXT NOT NULL,
		prompt_tokens INTEGER DEFAULT 0,
		completion_tokens INTEGER DEFAULT 0,
		cost REAL DEFAULT 0.0
	);
	CREATE INDEX IF NOT EXISTS idx_timestamp ON usage_events(timestamp);
	`
	_, err := DB.Exec(query)
	if err != nil {
		return fmt.Errorf("failed to create tables: %w", err)
	}
	return nil
}

// RecordUsage writes a single usage event to the database
func RecordUsage(tool, model string, prompt, completion int, cost float64) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}

	query := `
	INSERT INTO usage_events (timestamp, tool, model, prompt_tokens, completion_tokens, cost)
	VALUES (?, ?, ?, ?, ?, ?)
	`
	_, err := DB.Exec(query, time.Now().Unix(), tool, model, prompt, completion, cost)
	return err
}

// GetDailyUsage returns aggregated usage for the last N days
func GetDailyUsage(days int) (map[string]float64, error) {
	if DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	cutoff := time.Now().AddDate(0, 0, -days).Unix()
	query := `
	SELECT date(timestamp, 'unixepoch', 'localtime') as day, SUM(cost) 
	FROM usage_events 
	WHERE timestamp >= ? 
	GROUP BY day 
	ORDER BY day DESC
	`

	rows, err := DB.Query(query, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	dailyCosts := make(map[string]float64)
	for rows.Next() {
		var day string
		var cost float64
		if err := rows.Scan(&day, &cost); err != nil {
			return nil, err
		}
		dailyCosts[day] = cost
	}
	return dailyCosts, nil
}
