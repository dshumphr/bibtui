package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

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

	noteCursorSt = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#7C4E00", Dark: "#CBA252"})

	noteAddSt = lipgloss.NewStyle().
			Faint(true).
			Italic(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#888888", Dark: "#666666"})

	inputCursorSt = lipgloss.NewStyle().Reverse(true)

	breadcrumbMultiSt = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.AdaptiveColor{Light: "#888888", Dark: "#999999"})
)

// ── content rendering ─────────────────────────────────────────────────────

const (
	leftMargin = 2
	numCols    = 4 // "NNN " — fits up to 3-digit verse numbers
	markerColW = 1 // breadcrumb gutter: always at the far right, at the
	// same horizontal position the annotation column's left edge sits at
	// when it's open — "look over there," not "look at the verse number"
)

// bookBanner returns the lines for the "THE BOOK OF X" header shown above
// chapter 1.
func bookBanner(bookName string, width int) []string {
	if width <= 0 {
		width = 1
	}
	title := strings.ToUpper("The Book of " + bookName)
	rule := divSt.Render(strings.Repeat("━", width))
	return []string{
		"",
		rule,
		padToWidth(labelSt.Render(centerPad(title, width)), width),
		rule,
		"",
	}
}

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

// breadcrumbMarker returns a styled "•" for a verse carrying an
// annotation the side column isn't currently showing — colored in the
// touching group's color when exactly one group touches this verse, or
// a neutral color when more than one does (picking one color among
// several would misrepresent what's there). Returns "" when nothing is
// hidden at ref, so the caller's layout doesn't shift.
func breadcrumbMarker(store *AnnotationStore, hidden map[string]bool, ref AnnotationRef) string {
	if store == nil || len(hidden) == 0 {
		return ""
	}
	anns := store.AtRef(ref, hidden)
	if len(anns) == 0 {
		return ""
	}
	colors := map[string]bool{}
	for _, a := range anns {
		colors[a.Group.Color] = true
	}
	if len(colors) == 1 {
		for c := range colors {
			return lipgloss.NewStyle().Foreground(lipgloss.Color(c)).Render("•")
		}
	}
	return breadcrumbMultiSt.Render("•")
}

// renderAnnotations returns display lines for every active annotation that
// touches the given canonical verse reference.  Lines are wrapped to aw
// (annotation-column width) and styled with the group's colour and intensity.
func renderAnnotations(store *AnnotationStore, active map[string]bool, ref AnnotationRef, aw int) []string {
	if store == nil || aw <= 0 {
		return nil
	}
	anns := store.AtRef(ref, active)
	if len(anns) == 0 {
		return nil
	}
	var lines []string
	for _, ga := range anns {
		prefix := fmt.Sprintf("[%s] ", ga.GroupName)
		style := lipgloss.NewStyle().Foreground(lipgloss.Color(ga.Group.Color))
		if ga.GroupName == notesGroupName {
			style = style.Italic(true)
		}
		if ga.Annotation.Intensity != nil {
			switch *ga.Annotation.Intensity {
			case 1:
				style = style.Faint(true)
			case 2:
				// normal weight
			case 3:
				style = style.Bold(true)
			}
		}
		text := prefix + ga.Annotation.Text
		wrapped := wordWrap(text, aw)
		for _, wl := range wrapped {
			lines = append(lines, padToWidth(style.Render(wl), aw))
		}
	}
	return lines
}

// buildContent renders a chapter for all active translations into a slice of
// display lines.  When active annotation groups exist an extra right-hand
// column is reserved so that annotations can be shown aligned with the verses
// they annotate.  The returned map records the starting line offset for each
// verse number (used for verse-level scrolling).
func buildContent(c ref, translations []string, store *AnnotationStore, active map[string]bool, totalW int, cursorVerse int, noteMode bool) ([]string, map[int]int) {
	n := len(translations)
	if n == 0 {
		return nil, nil
	}

	// Decide whether we need an annotation column.
	hasAnnCol := false
	if store != nil {
		for _, on := range active {
			if on {
				hasAnnCol = true
				break
			}
		}
	}
	if noteMode {
		hasAnnCol = true
	}

	// Groups whose annotations aren't already visible in the side column
	// — every group if the column is closed, only the inactive ones if
	// it's open — computed once and reused per verse by breadcrumbMarker.
	hiddenGroups := map[string]bool{}
	if store != nil {
		for name := range store.Groups {
			if hasAnnCol && active[name] {
				continue
			}
			hiddenGroups[name] = true
		}
	}

	// Allocate widths: translation panes + a slim always-present marker
	// gutter (breadcrumbs live here now, not inline with the verse
	// number) + the optional wide annotation pane.
	aw := 0
	divCount := n // n-1 between panes + 1 before the marker gutter
	if hasAnnCol {
		aw = clamp(totalW/4, 20, 40)
		divCount = n + 1 // + 1 more before the annotation column
	}
	avail := totalW - divCount - aw - markerColW
	pw := avail / n
	if pw < 20 {
		pw = 20
	}

	div := divSt.Render("│")
	blank := strings.Repeat(" ", pw)
	blankAnn := strings.Repeat(" ", aw)
	blankMarker := strings.Repeat(" ", markerColW)

	// Header rows: blank, translation labels, separator, blank.
	labelParts := make([]string, n)
	sepParts := make([]string, n)
	for i, t := range translations {
		labelParts[i] = padToWidth(labelSt.Render(centerPad(strings.ToUpper(t), pw)), pw)
		sepParts[i] = divSt.Render(strings.Repeat("─", pw))
	}
	labelParts = append(labelParts, padToWidth("", markerColW))
	sepParts = append(sepParts, divSt.Render(strings.Repeat("─", markerColW)))
	blankRow := strings.Join(repN(blank, n), div) + div + blankMarker
	if hasAnnCol {
		activeNames := []string{}
		for _, name := range sortedGroupNames(store) {
			if active[name] {
				activeNames = append(activeNames, name)
			}
		}
		labelParts = append(labelParts, padToWidth(
			labelSt.Render(centerPad(strings.ToUpper(strings.Join(activeNames, ",")), aw)), aw))
		sepParts = append(sepParts, divSt.Render(strings.Repeat("─", aw)))
		blankRow += div + blankAnn
	}

	result := []string{
		"",
		strings.Join(labelParts, divSt.Render("│")),
		strings.Join(sepParts, divSt.Render("┼")),
		"",
	}

	// Book-title banner at the start of chapter 1.
	if c.num == 1 {
		banner := bookBanner(c.book.name, totalW)
		result = append(banner, result...)
	}

	verseMap := make(map[int]int)

	// Parse each translation's verses.
	allVerses := make([][]verse, n)
	maxV := 0
	for i, t := range translations {
		allVerses[i] = parseChapter(t, c.book.slug, c.num)
		if len(allVerses[i]) > maxV {
			maxV = len(allVerses[i])
		}
	}

	// For each verse slot, render all panes plus optional annotations.
	for vi := 0; vi < maxV; vi++ {
		lineOffset := len(result)

		// Determine this verse-slot's canonical number first — needed
		// before rendering any pane, since the breadcrumb marker (and
		// verseMap) don't depend on which translation we're looking at.
		vnum := 0
		for pi := 0; pi < n; pi++ {
			if vi < len(allVerses[pi]) {
				vnum = allVerses[pi][vi].num
				break
			}
		}
		if vnum > 0 {
			verseMap[vnum] = lineOffset
		}

		var annRef AnnotationRef
		marker := ""
		if vnum > 0 {
			annRef = AnnotationRef{Book: c.book.slug, Chapter: c.num, Verse: vnum}
			marker = breadcrumbMarker(store, hiddenGroups, annRef)
		}

		paneLines := make([][]string, n)
		maxH := 0
		for pi := range translations {
			if vi < len(allVerses[pi]) {
				paneLines[pi] = renderVerseLines(allVerses[pi][vi], pw)
			}
			if len(paneLines[pi]) > maxH {
				maxH = len(paneLines[pi])
			}
		}

		var annLines []string
		if hasAnnCol && vnum > 0 {
			innerW := aw
			if noteMode {
				innerW = aw - 2
				if innerW < 1 {
					innerW = 1
				}
			}
			rawLines := renderAnnotations(store, active, annRef, innerW)
			if noteMode && len(rawLines) == 0 {
				rawLines = []string{padToWidth(noteAddSt.Render("+ add note"), innerW)}
			}
			if noteMode {
				annLines = make([]string, len(rawLines))
				for i, l := range rawLines {
					gutter := "  "
					if vnum == cursorVerse && i == 0 {
						gutter = noteCursorSt.Render("▸ ")
					}
					annLines[i] = gutter + l
				}
			} else {
				annLines = rawLines
			}
			if len(annLines) > maxH {
				maxH = len(annLines)
			}
		}

		for li := 0; li < maxH; li++ {
			parts := make([]string, 0, n+2)
			for pi := range translations {
				var s string
				if li < len(paneLines[pi]) {
					s = paneLines[pi][li]
				}
				parts = append(parts, padToWidth(s, pw))
			}
			markerCell := " "
			if li == 0 && marker != "" {
				markerCell = marker
			}
			parts = append(parts, padToWidth(markerCell, markerColW))
			if hasAnnCol {
				var s string
				if li < len(annLines) {
					s = annLines[li]
				}
				parts = append(parts, padToWidth(s, aw))
			}
			result = append(result, strings.Join(parts, div))
		}
		result = append(result, blankRow)
	}

	return result, verseMap
}
