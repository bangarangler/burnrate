package config

import (
	"os"
	"strconv"
)

type Config struct {
	DailyBudget float64
}

// Load loads the configuration from environment variables or defaults
func Load() *Config {
	cfg := &Config{
		DailyBudget: 5.0, // Default $5.00/day
	}

	if val := os.Getenv("BURNRATE_DAILY_BUDGET"); val != "" {
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			cfg.DailyBudget = f
		}
	}

	return cfg
}
