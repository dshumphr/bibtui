package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	homeTitleSt = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#7C4E00", Dark: "#CBA252"})

	homeSubtleSt = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#9B9B9B", Dark: "#525252"})

	homeKeySt = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#383838", Dark: "#C8C8C8"})
)

func homeView(m model) string {
	if !m.ready {
		return "\n  Loading..."
	}

	title := homeTitleSt.Render("bibtui")

	key := func(k, label string) string {
		return homeKeySt.Render(k) + " " + homeSubtleSt.Render(label)
	}

	var body []string
	if m.session != nil {
		s := m.session
		bookName := s.BookSlug
		for _, b := range books {
			if b.slug == s.BookSlug {
				bookName = b.name
				break
			}
		}
		trans := strings.ToUpper(strings.Join(s.Translations, " · "))
		location := homeKeySt.Render(bookName+" "+itoa(s.Chapter)) +
			homeSubtleSt.Render("  ·  "+trans) +
			"  " + homeSubtleSt.Render(timeAgo(s.SavedAt))

		body = append(body, homeSubtleSt.Render("last read"))
		body = append(body, location)
		body = append(body, "")
		body = append(body,
			key("r", "resume")+"   "+key("n", "new")+"   "+key("g", "goto")+"   "+key("q", "quit"),
		)
	} else {
		body = append(body, homeSubtleSt.Render("no previous session"))
		body = append(body, "")
		body = append(body,
			key("n", "new")+"   "+key("g", "goto")+"   "+key("q", "quit"),
		)
	}

	block := lipgloss.JoinVertical(lipgloss.Center,
		append([]string{title, ""}, body...)...,
	)

	// ── stats panel ────────────────────────────────────────────────────────
	// Stacked below the resume block. Sized to terminal width.
	statsBlock := renderHomeStats(m)
	if statsBlock != "" {
		block = lipgloss.JoinVertical(lipgloss.Center, block, "", statsBlock)
	}

	if !m.gotoOpen {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, block)
	}

	// ── goto overlay ──────────────────────────────────────────────────────

	bookQuery, chapter, verse := parseGotoQuery(m.gotoInput)

	maxCands := 6
	n := len(m.gotoCands)
	if n > maxCands {
		n = maxCands
	}
	popupLines := 0
	if bookQuery != "" && n > 0 {
		popupLines = n + 1 // separator + candidates
	}

	contentH := m.height - popupLines - 1
	if contentH < 1 {
		contentH = 1
	}

	homeContent := lipgloss.Place(m.width, contentH, lipgloss.Center, lipgloss.Center, block)

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

	cmdBar := helpSt.Render(
		padToWidth("  "+pickerCursorSt.Render(":")+" "+m.gotoInput+"█"+"    tab complete  ·  ↑/↓ navigate  ·  esc cancel", m.width),
	)

	return homeContent + "\n" + popup + cmdBar
}

// renderHomeStats produces the activity heatmap, chapter-view sparkline, and
// summary indicators. Sized to the current terminal width. Returns "" if
// there's nothing to show or no room to show it.
func renderHomeStats(m model) string {
	if m.width < 30 || m.height < 16 {
		return ""
	}

	// Pick a panel width that's nice and centered, capped so the heatmap
	// doesn't sprawl on very wide terminals.
	panelW := m.width - 8
	if panelW > 120 {
		panelW = 120
	}
	if panelW < 24 {
		panelW = 24
	}

	// Heatmap: each day cell is 2 chars wide.
	weeks := panelW / 2
	if weeks > 53 {
		weeks = 53
	}
	if weeks < 8 {
		weeks = 8
	}
	heat := renderHeatmap(m.stats, weeks)
	heatW := weeks * 2

	// Sparkline at panel width (single line, 1 char per bucket).
	spark := renderSparkline(m.stats, m.index, panelW)

	// Indicators.
	notes := totalNotes(m.store)
	viewed := 0
	total := len(m.index)
	if m.stats != nil {
		viewed = m.stats.ChaptersViewed(m.index)
	}
	pct := 0
	if total > 0 {
		pct = viewed * 100 / total
	}
	noteWord := "notes"
	if notes == 1 {
		noteWord = "note"
	}
	indicators := homeKeySt.Render(itoa(notes)) + homeSubtleSt.Render(" "+noteWord) +
		homeSubtleSt.Render("   ·   ") +
		homeKeySt.Render(itoa(pct)+"%") + homeSubtleSt.Render(" read ") +
		homeSubtleSt.Render("("+itoa(viewed)+"/"+itoa(total)+")")

	var rows []string
	rows = append(rows, homeSubtleSt.Render(centerPad("activity", heatW)))
	rows = append(rows, heat...)
	rows = append(rows, "")
	rows = append(rows, homeSubtleSt.Render(centerPad("chapter views", panelW)))
	rows = append(rows, spark)
	rows = append(rows, "")
	rows = append(rows, indicators)

	return lipgloss.JoinVertical(lipgloss.Center, rows...)
}
