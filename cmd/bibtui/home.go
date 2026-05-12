package main

import (
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

	homeDimSt = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#CCCCCC", Dark: "#444444"})
)

func homeView(m model) string {
	if !m.ready {
		return "\n  Loading..."
	}

	title := homeTitleSt.Render("bibtui")

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
			homeKeySt.Render("r")+" "+homeSubtleSt.Render("resume")+"   "+
				homeKeySt.Render("n")+" "+homeSubtleSt.Render("new")+"   "+
				homeKeySt.Render("q")+" "+homeSubtleSt.Render("quit"),
		)
	} else {
		body = append(body, homeSubtleSt.Render("no previous session"))
		body = append(body, "")
		body = append(body,
			homeKeySt.Render("n")+" "+homeSubtleSt.Render("new session")+"   "+
				homeKeySt.Render("q")+" "+homeSubtleSt.Render("quit"),
		)
	}

	block := lipgloss.JoinVertical(lipgloss.Center,
		append([]string{title, ""}, body...)...,
	)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, block)
}
