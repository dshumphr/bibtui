package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

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

// ── parsing ───────────────────────────────────────────────────────────────

type verse struct {
	num  int
	text string
}

var verseRe = regexp.MustCompile(`^\*\*(\d+)\*\* (.+)`)

func parseChapter(translation, slug string, chNum int) []verse {
	path := filepath.Join("bibles", translation, slug, fmt.Sprintf("%d.md", chNum))
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var verses []verse
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if match := verseRe.FindStringSubmatch(line); match != nil {
			n, _ := strconv.Atoi(match[1])
			verses = append(verses, verse{n, match[2]})
		}
	}
	return verses
}

// ── text utilities ────────────────────────────────────────────────────────

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

// padToWidth pads s with trailing spaces to reach the target visual width.
func padToWidth(s string, width int) string {
	w := lipgloss.Width(s)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}

// centerPad centres s within a field of the given visual width using spaces.
func centerPad(s string, width int) string {
	w := lipgloss.Width(s)
	if w >= width {
		return s
	}
	left := (width - w) / 2
	right := width - w - left
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", right)
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// ── styles ────────────────────────────────────────────────────────────────

var (
	verseNumSt = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#7C4E00", Dark: "#CBA252"})

	divSt = lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#CCCCCC", Dark: "#444444"})

	labelSt = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.AdaptiveColor{Light: "#555555", Dark: "#AAAAAA"})

	statusSt = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#F5F5F5", Dark: "#1A1A1A"}).
			Background(lipgloss.AdaptiveColor{Light: "#383838", Dark: "#C8C8C8"})

	helpSt = lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#9B9B9B", Dark: "#525252"})

	pickerBorderSt = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			Padding(1, 3).
			BorderForeground(lipgloss.AdaptiveColor{Light: "#383838", Dark: "#C8C8C8"})

	pickerCursorSt = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#7C4E00", Dark: "#CBA252"})
)

// ── content rendering ─────────────────────────────────────────────────────

const (
	leftMargin = 2
	numCols    = 4 // "NNN " — fits up to 3-digit verse numbers
)

// renderVerseLines renders a single verse into display lines for a pane of
// width paneW, with an inline verse number and word-wrapped text.
func renderVerseLines(v verse, paneW int) []string {
	textW := paneW - leftMargin - numCols - 1
	if textW < 10 {
		textW = 10
	}
	num := verseNumSt.Render(fmt.Sprintf("%3d ", v.num))
	cont := strings.Repeat(" ", leftMargin+numCols)
	wrapped := wordWrap(v.text, textW)
	var lines []string
	for i, wl := range wrapped {
		if i == 0 {
			lines = append(lines, strings.Repeat(" ", leftMargin)+num+wl)
		} else {
			lines = append(lines, cont+wl)
		}
	}
	return lines
}

// buildContent renders a chapter for all active translations into a slice of
// display lines. Each verse occupies the same number of rows in every pane
// (padded to the tallest rendering) so that verse boundaries stay aligned.
func buildContent(c ref, translations []string, paneW int) []string {
	n := len(translations)
	if n == 0 {
		return nil
	}

	div := divSt.Render("│")
	blank := strings.Repeat(" ", paneW)
	blankRow := strings.Join(repN(blank, n), div)

	// Translation label row and separator.
	labelParts := make([]string, n)
	sepParts := make([]string, n)
	for i, t := range translations {
		labelParts[i] = padToWidth(labelSt.Render(centerPad(strings.ToUpper(t), paneW)), paneW)
		sepParts[i] = divSt.Render(strings.Repeat("─", paneW))
	}
	result := []string{
		"",
		strings.Join(labelParts, divSt.Render("│")),
		strings.Join(sepParts, divSt.Render("┼")),
		"",
	}

	// Parse each translation's verses.
	allVerses := make([][]verse, n)
	maxV := 0
	for i, t := range translations {
		allVerses[i] = parseChapter(t, c.book.slug, c.num)
		if len(allVerses[i]) > maxV {
			maxV = len(allVerses[i])
		}
	}

	// For each verse slot, render all panes, find the tallest, pad the rest.
	for vi := 0; vi < maxV; vi++ {
		paneLines := make([][]string, n)
		maxH := 0
		for pi := range translations {
			if vi < len(allVerses[pi]) {
				paneLines[pi] = renderVerseLines(allVerses[pi][vi], paneW)
			}
			if len(paneLines[pi]) > maxH {
				maxH = len(paneLines[pi])
			}
		}
		for li := 0; li < maxH; li++ {
			parts := make([]string, n)
			for pi := range translations {
				var s string
				if li < len(paneLines[pi]) {
					s = paneLines[pi][li]
				}
				parts[pi] = padToWidth(s, paneW)
			}
			result = append(result, strings.Join(parts, div))
		}
		result = append(result, blankRow)
	}

	return result
}

func repN(s string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s
	}
	return out
}

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
}

func initial(translations []string) model {
	allTrans := detectTranslations()
	return model{
		index:        buildIndex(translations[0]),
		translations: translations,
		allTrans:     allTrans,
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
	n := len(m.translations)
	pw := (m.width - (n - 1)) / n
	if pw < 15 {
		pw = 15
	}
	m.lines = buildContent(m.index[m.pos], m.translations, pw)
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
		}
	}
	return m, nil
}

// ── view ──────────────────────────────────────────────────────────────────

func (m model) statusBar() string {
	c := m.index[m.pos]
	transLabel := strings.ToUpper(strings.Join(m.translations, " · "))
	left := fmt.Sprintf("  %s %d  ·  %s  ", c.book.name, c.num, transLabel)
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
	return helpSt.Render("  j/k scroll  ·  [/] chapter  ·  o open/close  ·  q quit")
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

	m := initial(startTrans)
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
