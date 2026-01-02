/*
Copyright 2025 burnrate authors
*/
package cmd

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/bangarangler/burnrate/internal/parser"
	"github.com/bangarangler/burnrate/internal/pricing"
	"github.com/bangarangler/burnrate/internal/tracker"
	"github.com/bangarangler/burnrate/internal/tui"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
)

var aiderLogPath string
var crushDBPath string

// dashboardCmd represents the dashboard command
var dashboardCmd = &cobra.Command{
	Use:   "dashboard",
	Short: "Launch the live cost dashboard",
	Long:  `Opens a terminal dashboard showing your current AI spend, burn rate, and tool status.`,
	Run: func(cmd *cobra.Command, args []string) {
		// Initialize pricing (async fetch)
		go func() {
			// This will update the pricing map in the background
			// If it fails, we just continue with hardcoded defaults
			_ = pricing.UpdatePricing()
		}()

		// Initialize tool watchers - they now report their own status to tracker

		// OpenCode (Tier 1 - Full Tracking)
		parser.StartOpenCodeWatcher()

		// Aider (Tier 1 - Full Tracking)
		parser.StartAiderWatcher(aiderLogPath)

		// Codex (Tier 1 - Full Tracking, partial without OTEL)
		parser.StartCodexWatcher()

		// Crush (Tier 1 - Full Tracking)
		parser.StartCrushWatcher(crushDBPath)

		// Copilot (Tier 2 - Detection Only)
		copilotStatus := parser.CheckCopilotStatus()
		tracker.Global.SetToolStatus(tracker.ToolStatus{
			Name:         "Copilot",
			Tier:         tracker.TierDetectionOnly,
			Status:       copilotStatus.StatusCode(),
			Message:      copilotStatus.StatusMessage(),
			DashboardURL: copilotStatus.DashboardURL,
		})

		// Launch TUI
		p := tea.NewProgram(tui.InitialModel(), tea.WithAltScreen())

		// Handle graceful shutdown
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sig
			p.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
		}()

		p.Run()
	},
}

func init() {
	rootCmd.AddCommand(dashboardCmd)

	// Aider analytics log path flag
	dashboardCmd.Flags().StringVar(&aiderLogPath, "aider-log", "",
		"Path to Aider analytics JSONL log file (default: ~/.aider/usage.jsonl)")

	// Crush database path flag
	dashboardCmd.Flags().StringVar(&crushDBPath, "crush-db", "",
		"Path to Crush SQLite database (default: .crush/crush.db)")
}
