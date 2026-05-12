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

// ── app mode ─────────────────────────────────────────────────────────────

type appMode int

const (
	modeHome   appMode = iota
	modeReader
)

// ── model ─────────────────────────────────────────────────────────────────

type model struct {
	mode    appMode
	session *Session // last loaded session, shown on home screen
	index        []ref
	pos          int
	translations []string // active translations
	allTrans     []string // all detected translations
	lines        []string // pre-rendered combined display lines
	verseMap     map[int]int // verse number → line offset within m.lines
	scroll       int
	width        int
	height       int
	ready        bool
	pickerOpen   bool
	pickerIdx    int
	gotoOpen     bool
	gotoInput    string
	gotoCands    []int // indices into books[] matching current input
	gotoCursor   int
	store        *AnnotationStore
}

// withMode switches the app mode and triggers a content rebuild when entering
// the reader so that lines are populated before the first render.
func (m model) withMode(mode appMode) model {
	m.mode = mode
	if mode == modeReader {
		m = m.withContent().withScrollClamped()
	}
	return m
}

// applySession restores position, scroll, and translations from a saved session.
func (m model) applySession(s *Session) model {
	for i, r := range m.index {
		if r.book.slug == s.BookSlug && r.num == s.Chapter {
			m.pos = i
			break
		}
	}
	if len(s.Translations) > 0 {
		valid := make([]string, 0, len(s.Translations))
		for _, t := range s.Translations {
			for _, a := range m.allTrans {
				if a == t {
					valid = append(valid, t)
					break
				}
			}
		}
		if len(valid) > 0 {
			m.translations = valid
		}
	}
	m.scroll = s.Scroll
	return m
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

// withContent rebuilds m.lines and m.verseMap from the current state.
func (m model) withContent() model {
	if !m.ready || len(m.translations) == 0 || len(m.index) == 0 {
		m.lines = nil
		m.verseMap = nil
		return m
	}
	m.lines, m.verseMap = buildContent(m.index[m.pos], m.translations, m.store, m.width)
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
		if m.mode == modeReader {
			m = m.withContent().withScrollClamped()
		}
		return m, nil

	case tea.KeyMsg:
		if m.mode == modeHome {
			switch msg.String() {
			case "q", "ctrl+c":
				return m, tea.Quit
			case "n":
				m.pos = 0
				m.scroll = 0
				m = m.withMode(modeReader)
				m.gotoOpen = true
				m.gotoInput = ""
				m.gotoCands = nil
				m.gotoCursor = 0
			case "r":
				if m.session != nil {
					m = m.applySession(m.session)
					m = m.withMode(modeReader)
					m.scroll = m.session.Scroll
					m = m.withScrollClamped()
				}
			}
			return m, nil
		}

		if m.gotoOpen {
			switch msg.Type {
			case tea.KeyEsc:
				m.gotoOpen = false
				m.gotoInput = ""
				m.gotoCands = nil
			case tea.KeyEnter:
				_, _, verse := parseGotoQuery(m.gotoInput)
				newPos := m.resolveGoto()
				m.gotoOpen = false
				m.gotoInput = ""
				m.gotoCands = nil
				if newPos >= 0 {
					m.pos = newPos
					m = m.withContent()
					m.scroll = 0
					if verse > 0 && m.verseMap != nil {
						if off, ok := m.verseMap[verse]; ok {
							m.scroll = clamp(off, 0, m.maxScroll())
						}
					}
					saveSession(m)
				}
			case tea.KeyTab:
				m = m.gotoComplete()
			case tea.KeyUp:
				if m.gotoCursor > 0 {
					m.gotoCursor--
				}
			case tea.KeyDown:
				if m.gotoCursor < len(m.gotoCands)-1 {
					m.gotoCursor++
				}
			case tea.KeyBackspace:
				if len(m.gotoInput) > 0 {
					runes := []rune(m.gotoInput)
					m.gotoInput = string(runes[:len(runes)-1])
					m = m.updateGotoCands()
				}
			case tea.KeyRunes:
				m.gotoInput += string(msg.Runes)
				m = m.updateGotoCands()
			}
			return m, nil
		}

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
			saveSession(m)
			return m, tea.Quit
		case ":":
			m.gotoOpen = true
			m.gotoInput = ""
			m.gotoCands = nil
			m.gotoCursor = 0
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
				saveSession(m)
			}
		case "[":
			if m.pos > 0 {
				m.pos--
				m = m.withContent()
				m.scroll = 0
				saveSession(m)
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

func (m model) updateGotoCands() model {
	bookQuery, _, _ := parseGotoQuery(m.gotoInput)
	m.gotoCands = bookCandidates(bookQuery)
	m.gotoCursor = 0
	return m
}

func (m model) resolveGoto() int {
	bookQuery, chapter, _ := parseGotoQuery(m.gotoInput)
	cands := bookCandidates(bookQuery)
	if len(cands) == 0 {
		return -1
	}
	bookIdx := cands[0]
	if m.gotoCursor < len(cands) {
		bookIdx = cands[m.gotoCursor]
	}
	return findIndexPos(m.index, bookIdx, chapter)
}

func (m model) gotoComplete() model {
	if len(m.gotoCands) == 0 {
		return m
	}
	idx := m.gotoCursor
	if idx >= len(m.gotoCands) {
		idx = 0
	}
	bookName := books[m.gotoCands[idx]].name
	m.gotoInput = gotoCompleteInput(m.gotoInput, bookName)
	m = m.updateGotoCands()
	return m
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
	return helpSt.Render("  j/k scroll  ·  [/] chapter  ·  : goto  ·  o translations" + groupHelp + "  ·  q quit")
}

// viewWithGoto renders the normal content but shrinks it to make room for
// a candidate list and a command bar at the bottom, keeping the text visible.
func (m model) viewWithGoto() string {
	bookQuery, chapter, verse := parseGotoQuery(m.gotoInput)

	// How many candidate rows to show (plus one separator line).
	maxCands := 6
	n := len(m.gotoCands)
	if n > maxCands {
		n = maxCands
	}
	popupLines := 0
	if bookQuery != "" && n > 0 {
		popupLines = n + 1 // separator + candidates
	}

	// Content — shrink to leave room for popup.
	contentH := m.vpH() - popupLines
	if contentH < 0 {
		contentH = 0
	}
	end := clamp(m.scroll+contentH, 0, len(m.lines))
	var content string
	if contentH > 0 {
		rows := m.lines[m.scroll:end]
		content = strings.Join(rows, "\n")
		if len(rows) < contentH {
			content += strings.Repeat("\n", contentH-len(rows))
		}
		content += "\n"
	}

	// Candidate popup rows.
	var popup string
	if popupLines > 0 {
		popup += divSt.Render(strings.Repeat("─", m.width)) + "\n"
		for i := 0; i < n; i++ {
			prefix := "  "
			if i == m.gotoCursor {
				prefix = "▶ "
			}
			b := books[m.gotoCands[i]]
			desc := b.name
			if chapter > 0 && verse > 0 {
				desc = fmt.Sprintf("%s %d:%d", b.name, chapter, verse)
			} else if chapter > 0 {
				desc = fmt.Sprintf("%s %d", b.name, chapter)
			}
			line := prefix + desc
			if i == m.gotoCursor {
				line = pickerCursorSt.Render(line)
			}
			popup += padToWidth(line, m.width) + "\n"
		}
	}

	// Command bar (replaces help bar).
	cmdBar := helpSt.Render(
		padToWidth("  "+pickerCursorSt.Render(":")+" "+m.gotoInput+"█"+"  tab complete  ·  ↑/↓ navigate  ·  esc cancel", m.width),
	)

	return m.statusBar() + "\n" + content + popup + cmdBar
}

func (m model) View() string {
	if !m.ready {
		return "\n  Loading..."
	}

	if m.mode == modeHome {
		return homeView(m)
	}

	if m.gotoOpen {
		return m.viewWithGoto()
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

	session := loadSession()

	m := initial(startTrans, store)
	if len(m.index) == 0 {
		fmt.Fprintln(os.Stderr, "no chapters found")
		os.Exit(1)
	}
	m.session = session
	m.mode = modeHome

	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
