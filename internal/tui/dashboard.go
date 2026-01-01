// internal/tui/dashboard.go
package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/bangarangler/burnrate/internal/tracker"
	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Color palette
var (
	primaryColor   = lipgloss.Color("205") // Pink/magenta for branding
	successColor   = lipgloss.Color("42")  // Green for active
	warningColor   = lipgloss.Color("214") // Orange for partial
	infoColor      = lipgloss.Color("39")  // Blue for configured
	errorColor     = lipgloss.Color("196") // Red for not found
	mutedColor     = lipgloss.Color("240") // Gray for muted text
	borderColor    = lipgloss.Color("62")  // Purple for borders
	highlightColor = lipgloss.Color("229") // Yellow for highlights
)

// Styles
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(primaryColor).
			Padding(0, 1)

	subtitleStyle = lipgloss.NewStyle().
			Foreground(mutedColor).
			Italic(true)

	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(borderColor).
			Padding(0, 1)

	statsBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(borderColor).
			Padding(0, 2)

	statLabelStyle = lipgloss.NewStyle().
			Foreground(mutedColor)

	statValueStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(highlightColor)

	toolsBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(borderColor).
			Padding(0, 1)

	tableBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(borderColor)

	footerStyle = lipgloss.NewStyle().
			Foreground(mutedColor).
			Padding(1, 0)
)

type model struct {
	table     table.Model
	total     float64
	burnRate  float64
	startTime time.Time
}

func InitialModel() model {
	columns := []table.Column{
		{Title: "Model", Width: 35},
		{Title: "Input", Width: 10},
		{Title: "Output", Width: 10},
		{Title: "Cost", Width: 10},
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows([]table.Row{}),
		table.WithFocused(true),
		table.WithHeight(8),
	)

	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(mutedColor).
		BorderBottom(true).
		Bold(true).
		Foreground(primaryColor)
	s.Selected = s.Selected.
		Foreground(lipgloss.Color("229")).
		Background(lipgloss.Color("57")).
		Bold(false)
	t.SetStyles(s)

	return model{
		table:     t,
		startTime: time.Now(),
	}
}

func (m model) Init() tea.Cmd {
	return tickCmd()
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

type tickMsg time.Time

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tickMsg:
		m.total = tracker.Global.GetSessionCost()
		m.burnRate = tracker.Global.GetBurnRatePerHour()

		// Use safe getter for usages
		rows := []table.Row{}
		for _, u := range tracker.Global.GetUsages() {
			rows = append(rows, table.Row{
				u.Model,
				formatTokens(u.PromptTokens),
				formatTokens(u.CompletionTokens),
				fmt.Sprintf("$%.4f", u.Cost),
			})
		}
		m.table.SetRows(rows)

		return m, tickCmd()

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "r":
			tracker.Global.Reset()
			m.startTime = time.Now()
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m model) View() string {
	// Header
	header := lipgloss.JoinHorizontal(lipgloss.Center,
		titleStyle.Render("burnrate"),
		subtitleStyle.Render(" Real-time AI Spend Monitor"),
	)

	// Session stats row
	duration := time.Since(m.startTime)
	durationStr := formatDuration(duration)

	stats := statsBoxStyle.Render(
		lipgloss.JoinHorizontal(lipgloss.Center,
			statLabelStyle.Render("Total ")+statValueStyle.Render(fmt.Sprintf("$%.4f", m.total)),
			"    ",
			statLabelStyle.Render("Burn ")+statValueStyle.Render(fmt.Sprintf("$%.2f/hr", m.burnRate)),
			"    ",
			statLabelStyle.Render("Duration ")+statValueStyle.Render(durationStr),
		),
	)

	// Tools panel
	toolsPanel := m.renderToolsPanel()

	// Usage table
	usageTable := tableBoxStyle.Render(m.table.View())

	// Footer
	footer := footerStyle.Render("[q] quit  [r] reset")

	return lipgloss.JoinVertical(lipgloss.Left,
		"",
		header,
		"",
		stats,
		"",
		toolsPanel,
		"",
		usageTable,
		footer,
	)
}

func (m model) renderToolsPanel() string {
	statuses := tracker.Global.GetToolStatuses()

	if len(statuses) == 0 {
		return toolsBoxStyle.Render(
			lipgloss.NewStyle().Foreground(mutedColor).Render("No tools detected"),
		)
	}

	var lines []string
	for _, s := range statuses {
		line := formatToolStatus(s)
		lines = append(lines, line)
	}

	content := strings.Join(lines, "\n")
	return toolsBoxStyle.Render(content)
}

func formatToolStatus(s *tracker.ToolStatus) string {
	// Status icon and color
	var icon string
	var statusStyle lipgloss.Style

	switch s.Status {
	case "active":
		icon = lipgloss.NewStyle().Foreground(successColor).Render("*")
		statusStyle = lipgloss.NewStyle().Foreground(successColor)
	case "partial":
		icon = lipgloss.NewStyle().Foreground(warningColor).Render("!")
		statusStyle = lipgloss.NewStyle().Foreground(warningColor)
	case "configured":
		icon = lipgloss.NewStyle().Foreground(infoColor).Render("o")
		statusStyle = lipgloss.NewStyle().Foreground(infoColor)
	case "waiting":
		icon = lipgloss.NewStyle().Foreground(mutedColor).Render("~")
		statusStyle = lipgloss.NewStyle().Foreground(mutedColor)
	default: // not_found, error
		icon = lipgloss.NewStyle().Foreground(errorColor).Render("x")
		statusStyle = lipgloss.NewStyle().Foreground(errorColor)
	}

	// Tool name (fixed width)
	name := lipgloss.NewStyle().Width(12).Render(s.Name)

	// Status text
	statusText := statusStyle.Width(12).Render(s.Status)

	// Event count and last time (for Tier 1 tools with events)
	var eventInfo string
	if s.Tier == tracker.TierFullTracking && s.EventCount > 0 {
		eventInfo = fmt.Sprintf("%d events", s.EventCount)
		if !s.LastEventTime.IsZero() {
			eventInfo += "  " + formatRelativeTime(s.LastEventTime)
		}
	} else if s.Tier == tracker.TierDetectionOnly && s.DashboardURL != "" {
		// Show shortened dashboard URL for detection-only tools
		shortURL := strings.TrimPrefix(s.DashboardURL, "https://")
		eventInfo = lipgloss.NewStyle().Foreground(infoColor).Render("-> " + shortURL)
	} else if s.Message != "" {
		eventInfo = lipgloss.NewStyle().Foreground(mutedColor).Render(s.Message)
	}

	return fmt.Sprintf(" %s %s %s  %s", icon, name, statusText, eventInfo)
}

func formatTokens(tokens int) string {
	if tokens >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(tokens)/1000000)
	}
	if tokens >= 1000 {
		return fmt.Sprintf("%.1fK", float64(tokens)/1000)
	}
	return fmt.Sprintf("%d", tokens)
}

func formatDuration(d time.Duration) string {
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60

	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}

func formatRelativeTime(t time.Time) string {
	d := time.Since(t)

	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		mins := int(d.Minutes())
		return fmt.Sprintf("%dm ago", mins)
	}
	if d < 24*time.Hour {
		hours := int(d.Hours())
		return fmt.Sprintf("%dh ago", hours)
	}
	days := int(d.Hours() / 24)
	return fmt.Sprintf("%dd ago", days)
}
