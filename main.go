package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── canonical book order (Genesis → Revelation) ───────────────────────────

type book struct{ slug, name string }

var books = []book{
	{"genesis", "Genesis"},
	{"exodus", "Exodus"},
	{"leviticus", "Leviticus"},
	{"numbers", "Numbers"},
	{"deuteronomy", "Deuteronomy"},
	{"joshua", "Joshua"},
	{"judges", "Judges"},
	{"ruth", "Ruth"},
	{"1-samuel", "1 Samuel"},
	{"2-samuel", "2 Samuel"},
	{"1-kings", "1 Kings"},
	{"2-kings", "2 Kings"},
	{"1-chronicles", "1 Chronicles"},
	{"2-chronicles", "2 Chronicles"},
	{"ezra", "Ezra"},
	{"nehemiah", "Nehemiah"},
	{"esther", "Esther"},
	{"job", "Job"},
	{"psalms", "Psalms"},
	{"proverbs", "Proverbs"},
	{"ecclesiastes", "Ecclesiastes"},
	{"song-of-solomon", "Song of Solomon"},
	{"isaiah", "Isaiah"},
	{"jeremiah", "Jeremiah"},
	{"lamentations", "Lamentations"},
	{"ezekiel", "Ezekiel"},
	{"daniel", "Daniel"},
	{"hosea", "Hosea"},
	{"joel", "Joel"},
	{"amos", "Amos"},
	{"obadiah", "Obadiah"},
	{"jonah", "Jonah"},
	{"micah", "Micah"},
	{"nahum", "Nahum"},
	{"habakkuk", "Habakkuk"},
	{"zephaniah", "Zephaniah"},
	{"haggai", "Haggai"},
	{"zechariah", "Zechariah"},
	{"malachi", "Malachi"},
	{"matthew", "Matthew"},
	{"mark", "Mark"},
	{"luke", "Luke"},
	{"john", "John"},
	{"acts", "Acts"},
	{"romans", "Romans"},
	{"1-corinthians", "1 Corinthians"},
	{"2-corinthians", "2 Corinthians"},
	{"galatians", "Galatians"},
	{"ephesians", "Ephesians"},
	{"philippians", "Philippians"},
	{"colossians", "Colossians"},
	{"1-thessalonians", "1 Thessalonians"},
	{"2-thessalonians", "2 Thessalonians"},
	{"1-timothy", "1 Timothy"},
	{"2-timothy", "2 Timothy"},
	{"titus", "Titus"},
	{"philemon", "Philemon"},
	{"hebrews", "Hebrews"},
	{"james", "James"},
	{"1-peter", "1 Peter"},
	{"2-peter", "2 Peter"},
	{"1-john", "1 John"},
	{"2-john", "2 John"},
	{"3-john", "3 John"},
	{"jude", "Jude"},
	{"revelation", "Revelation"},
}

// ── chapter index ─────────────────────────────────────────────────────────

type ref struct {
	book book
	num  int
}

func buildIndex(translation string) []ref {
	var refs []ref
	base := filepath.Join("bibles", translation)
	for _, b := range books {
		entries, err := os.ReadDir(filepath.Join(base, b.slug))
		if err != nil {
			continue
		}
		var nums []int
		for _, e := range entries {
			n, err := strconv.Atoi(strings.TrimSuffix(e.Name(), ".md"))
			if err == nil {
				nums = append(nums, n)
			}
		}
		sort.Ints(nums)
		for _, n := range nums {
			refs = append(refs, ref{b, n})
		}
	}
	return refs
}

// ── styles ────────────────────────────────────────────────────────────────

var (
	verseNumSt = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#7C4E00", Dark: "#CBA252"})

	statusSt = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#F5F5F5", Dark: "#1A1A1A"}).
			Background(lipgloss.AdaptiveColor{Light: "#383838", Dark: "#C8C8C8"})

	helpSt = lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#9B9B9B", Dark: "#525252"})
)

// ── model ─────────────────────────────────────────────────────────────────

type model struct {
	index       []ref
	pos         int
	vp          viewport.Model
	width       int
	height      int
	ready       bool
	translation string
}

func initial(translation string) model {
	return model{
		index:       buildIndex(translation),
		translation: translation,
	}
}

func (m model) cur() ref { return m.index[m.pos] }

func (m model) chapterPath() string {
	c := m.cur()
	return filepath.Join("bibles", m.translation, c.book.slug, fmt.Sprintf("%d.md", c.num))
}

// ── content rendering ─────────────────────────────────────────────────────

var verseRe = regexp.MustCompile(`^\*\*(\d+)\*\* (.+)`)

func wordWrap(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}
	var lines []string
	var cur strings.Builder
	for _, word := range strings.Fields(text) {
		switch {
		case cur.Len() == 0:
			cur.WriteString(word)
		case cur.Len()+1+len(word) <= width:
			cur.WriteByte(' ')
			cur.WriteString(word)
		default:
			lines = append(lines, cur.String())
			cur.Reset()
			cur.WriteString(word)
		}
	}
	if cur.Len() > 0 {
		lines = append(lines, cur.String())
	}
	return lines
}

func (m model) renderContent() string {
	raw, err := os.ReadFile(m.chapterPath())
	if err != nil {
		return fmt.Sprintf("\n  error reading file: %v\n", err)
	}

	w := m.width
	if w <= 0 {
		w = 80
	}

	// 2-char left margin; verse num column is "NNN " = 4 chars wide
	const leftMargin = 2
	const numCols = 4
	textCols := w - leftMargin - numCols - 1 // 1-char right margin
	if textCols < 20 {
		textCols = 20
	}
	continuation := strings.Repeat(" ", leftMargin+numCols)

	var sb strings.Builder
	sb.WriteByte('\n')

	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		match := verseRe.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		vnum, text := match[1], match[2]
		numStr := verseNumSt.Render(fmt.Sprintf("%3s ", vnum))
		wrapped := wordWrap(text, textCols)
		for i, wl := range wrapped {
			if i == 0 {
				fmt.Fprintf(&sb, "%s%s%s\n", strings.Repeat(" ", leftMargin), numStr, wl)
			} else {
				fmt.Fprintf(&sb, "%s%s\n", continuation, wl)
			}
		}
		sb.WriteByte('\n')
	}

	return sb.String()
}

// ── status / help bars ────────────────────────────────────────────────────

func (m model) statusBar() string {
	c := m.cur()
	left := fmt.Sprintf("  %s %d  ·  %s  ", c.book.name, c.num, strings.ToUpper(m.translation))
	pct := int(m.vp.ScrollPercent() * 100)
	right := fmt.Sprintf("  %d%%  ", pct)

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 0 {
		gap = 0
	}
	return statusSt.Render(left + strings.Repeat(" ", gap) + right)
}

func (m model) helpBar() string {
	return helpSt.Render("  j/k scroll  ·  [/] chapter  ·  q quit")
}

// ── bubbletea lifecycle ───────────────────────────────────────────────────

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "]":
			if m.pos < len(m.index)-1 {
				m.pos++
				m.vp.SetContent(m.renderContent())
				m.vp.GotoTop()
			}
			return m, nil
		case "[":
			if m.pos > 0 {
				m.pos--
				m.vp.SetContent(m.renderContent())
				m.vp.GotoTop()
			}
			return m, nil
		}

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		vpH := msg.Height - 2
		if !m.ready {
			m.vp = viewport.New(msg.Width, vpH)
			m.vp.SetContent(m.renderContent())
			m.ready = true
		} else {
			m.vp.Width = msg.Width
			m.vp.Height = vpH
			m.vp.SetContent(m.renderContent())
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

func (m model) View() string {
	if !m.ready {
		return "\n  Loading..."
	}
	return m.statusBar() + "\n" + m.vp.View() + "\n" + m.helpBar()
}

// ── entry point ───────────────────────────────────────────────────────────

func main() {
	translation := "kjv"
	if len(os.Args) > 1 {
		translation = os.Args[1]
	}

	m := initial(translation)
	if len(m.index) == 0 {
		fmt.Fprintf(os.Stderr, "no chapters found for translation %q under bibles/\n", translation)
		os.Exit(1)
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
