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

type noteUIMode int

const (
	noteUIOff   noteUIMode = iota
	noteUIOuter            // browsing the annotation column verse-by-verse
	noteUIInner            // managing notes for a single verse
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
	stats        *Stats

	noteUI            noteUIMode
	noteCursorVerse   int // verse highlighted in the annotation column (outer)
	noteRef           AnnotationRef
	noteCands         []Annotation
	noteCursor        int // row inside the inner panel (0..len(noteCands) for + new note)
	noteInput         string
	noteInputCursor   int  // rune index within noteInput
	noteEditIdx       int  // -1 = creating new, >=0 = editing existing index
	noteDeleteConfirm bool // y/n prompt for deletion
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

func initial(translations []string, store *AnnotationStore, stats *Stats) model {
	allTrans := detectTranslations()
	return model{
		index:        buildIndex(translations[0]),
		translations: translations,
		allTrans:     allTrans,
		store:        store,
		stats:        stats,
	}
}

// recordView marks the currently-positioned chapter as viewed.
func (m model) recordView() {
	if m.stats == nil || len(m.index) == 0 {
		return
	}
	r := m.index[m.pos]
	m.stats.RecordView(r.book.slug, r.num)
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
	cursor := 0
	if m.noteUI != noteUIOff {
		cursor = m.noteCursorVerse
	}
	m.lines, m.verseMap = buildContent(m.index[m.pos], m.translations, m.store, m.width, cursor, m.noteUI != noteUIOff)
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

// activeRef returns the verse whose top line is at or above the current scroll
// position.  If no verse is visible yet, it returns an empty ref.
func (m model) activeRef() AnnotationRef {
	if m.verseMap == nil || len(m.index) == 0 {
		return AnnotationRef{}
	}
	c := m.index[m.pos]
	bestV := 0
	bestOff := -1
	for vnum, off := range m.verseMap {
		if off <= m.scroll && (off > bestOff || (off == bestOff && vnum > bestV)) {
			bestOff = off
			bestV = vnum
		}
	}
	if bestV == 0 {
		return AnnotationRef{}
	}
	return AnnotationRef{Book: c.book.slug, Chapter: c.num, Verse: bestV}
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
					if m.mode == modeHome {
						m.mode = modeReader
					}
					saveSession(m)
					m.recordView()
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

		if m.noteUI == noteUIInner {
			return m.updateInnerNote(msg)
		}
		if m.noteUI == noteUIOuter {
			return m.updateOuterNote(msg)
		}

		if m.mode == modeHome {
			switch msg.String() {
			case "q", "ctrl+c":
				return m, tea.Quit
			case "n":
				m.pos = 0
				m.scroll = 0
				m = m.withMode(modeReader)
				m.recordView()
			case "g":
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
					m.recordView()
				}
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
				m.recordView()
			}
		case "[":
			if m.pos > 0 {
				m.pos--
				m = m.withContent()
				m.scroll = 0
				saveSession(m)
				m.recordView()
			}
		case "o":
			m.pickerOpen = true
		case "n":
			if m.store != nil && len(m.verseMap) > 0 {
				ref := m.activeRef()
				v := ref.Verse
				if v == 0 {
					vs := m.sortedVerses()
					if len(vs) > 0 {
						v = vs[0]
					}
				}
				if v > 0 {
					m.noteUI = noteUIOuter
					m.noteCursorVerse = v
					m = m.withContent()
					m = m.ensureCursorVisible()
				}
			}
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
	if m.noteUI == noteUIOuter {
		left += "·  NOTE  "
	} else if m.noteUI == noteUIInner {
		left += "·  NOTE › " + m.noteRef.String() + "  "
	}
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
	if m.noteUI == noteUIOuter {
		return helpSt.Render("  j/k verse  ·  enter open  ·  esc exit note mode")
	}
	groups := sortedGroupNames(m.store)
	var groupHelp string
	if len(groups) > 0 {
		groupHelp = " ·  1-" + strconv.Itoa(clamp(len(groups), 1, 9)) + " toggle groups"
	}
	return helpSt.Render("  j/k scroll  ·  [/] chapter  ·  : goto  ·  o translations  ·  n notes" + groupHelp + "  ·  q quit")
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

// viewWithNotes renders the normal content but shrinks it to make room for
// the per-verse note overlay at the bottom.
func (m model) viewWithNotes() string {
	var overlay []string

	title := fmt.Sprintf("  Notes on %s  (other annotation groups are read-only)  ", m.noteRef.String())
	overlay = append(overlay, helpSt.Render(padToWidth(title, m.width)))

	// Existing notes
	for i, note := range m.noteCands {
		prefix := "    "
		if i == m.noteCursor {
			prefix = "  ▸ "
		}
		text := prefix + note.Text
		wrapped := wordWrap(text, m.width-4)
		for j, wl := range wrapped {
			line := padToWidth(wl, m.width)
			if i == m.noteCursor && j == 0 {
				line = pickerCursorSt.Render(line)
			}
			overlay = append(overlay, line)
		}
	}

	// Permanent "+ new note" row
	newPrefix := "    "
	if m.noteCursor == len(m.noteCands) {
		newPrefix = "  ▸ "
	}
	newLine := padToWidth(newPrefix+"+ new note", m.width)
	if m.noteCursor == len(m.noteCands) {
		newLine = pickerCursorSt.Render(newLine)
	} else {
		newLine = noteAddSt.Render(newLine)
	}
	overlay = append(overlay, newLine)

	overlay = append(overlay, divSt.Render(strings.Repeat("─", m.width)))

	// Input / prompt line
	var inputLine string
	switch {
	case m.noteDeleteConfirm:
		inputLine = "  " + pickerCursorSt.Render("delete this note? (y/n)")
	case m.noteEditIdx >= 0:
		inputLine = "  edit > " + renderInputWithCursor(m.noteInput, m.noteInputCursor)
	case m.noteInput != "" || m.noteCursor == len(m.noteCands):
		inputLine = "  new  > " + renderInputWithCursor(m.noteInput, m.noteInputCursor)
	default:
		inputLine = "  enter to edit · esc to close"
		inputLine = helpSt.Render(inputLine)
	}
	overlay = append(overlay, padToWidth(inputLine, m.width))

	// Help
	var helpText string
	switch {
	case m.noteDeleteConfirm:
		helpText = "  y confirm · n/esc cancel  "
	case m.noteInput != "" || m.noteEditIdx >= 0 || m.noteCursor == len(m.noteCands):
		helpText = "  enter save · esc cancel  "
	default:
		helpText = "  j/k navigate · enter edit · d delete · esc back  "
	}
	overlay = append(overlay, helpSt.Render(padToWidth(helpText, m.width)))

	overlayH := len(overlay)
	contentH := m.vpH() + 1 - overlayH
	if contentH < 0 {
		contentH = 0
	}

	var content string
	if contentH > 0 {
		end := clamp(m.scroll+contentH, 0, len(m.lines))
		rows := m.lines[m.scroll:end]
		content = strings.Join(rows, "\n")
		if len(rows) < contentH {
			content += strings.Repeat("\n", contentH-len(rows))
		}
		content += "\n"
	}

	return m.statusBar() + "\n" + content + strings.Join(overlay, "\n")
}

// renderInputWithCursor renders a text input with a block cursor at the given
// rune position.
func renderInputWithCursor(s string, pos int) string {
	runes := []rune(s)
	if pos < 0 {
		pos = 0
	}
	if pos > len(runes) {
		pos = len(runes)
	}
	if pos == len(runes) {
		return string(runes) + inputCursorSt.Render(" ")
	}
	return string(runes[:pos]) + inputCursorSt.Render(string(runes[pos])) + string(runes[pos+1:])
}

// sortedVerses returns the verse numbers of the current chapter in ascending order.
func (m model) sortedVerses() []int {
	out := make([]int, 0, len(m.verseMap))
	for v := range m.verseMap {
		out = append(out, v)
	}
	sort.Ints(out)
	return out
}

// ensureCursorVisible scrolls so the note-mode cursor verse stays on screen.
func (m model) ensureCursorVisible() model {
	if m.noteUI == noteUIOff {
		return m
	}
	off, ok := m.verseMap[m.noteCursorVerse]
	if !ok {
		return m
	}
	vpH := m.vpH()
	if off < m.scroll {
		m.scroll = off
	} else if off >= m.scroll+vpH-2 {
		m.scroll = off - vpH + 3
	}
	return m.withScrollClamped()
}

// openInnerNote enters the per-verse note manager for the cursor verse.
func (m model) openInnerNote() model {
	m.noteUI = noteUIInner
	m.noteRef = AnnotationRef{
		Book:    m.index[m.pos].book.slug,
		Chapter: m.index[m.pos].num,
		Verse:   m.noteCursorVerse,
	}
	if m.store != nil {
		m.noteCands = m.store.NotesAtRef(m.noteRef)
	}
	m.noteCursor = 0
	m.noteInput = ""
	m.noteInputCursor = 0
	m.noteEditIdx = -1
	m.noteDeleteConfirm = false
	return m
}

// closeInnerNote drops back to the outer note mode without leaving note mode entirely.
func (m model) closeInnerNote() model {
	m.noteUI = noteUIOuter
	m.noteInput = ""
	m.noteInputCursor = 0
	m.noteEditIdx = -1
	m.noteDeleteConfirm = false
	m.noteCands = nil
	m = m.withContent().withScrollClamped()
	return m
}

// updateOuterNote handles key input when navigating the annotation column.
func (m model) updateOuterNote(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	verses := m.sortedVerses()
	if len(verses) == 0 {
		m.noteUI = noteUIOff
		m = m.withContent()
		return m, nil
	}
	idx := 0
	for i, v := range verses {
		if v == m.noteCursorVerse {
			idx = i
			break
		}
	}
	switch msg.String() {
	case "esc", "q":
		m.noteUI = noteUIOff
		m = m.withContent().withScrollClamped()
		return m, nil
	case "enter":
		m = m.openInnerNote()
		return m, nil
	case "j", "down":
		if idx < len(verses)-1 {
			m.noteCursorVerse = verses[idx+1]
			m = m.withContent()
			m = m.ensureCursorVisible()
		}
		return m, nil
	case "k", "up":
		if idx > 0 {
			m.noteCursorVerse = verses[idx-1]
			m = m.withContent()
			m = m.ensureCursorVisible()
		}
		return m, nil
	case "g":
		m.noteCursorVerse = verses[0]
		m = m.withContent()
		m = m.ensureCursorVisible()
		return m, nil
	case "G":
		m.noteCursorVerse = verses[len(verses)-1]
		m = m.withContent()
		m = m.ensureCursorVisible()
		return m, nil
	}
	return m, nil
}

// updateInnerNote handles key input when managing notes for a single verse.
func (m model) updateInnerNote(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Delete confirm prompt owns the keyboard while active.
	if m.noteDeleteConfirm {
		switch msg.String() {
		case "y", "Y":
			if m.store != nil && m.noteCursor < len(m.noteCands) {
				m.store.DeleteNoteAt(m.noteRef, m.noteCursor)
				m.store.Save()
				m.noteCands = m.store.NotesAtRef(m.noteRef)
				if m.noteCursor >= len(m.noteCands) && m.noteCursor > 0 {
					m.noteCursor--
				}
				m = m.withContent().withScrollClamped()
			}
			m.noteDeleteConfirm = false
		default:
			m.noteDeleteConfirm = false
		}
		return m, nil
	}

	editing := m.noteEditIdx >= 0 || m.noteInput != ""

	switch msg.Type {
	case tea.KeyEsc:
		if editing && (m.noteInput != "" || m.noteEditIdx >= 0) {
			// cancel the in-progress edit, stay in inner panel
			m.noteInput = ""
			m.noteInputCursor = 0
			m.noteEditIdx = -1
			return m, nil
		}
		m = m.closeInnerNote()
		return m, nil
	case tea.KeyEnter:
		if m.noteInput == "" && m.noteEditIdx < 0 {
			// "enter" on a note row → load it for editing
			if m.noteCursor < len(m.noteCands) {
				m.noteInput = m.noteCands[m.noteCursor].Text
				m.noteInputCursor = len([]rune(m.noteInput))
				m.noteEditIdx = m.noteCursor
			}
			// "enter" on + new note row → just start typing (input stays empty)
			return m, nil
		}
		// Save and return to outer note mode.
		if m.store != nil {
			if m.noteEditIdx >= 0 && m.noteEditIdx < len(m.noteCands) {
				m.store.UpdateNote(m.noteRef, m.noteEditIdx, m.noteInput)
			} else if m.noteInput != "" {
				m.store.AddNote(m.noteRef, m.noteInput)
			}
			m.store.Save()
		}
		m = m.closeInnerNote()
		return m, nil
	case tea.KeyBackspace:
		if editing && m.noteInputCursor > 0 {
			runes := []rune(m.noteInput)
			runes = append(runes[:m.noteInputCursor-1], runes[m.noteInputCursor:]...)
			m.noteInput = string(runes)
			m.noteInputCursor--
		}
		return m, nil
	case tea.KeyDelete:
		if editing {
			runes := []rune(m.noteInput)
			if m.noteInputCursor < len(runes) {
				runes = append(runes[:m.noteInputCursor], runes[m.noteInputCursor+1:]...)
				m.noteInput = string(runes)
			}
		}
		return m, nil
	case tea.KeyLeft:
		if editing && m.noteInputCursor > 0 {
			m.noteInputCursor--
		}
		return m, nil
	case tea.KeyRight:
		if editing {
			n := len([]rune(m.noteInput))
			if m.noteInputCursor < n {
				m.noteInputCursor++
			}
		}
		return m, nil
	case tea.KeyHome, tea.KeyCtrlA:
		if editing {
			m.noteInputCursor = 0
		}
		return m, nil
	case tea.KeyEnd, tea.KeyCtrlE:
		if editing {
			m.noteInputCursor = len([]rune(m.noteInput))
		}
		return m, nil
	case tea.KeyUp:
		if m.noteCursor > 0 && !editing {
			m.noteCursor--
		}
		return m, nil
	case tea.KeyDown:
		if m.noteCursor < len(m.noteCands) && !editing {
			m.noteCursor++
		}
		return m, nil
	}

	if !editing {
		switch msg.String() {
		case "j", "down":
			if m.noteCursor < len(m.noteCands) {
				m.noteCursor++
			}
			return m, nil
		case "k", "up":
			if m.noteCursor > 0 {
				m.noteCursor--
			}
			return m, nil
		case "d":
			if m.noteCursor < len(m.noteCands) {
				m.noteDeleteConfirm = true
			}
			return m, nil
		}
	}

	if msg.Type == tea.KeyRunes {
		// Only consume keystrokes as text when the user is plausibly composing:
		// already editing, or sitting on the "+ new note" row.
		if editing || m.noteCursor == len(m.noteCands) {
			runes := []rune(m.noteInput)
			ins := msg.Runes
			out := make([]rune, 0, len(runes)+len(ins))
			out = append(out, runes[:m.noteInputCursor]...)
			out = append(out, ins...)
			out = append(out, runes[m.noteInputCursor:]...)
			m.noteInput = string(out)
			m.noteInputCursor += len(ins)
		}
	}
	return m, nil
}

func (m model) View() string {
	if !m.ready {
		return "\n  Loading..."
	}

	if m.mode == modeHome {
		return homeView(m)
	}

	if m.noteUI == noteUIInner {
		return m.viewWithNotes()
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
	stats := LoadStats(defaultStatsPath)

	m := initial(startTrans, store, stats)
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
