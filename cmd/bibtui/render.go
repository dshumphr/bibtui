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

// renderAnnotations returns display lines for every active annotation that
// touches the given canonical verse reference.  Lines are wrapped to aw
// (annotation-column width) and styled with the group's colour and intensity.
func renderAnnotations(store *AnnotationStore, ref AnnotationRef, aw int) []string {
	if store == nil || aw <= 0 {
		return nil
	}
	anns := store.AtRef(ref)
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
func buildContent(c ref, translations []string, store *AnnotationStore, totalW int, cursorVerse int, noteMode bool) ([]string, map[int]int) {
	n := len(translations)
	if n == 0 {
		return nil, nil
	}

	// Decide whether we need an annotation column.
	hasAnnCol := false
	if store != nil {
		for _, on := range store.ActiveGroups {
			if on {
				hasAnnCol = true
				break
			}
		}
	}
	if noteMode {
		hasAnnCol = true
	}

	// Allocate widths: translation panes + optional annotation pane.
	aw := 0
	divCount := n - 1
	if hasAnnCol {
		aw = clamp(totalW/4, 20, 40)
		divCount = n // divider between last translation and annotation column
	}
	avail := totalW - divCount - aw
	pw := avail / n
	if pw < 20 {
		pw = 20
	}

	div := divSt.Render("│")
	blank := strings.Repeat(" ", pw)
	blankAnn := strings.Repeat(" ", aw)

	// Header rows: blank, translation labels, separator, blank.
	labelParts := make([]string, n)
	sepParts := make([]string, n)
	for i, t := range translations {
		labelParts[i] = padToWidth(labelSt.Render(centerPad(strings.ToUpper(t), pw)), pw)
		sepParts[i] = divSt.Render(strings.Repeat("─", pw))
	}
	blankRow := strings.Join(repN(blank, n), div)
	if hasAnnCol {
		activeNames := []string{}
		for _, name := range sortedGroupNames(store) {
			if store.ActiveGroups[name] {
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
		if hasAnnCol {
			if vnum > 0 {
				annRef := AnnotationRef{Book: c.book.slug, Chapter: c.num, Verse: vnum}
				innerW := aw
				if noteMode {
					innerW = aw - 2
					if innerW < 1 {
						innerW = 1
					}
				}
				rawLines := renderAnnotations(store, annRef, innerW)
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
		}

		for li := 0; li < maxH; li++ {
			parts := make([]string, n)
			for pi := range translations {
				var s string
				if li < len(paneLines[pi]) {
					s = paneLines[pi][li]
				}
				parts[pi] = padToWidth(s, pw)
			}
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
