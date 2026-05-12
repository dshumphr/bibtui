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
