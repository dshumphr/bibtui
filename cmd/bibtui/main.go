package main

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── translation detection ─────────────────────────────────────────────────

func detectTranslations() []string {
	entries, _ := os.ReadDir("bibles")
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// ── model ─────────────────────────────────────────────────────────────────

type model struct {
	index        []ref
	pos          int
	translations []string // active translations
	allTrans     []string // all detected translations
	lines        []string // pre-rendered combined display lines
	scroll       int
	width        int
	height       int
	ready        bool
	pickerOpen   bool
	pickerIdx    int
	store        *AnnotationStore
}

func initial(translations []string, store *AnnotationStore) model {
	allTrans := detectTranslations()
	return model{
		index:        buildIndex(translations[0]),
		translations: translations,
		allTrans:     allTrans,
		store:        store,
	}
}

func (m model) vpH() int {
	if h := m.height - 2; h > 0 {
		return h
	}
	return 1
}

func (m model) maxScroll() int {
	if ms := len(m.lines) - m.vpH(); ms > 0 {
		return ms
	}
	return 0
}

func (m model) scrollPct() int {
	if m.maxScroll() == 0 {
		return 100
	}
	return clamp(int(float64(m.scroll)/float64(m.maxScroll())*100), 0, 100)
}

func (m model) isActive(t string) bool {
	for _, a := range m.translations {
		if a == t {
			return true
		}
	}
	return false
}

// withContent rebuilds m.lines from the current state and returns the updated model.
func (m model) withContent() model {
	if !m.ready || len(m.translations) == 0 || len(m.index) == 0 {
		m.lines = nil
		return m
	}
	m.lines = buildContent(m.index[m.pos], m.translations, m.store, m.width)
	return m
}

func (m model) withScrollClamped() model {
	if ms := m.maxScroll(); m.scroll > ms {
		m.scroll = ms
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
	return m
}

func (m model) withToggled(t string) model {
	if m.isActive(t) {
		if len(m.translations) <= 1 {
			return m // always keep at least one pane
		}
		out := make([]string, 0, len(m.translations)-1)
		for _, a := range m.translations {
			if a != t {
				out = append(out, a)
			}
		}
		m.translations = out
	} else {
		m.translations = append(m.translations, t)
		sort.Strings(m.translations)
	}
	return m
}

// ── bubbletea lifecycle ───────────────────────────────────────────────────

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.ready = true
		m = m.withContent().withScrollClamped()
		return m, nil

	case tea.KeyMsg:
		if m.pickerOpen {
			switch msg.String() {
			case "q", "esc":
				m.pickerOpen = false
			case "j", "down":
				if m.pickerIdx < len(m.allTrans)-1 {
					m.pickerIdx++
				}
			case "k", "up":
				if m.pickerIdx > 0 {
					m.pickerIdx--
				}
			case " ", "enter":
				if m.pickerIdx < len(m.allTrans) {
					m = m.withToggled(m.allTrans[m.pickerIdx])
					m = m.withContent().withScrollClamped()
				}
			}
			return m, nil
		}

		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "j", "down":
			m.scroll = clamp(m.scroll+1, 0, m.maxScroll())
		case "k", "up":
			m.scroll = clamp(m.scroll-1, 0, m.maxScroll())
		case "ctrl+d":
			m.scroll = clamp(m.scroll+m.vpH()/2, 0, m.maxScroll())
		case "ctrl+u":
			m.scroll = clamp(m.scroll-m.vpH()/2, 0, m.maxScroll())
		case "]":
			if m.pos < len(m.index)-1 {
				m.pos++
				m = m.withContent()
				m.scroll = 0
			}
		case "[":
			if m.pos > 0 {
				m.pos--
				m = m.withContent()
				m.scroll = 0
			}
		case "o":
			m.pickerOpen = true
		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			if m.store == nil {
				break
			}
			idx, _ := strconv.Atoi(msg.String())
			idx-- // 1-based key → 0-based index
			names := sortedGroupNames(m.store)
			if idx < len(names) {
				m.store.ToggleGroup(names[idx])
				m = m.withContent().withScrollClamped()
			}
		}
	}
	return m, nil
}

// ── view ──────────────────────────────────────────────────────────────────

func (m model) statusBar() string {
	c := m.index[m.pos]
	transLabel := strings.ToUpper(strings.Join(m.translations, " · "))

	var annLabel string
	if m.store != nil {
		var active []string
		for _, name := range sortedGroupNames(m.store) {
			if m.store.ActiveGroups[name] {
				active = append(active, name)
			}
		}
		if len(active) > 0 {
			annLabel = " · " + strings.Join(active, ",")
		}
	}

	left := fmt.Sprintf("  %s %d  ·  %s%s  ", c.book.name, c.num, transLabel, annLabel)
	right := fmt.Sprintf("  %d%%  ", m.scrollPct())
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 0 {
		gap = 0
	}
	return statusSt.Render(left + strings.Repeat(" ", gap) + right)
}

func (m model) helpBar() string {
	if m.pickerOpen {
		return helpSt.Render("  space toggle  ·  j/k move  ·  q/esc close")
	}
	groups := sortedGroupNames(m.store)
	var groupHelp string
	if len(groups) > 0 {
		groupHelp = " ·  1-" + strconv.Itoa(clamp(len(groups), 1, 9)) + " toggle groups"
	}
	return helpSt.Render("  j/k scroll  ·  [/] chapter  ·  o open/close" + groupHelp + "  ·  q quit")
}

func (m model) View() string {
	if !m.ready {
		return "\n  Loading..."
	}

	if m.pickerOpen {
		return m.statusBar() + "\n" + m.pickerContent() + "\n" + m.helpBar()
	}

	vpH := m.vpH()
	end := clamp(m.scroll+vpH, 0, len(m.lines))
	rows := m.lines[m.scroll:end]
	content := strings.Join(rows, "\n")
	if len(rows) < vpH {
		content += strings.Repeat("\n", vpH-len(rows))
	}
	return m.statusBar() + "\n" + content + "\n" + m.helpBar()
}

func (m model) pickerContent() string {
	var rows []string
	for i, t := range m.allTrans {
		cursor := "  "
		if i == m.pickerIdx {
			cursor = "▶ "
		}
		check := " "
		if m.isActive(t) {
			check = "✓"
		}
		line := fmt.Sprintf("%s[%s] %s", cursor, check, strings.ToUpper(t))
		if i == m.pickerIdx {
			line = pickerCursorSt.Render(line)
		}
		rows = append(rows, line)
	}
	box := pickerBorderSt.Render(strings.Join(rows, "\n"))
	return lipgloss.Place(m.width, m.vpH(), lipgloss.Center, lipgloss.Center, box)
}

// ── entry point ───────────────────────────────────────────────────────────

func main() {
	allTrans := detectTranslations()
	if len(allTrans) == 0 {
		fmt.Fprintln(os.Stderr, "no translations found under bibles/")
		os.Exit(1)
	}

	store := NewAnnotationStore(defaultStorePath)
	if err := store.Load(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not load annotations: %v\n", err)
	}
	// Auto-activate every group on startup so the prototype is immediately visible.
	for name := range store.Groups {
		store.ActiveGroups[name] = true
	}

	startTrans := os.Args[1:]
	if len(startTrans) == 0 {
		startTrans = []string{"kjv"}
		found := false
		for _, t := range allTrans {
			if t == "kjv" {
				found = true
				break
			}
		}
		if !found {
			startTrans = []string{allTrans[0]}
		}
	}

	for _, t := range startTrans {
		found := false
		for _, a := range allTrans {
			if a == t {
				found = true
				break
			}
		}
		if !found {
			fmt.Fprintf(os.Stderr, "translation %q not found under bibles/\n", t)
			os.Exit(1)
		}
	}

	m := initial(startTrans, store)
	if len(m.index) == 0 {
		fmt.Fprintln(os.Stderr, "no chapters found")
		os.Exit(1)
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
