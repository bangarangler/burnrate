// internal/parser/copilot.go
package parser

import (
	"bytes"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
)

// CopilotStatus represents the detection status of GitHub Copilot CLI
type CopilotStatus struct {
	NewCLIInstalled bool   // Standalone 'copilot' CLI found
	GHExtInstalled  bool   // 'gh copilot' extension found
	Configured      bool   // OAuth tokens/config present
	CLIPath         string // Path to CLI if found
	ConfigPath      string // Path to config directory if found
	DashboardURL    string // URL to view usage (always set)
}

// CheckCopilotStatus performs a one-time detection of GitHub Copilot CLI
// This is a Tier 2 tool - we can only detect installation, not parse usage logs
func CheckCopilotStatus() CopilotStatus {
	status := CopilotStatus{
		DashboardURL: "https://github.com/settings/copilot",
	}

	// Check for standalone 'copilot' CLI (new CLI released 2025)
	if path, err := exec.LookPath("copilot"); err == nil {
		status.NewCLIInstalled = true
		status.CLIPath = path
	}

	// Check for 'gh copilot' extension
	status.GHExtInstalled = checkGHCopilotExtension()

	// Check for OAuth configuration
	status.Configured, status.ConfigPath = checkCopilotConfig()

	return status
}

// checkGHCopilotExtension checks if the gh copilot extension is installed
func checkGHCopilotExtension() bool {
	// First check if gh is installed
	if _, err := exec.LookPath("gh"); err != nil {
		return false
	}

	// Run gh extension list and look for copilot
	cmd := exec.Command("gh", "extension", "list")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = nil

	if err := cmd.Run(); err != nil {
		return false
	}

	return strings.Contains(out.String(), "copilot")
}

// checkCopilotConfig checks for Copilot configuration files
func checkCopilotConfig() (configured bool, configPath string) {
	usr, err := user.Current()
	if err != nil {
		return false, ""
	}

	// Check various config locations
	configPaths := []struct {
		dir  string
		file string
	}{
		// New standalone CLI config
		{filepath.Join(usr.HomeDir, ".copilot"), "config.json"},
		// Old gh copilot extension config
		{filepath.Join(usr.HomeDir, ".config", "github-copilot"), "apps.json"},
		// Alternative location
		{filepath.Join(usr.HomeDir, ".config", "github-copilot"), "hosts.json"},
	}

	for _, cfg := range configPaths {
		fullPath := filepath.Join(cfg.dir, cfg.file)
		if _, err := os.Stat(fullPath); err == nil {
			// File exists - check if it has content (not just empty)
			info, _ := os.Stat(fullPath)
			if info.Size() > 2 { // More than just "{}" or "[]"
				return true, cfg.dir
			}
		}
	}

	return false, ""
}

// IsInstalled returns true if any Copilot CLI is installed
func (s CopilotStatus) IsInstalled() bool {
	return s.NewCLIInstalled || s.GHExtInstalled
}

// StatusMessage returns a human-readable status message
func (s CopilotStatus) StatusMessage() string {
	if s.Configured {
		return "View usage on GitHub"
	}
	if s.IsInstalled() {
		return "CLI found but not configured"
	}
	return "CLI not installed"
}

// StatusCode returns the status code for display
func (s CopilotStatus) StatusCode() string {
	if s.Configured {
		return "configured"
	}
	if s.IsInstalled() {
		return "installed"
	}
	return "not_found"
}
