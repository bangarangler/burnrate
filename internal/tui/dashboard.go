// internal/tui/dashboard.go
package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/bangarangler/burnrate/internal/config"
	"github.com/bangarangler/burnrate/internal/pricing"
	"github.com/bangarangler/burnrate/internal/tracker"
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// KeyMap defines the keybindings for the dashboard
type KeyMap struct {
	SessionView key.Binding
	TodayView   key.Binding
	WeekView    key.Binding
	Reset       key.Binding
	Quit        key.Binding
}

// DefaultKeyMap returns the default keybindings
func DefaultKeyMap() KeyMap {
	return KeyMap{
		SessionView: key.NewBinding(
			key.WithKeys("s"),
			key.WithHelp("s", "session"),
		),
		TodayView: key.NewBinding(
			key.WithKeys("t"),
			key.WithHelp("t", "today"),
		),
		WeekView: key.NewBinding(
			key.WithKeys("w"),
			key.WithHelp("w", "week"),
		),
		Reset: key.NewBinding(
			key.WithKeys("r"),
			key.WithHelp("r", "reset"),
		),
		Quit: key.NewBinding(
			key.WithKeys("q", "ctrl+c"),
			key.WithHelp("q", "quit"),
		),
	}
}

// ShortHelp returns keybindings to be shown in the mini help view
func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.SessionView, k.TodayView, k.WeekView, k.Reset, k.Quit}
}

// FullHelp returns keybindings for the expanded help view
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.SessionView, k.TodayView, k.WeekView},
		{k.Reset, k.Quit},
	}
}

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
	activeTabColor = lipgloss.Color("205") // Active tab
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

	statusDotStyle = lipgloss.NewStyle().
			Foreground(successColor).
			SetString("●")

	statusDotStaleStyle = lipgloss.NewStyle().
				Foreground(mutedColor).
				SetString("○")

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

	chartBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(borderColor).
			Padding(0, 1)

	footerStyle = lipgloss.NewStyle().
			Foreground(mutedColor).
			Padding(1, 0)

	tabStyle = lipgloss.NewStyle().
			Foreground(mutedColor).
			Padding(0, 1)

	activeTabStyle = lipgloss.NewStyle().
			Foreground(activeTabColor).
			Bold(true).
			Border(lipgloss.NormalBorder(), false, false, true, false).
			BorderForeground(activeTabColor).
			Padding(0, 1)
)

type model struct {
	table       table.Model
	progress    progress.Model
	help        help.Model
	keys        KeyMap
	total       float64
	burnRate    float64
	startTime   time.Time
	activeView  string // "session", "today", "week"
	config      *config.Config
	pricingTime time.Time
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

	prog := progress.New(progress.WithDefaultGradient())
	prog.Width = 30

	return model{
		table:      t,
		progress:   prog,
		help:       help.New(),
		keys:       DefaultKeyMap(),
		startTime:  time.Now(),
		activeView: "session",
		config:     config.Load(),
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
		m.pricingTime = pricing.GetLastFetchTime()

		var usages []tracker.Usage
		var err error

		switch m.activeView {
		case "session":
			m.total = tracker.Global.GetSessionCost()
			usages = tracker.Global.GetUsages()
			// Burn rate only relevant for session view
			m.burnRate = tracker.Global.GetBurnRatePerHour()

		case "today", "week":
			usages, m.total, err = tracker.Global.GetHistoricalUsage(m.activeView)
			if err != nil {
				// Fallback or error handling
			}
			m.burnRate = 0 // Not applicable for historical views
		}

		rows := []table.Row{}
		for _, u := range usages {
			rows = append(rows, table.Row{
				u.Model,
				formatTokens(u.PromptTokens),
				formatTokens(u.CompletionTokens),
				fmt.Sprintf("$%.4f", u.Cost),
			})
		}
		m.table.SetRows(rows)

		return m, tickCmd()

	case tea.WindowSizeMsg:
		m.help.Width = msg.Width

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "r":
			tracker.Global.Reset()
			m.startTime = time.Now()
			return m, nil
		case "s":
			m.activeView = "session"
		case "t":
			m.activeView = "today"
		case "w":
			m.activeView = "week"
		case "?":
			m.help.ShowAll = !m.help.ShowAll
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m model) View() string {
	// Header with Pricing Status
	pricingStatus := statusDotStaleStyle.Render()
	if time.Since(m.pricingTime) < 1*time.Hour && !m.pricingTime.IsZero() {
		pricingStatus = statusDotStyle.Render()
	}

	header := lipgloss.JoinHorizontal(lipgloss.Center,
		titleStyle.Render("burnrate"),
		subtitleStyle.Render(" Real-time AI Spend Monitor  "),
		pricingStatus,
	)

	// Tabs
	tabs := lipgloss.JoinHorizontal(lipgloss.Bottom,
		m.renderTab("Session", "session"),
		m.renderTab("Today", "today"),
		m.renderTab("Week", "week"),
	)

	// Session stats row (Context sensitive)
	var stats string
	if m.activeView == "session" {
		duration := time.Since(m.startTime)
		durationStr := formatDuration(duration)

		stats = statsBoxStyle.Render(
			lipgloss.JoinHorizontal(lipgloss.Center,
				statLabelStyle.Render("Total ")+statValueStyle.Render(fmt.Sprintf("$%.4f", m.total)),
				"    ",
				statLabelStyle.Render("Burn ")+statValueStyle.Render(fmt.Sprintf("$%.2f/hr", m.burnRate)),
				"    ",
				statLabelStyle.Render("Duration ")+statValueStyle.Render(durationStr),
			),
		)
	} else {
		// Budget Bar for Today/Week
		pct := m.total / m.config.DailyBudget
		if m.activeView == "week" {
			pct = m.total / (m.config.DailyBudget * 7)
		}
		if pct > 1.0 {
			pct = 1.0
		}

		prog := m.progress.ViewAs(pct)
		limit := fmt.Sprintf("/$%.2f", m.config.DailyBudget)
		if m.activeView == "week" {
			limit = fmt.Sprintf("/$%.2f", m.config.DailyBudget*7)
		}

		stats = statsBoxStyle.Render(
			lipgloss.JoinVertical(lipgloss.Center,
				lipgloss.JoinHorizontal(lipgloss.Center,
					statLabelStyle.Render("Spend ")+statValueStyle.Render(fmt.Sprintf("$%.4f", m.total)),
					statLabelStyle.Render(limit),
				),
				prog,
			),
		)
	}

	// Historical Spend Chart (Today/Week only)
	var chart string
	if m.activeView != "session" {
		chart = m.renderHistoryChart()
	}

	// Tools panel (Always visible)
	toolsPanel := m.renderToolsPanel()

	// Usage table
	usageTable := tableBoxStyle.Render(m.table.View())

	// Footer
	footer := footerStyle.Render(m.help.View(m.keys))

	// Layout depends on view
	var mainContent string
	if m.activeView == "session" {
		mainContent = lipgloss.JoinVertical(lipgloss.Left,
			stats,
			"",
			toolsPanel,
			"",
			usageTable,
		)
	} else {
		mainContent = lipgloss.JoinVertical(lipgloss.Left,
			lipgloss.JoinHorizontal(lipgloss.Top,
				stats,
				"  ",
				chart,
			),
			"",
			toolsPanel,
			"",
			usageTable,
		)
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		"",
		header,
		tabs,
		"",
		mainContent,
		footer,
	)
}

func (m model) renderHistoryChart() string {
	days := 7
	if m.activeView == "week" {
		days = 7
	} else if m.activeView == "today" {
		days = 7 // Show context for today as well
	}

	dailySpends, err := tracker.Global.GetDailySpend(days)
	if err != nil || len(dailySpends) == 0 {
		return chartBoxStyle.Render("No history available")
	}

	// Find max for scaling
	var maxCost float64
	for _, ds := range dailySpends {
		if ds.Cost > maxCost {
			maxCost = ds.Cost
		}
	}
	if maxCost == 0 {
		maxCost = 1.0 // Avoid div by zero
	}

	// Simple ASCII Chart
	var bars []string
	bars = append(bars, lipgloss.NewStyle().Bold(true).Render("History (Last 7 Days)"))

	for _, ds := range dailySpends {
		// Parse date to just show Day/Month or short Day
		t, _ := time.Parse("2006-01-02", ds.Date)
		label := t.Format("Mon")

		barLen := int((ds.Cost / maxCost) * 20)
		if barLen == 0 && ds.Cost > 0 {
			barLen = 1
		}

		barChar := "▇"
		bar := strings.Repeat(barChar, barLen)

		// Color based on budget ratio (rough approx)
		color := successColor
		if ds.Cost > m.config.DailyBudget {
			color = errorColor
		} else if ds.Cost > m.config.DailyBudget*0.8 {
			color = warningColor
		}

		line := fmt.Sprintf("%s %s $%.2f",
			lipgloss.NewStyle().Width(3).Render(label),
			lipgloss.NewStyle().Foreground(color).Render(bar),
			ds.Cost,
		)
		bars = append(bars, line)
	}

	return chartBoxStyle.Render(strings.Join(bars, "\n"))
}

func (m model) renderTab(label, key string) string {
	if m.activeView == key {
		return activeTabStyle.Render(label)
	}
	return tabStyle.Render(label)
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
		// Show cost if available
		if s.TotalCost > 0 {
			eventInfo += fmt.Sprintf(" ($%.4f)", s.TotalCost)
		}
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
