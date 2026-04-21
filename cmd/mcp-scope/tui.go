package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/SSanju/mcp-scope/internal/capture"
)

// ── Styles ───────────────────────────────────────────────────────────────────

var (
	tuiHeader   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86"))
	tuiSelected = lipgloss.NewStyle().Background(lipgloss.Color("237"))
	tuiC2S      = lipgloss.NewStyle().Foreground(lipgloss.Color("33"))
	tuiS2C      = lipgloss.NewStyle().Foreground(lipgloss.Color("35"))
	tuiEvent    = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	tuiErr      = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	tuiDiv      = lipgloss.NewStyle().Foreground(lipgloss.Color("237"))
	tuiHelp     = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	tuiFilter   = lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true)
)

// ── Layout constants ─────────────────────────────────────────────────────────

const tuiFixed = 4 // header + 2 dividers + bar

// ── Model ────────────────────────────────────────────────────────────────────

type tuiModel struct {
	file     string
	all      []capture.Record
	filtered []capture.Record

	cursor  int // index into filtered
	listTop int // first visible index

	detail      viewport.Model
	filterInput textinput.Model
	filterMode  bool

	width  int
	height int
	ready  bool
}

func newTUIModel(records []capture.Record, file string) tuiModel {
	ti := textinput.New()
	ti.Placeholder = "regex…"
	ti.CharLimit = 128

	return tuiModel{
		file:        file,
		all:         records,
		filtered:    records,
		filterInput: ti,
	}
}

func (m tuiModel) Init() tea.Cmd { return nil }

// ── Update ───────────────────────────────────────────────────────────────────

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		dh := m.detailHeight()
		if !m.ready {
			m.detail = viewport.New(m.width, dh)
		} else {
			m.detail.Width = m.width
			m.detail.Height = dh
		}
		m.detail.SetContent(m.detailContent())
		m.ready = true
		return m, nil

	case tea.KeyMsg:
		if !m.ready {
			return m, nil
		}
		if m.filterMode {
			return m.updateFilter(msg)
		}
		return m.updateNav(msg)
	}
	return m, nil
}

func (m tuiModel) updateNav(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	lh := m.listHeight()
	n := len(m.filtered)

	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "j", "down":
		if m.cursor < n-1 {
			m.cursor++
			m.scroll(lh)
			m.refreshDetail()
		}
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
			m.scroll(lh)
			m.refreshDetail()
		}
	case "g":
		m.cursor = 0
		m.listTop = 0
		m.refreshDetail()
	case "G":
		m.cursor = max(0, n-1)
		m.scroll(lh)
		m.refreshDetail()
	case "ctrl+d", "pgdown":
		m.cursor = min(n-1, m.cursor+lh/2)
		m.scroll(lh)
		m.refreshDetail()
	case "ctrl+u", "pgup":
		m.cursor = max(0, m.cursor-lh/2)
		m.scroll(lh)
		m.refreshDetail()
	case "J":
		m.detail.ScrollDown(3)
	case "K":
		m.detail.ScrollUp(3)
	case "/":
		m.filterMode = true
		m.filterInput.Focus()
		return m, textinput.Blink
	case "esc":
		if m.filterInput.Value() != "" {
			m.filterInput.SetValue("")
			m.applyFilter()
		}
	}
	return m, nil
}

func (m tuiModel) updateFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.filterMode = false
		m.filterInput.Blur()
		m.filterInput.SetValue("")
		m.applyFilter()
		return m, nil
	case "enter":
		m.filterMode = false
		m.filterInput.Blur()
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	}
	var cmd tea.Cmd
	m.filterInput, cmd = m.filterInput.Update(msg)
	m.applyFilter()
	return m, cmd
}

func (m *tuiModel) applyFilter() {
	pat := m.filterInput.Value()
	if pat == "" {
		m.filtered = m.all
	} else {
		re, err := regexp.Compile("(?i)" + pat)
		if err != nil {
			re = regexp.MustCompile("(?i)" + regexp.QuoteMeta(pat))
		}
		out := make([]capture.Record, 0, len(m.all))
		for _, rec := range m.all {
			if tuiMatchRecord(re, rec) {
				out = append(out, rec)
			}
		}
		m.filtered = out
	}
	m.cursor = 0
	m.listTop = 0
	m.refreshDetail()
}

func tuiMatchRecord(re *regexp.Regexp, rec capture.Record) bool {
	if rec.IsEvent() {
		return re.MatchString(rec.Event)
	}
	_, id, method := classifyFrame(rec.Payload)
	summary := method + " " + id + " " + string(rec.Transport) + " " + string(rec.Dir)
	return re.MatchString(summary) || re.Match(rec.Payload)
}

func (m *tuiModel) refreshDetail() {
	m.detail.SetContent(m.detailContent())
	m.detail.GotoTop()
}

func (m *tuiModel) scroll(lh int) {
	if m.cursor < m.listTop {
		m.listTop = m.cursor
	} else if m.cursor >= m.listTop+lh {
		m.listTop = m.cursor - lh + 1
	}
}

// ── View ─────────────────────────────────────────────────────────────────────

func (m tuiModel) View() string {
	if !m.ready {
		return "\n  Loading…"
	}
	lh := m.listHeight()

	parts := []string{
		tuiHeader.Render(fmt.Sprintf(" mcp-scope — %s  (%d/%d frames)",
			m.file, len(m.filtered), len(m.all))),
		tuiDiv.Render(strings.Repeat("─", m.width)),
		m.renderList(lh),
		tuiDiv.Render(strings.Repeat("─", m.width)),
		m.detail.View(),
		m.renderBar(),
	}
	return strings.Join(parts, "\n")
}

func (m tuiModel) renderList(h int) string {
	lines := make([]string, h)
	for i := range lines {
		idx := m.listTop + i
		if idx >= len(m.filtered) {
			lines[i] = ""
			continue
		}
		rec := m.filtered[idx]
		text := tuiFormatLine(rec)
		if idx == m.cursor {
			lines[i] = tuiSelected.Width(m.width).Render(text)
		} else {
			lines[i] = tuiColorLine(rec, text)
		}
	}
	return strings.Join(lines, "\n")
}

func tuiFormatLine(rec capture.Record) string {
	ts := rec.TS.Format("15:04:05.000")
	if rec.IsEvent() {
		s := fmt.Sprintf("• %s  %-16s", ts, rec.Event)
		if len(rec.Meta) > 0 {
			s += "  " + formatMeta(rec.Meta)
		}
		return s
	}
	arrow := "→"
	if rec.Dir == capture.DirS2C {
		arrow = "←"
	}
	kind, id, method := classifyFrame(rec.Payload)
	summary := kind
	if method != "" {
		summary += " " + method
	}
	if id != "" {
		summary += " id=" + id
	}
	return fmt.Sprintf("%s %s  %-4s  %s", arrow, ts, string(rec.Transport), summary)
}

func tuiColorLine(rec capture.Record, text string) string {
	if rec.IsEvent() {
		return tuiEvent.Render(text)
	}
	kind, _, _ := classifyFrame(rec.Payload)
	if kind == "err" {
		return tuiErr.Render(text)
	}
	if rec.Dir == capture.DirS2C {
		return tuiS2C.Render(text)
	}
	return tuiC2S.Render(text)
}

func (m tuiModel) detailContent() string {
	if len(m.filtered) == 0 || m.cursor >= len(m.filtered) {
		return " (nothing selected)"
	}
	rec := m.filtered[m.cursor]
	var sb strings.Builder

	if rec.IsEvent() {
		sb.WriteString(fmt.Sprintf("Event:     %s\n", rec.Event))
		if !rec.TS.IsZero() {
			sb.WriteString(fmt.Sprintf("Timestamp: %s\n",
				rec.TS.Format("2006-01-02T15:04:05.000Z07:00")))
		}
		if len(rec.Meta) > 0 {
			sb.WriteString("Meta:\n")
			for k, v := range rec.Meta {
				sb.WriteString(fmt.Sprintf("  %s: %s\n", k, v))
			}
		}
		return sb.String()
	}

	arrow := "→  client→server"
	if rec.Dir == capture.DirS2C {
		arrow = "←  server→client"
	}
	kind, id, method := classifyFrame(rec.Payload)
	sb.WriteString(fmt.Sprintf("Direction: %s\n", arrow))
	sb.WriteString(fmt.Sprintf("Transport: %s\n", rec.Transport))
	sb.WriteString(fmt.Sprintf("Timestamp: %s\n",
		rec.TS.Format("2006-01-02T15:04:05.000Z07:00")))
	detail := kind
	if method != "" {
		detail += "  method=" + method
	}
	if id != "" {
		detail += "  id=" + id
	}
	sb.WriteString(fmt.Sprintf("Frame:     %s\n", detail))
	if len(rec.Meta) > 0 {
		sb.WriteString("Meta:\n")
		for k, v := range rec.Meta {
			sb.WriteString(fmt.Sprintf("  %s: %s\n", k, v))
		}
	}
	sb.WriteString("\nPayload:\n")
	var v any
	if json.Unmarshal(rec.Payload, &v) == nil {
		pretty, _ := json.MarshalIndent(v, "  ", "  ")
		sb.WriteString("  ")
		sb.WriteString(strings.ReplaceAll(string(pretty), "\n", "\n  "))
	} else {
		sb.WriteString(string(rec.Payload))
	}
	sb.WriteString("\n")
	return sb.String()
}

func (m tuiModel) renderBar() string {
	if m.filterMode {
		return tuiFilter.Render("/ ") + m.filterInput.View() +
			tuiHelp.Render("  enter confirm  esc clear")
	}
	filter := ""
	if v := m.filterInput.Value(); v != "" {
		filter = tuiFilter.Render("  ["+v+"]")
	}
	pos := ""
	if len(m.filtered) > 0 {
		pos = fmt.Sprintf(" %d/%d", m.cursor+1, len(m.filtered))
	}
	return tuiHelp.Render("j/k move  g/G top/bot  J/K detail scroll  / filter  esc clear  q quit") +
		filter + tuiHelp.Render(pos)
}

// ── Height helpers ────────────────────────────────────────────────────────────

func (m tuiModel) listHeight() int {
	avail := m.height - tuiFixed
	if avail < 4 {
		return 2
	}
	return avail * 6 / 10
}

func (m tuiModel) detailHeight() int {
	avail := m.height - tuiFixed
	if avail < 4 {
		return 2
	}
	return avail - avail*6/10
}

// ── Entry point ───────────────────────────────────────────────────────────────

func runTUI(args []string) int {
	fs := flag.NewFlagSet("tui", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `Usage: mcp-scope tui <capture.jsonl>

Interactive TUI explorer for capture files.

Keys:
  j / ↓       next frame            k / ↑       previous frame
  g           jump to top           G           jump to bottom
  ctrl+d      page down             ctrl+u      page up
  J           scroll detail down    K           scroll detail up
  /           filter (regex)        ESC         clear filter
  q           quit

Flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}

	f, err := os.Open(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer f.Close()

	var records []capture.Record
	sc := capture.NewRecordScanner(f)
	for sc.Scan() {
		records = append(records, sc.Record())
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	p := tea.NewProgram(newTUIModel(records, fs.Arg(0)), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "tui: %v\n", err)
		return 1
	}
	return 0
}
