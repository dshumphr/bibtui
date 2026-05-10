package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

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

func repN(s string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s
	}
	return out
}
