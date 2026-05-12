package main

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// ── activity heatmap ──────────────────────────────────────────────────────

// Five buckets, GitHub-ish green ramp, adaptive for light terminals.
// The empty bucket uses a noticeably-different shade so the heatmap's footprint
// is visible even before any reading has happened.
var heatStyles = []lipgloss.Style{
	lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#C8C8C8", Dark: "#3A3A3A"}),
	lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#9BD89B", Dark: "#3D6F35"}),
	lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#5FBF5F", Dark: "#5FA84D"}),
	lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#2E9B2E", Dark: "#8FCB6D"}),
	lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#136513", Dark: "#C8EE9A"}),
}

func heatBucket(n int) int {
	switch {
	case n <= 0:
		return 0
	case n == 1:
		return 1
	case n <= 3:
		return 2
	case n <= 6:
		return 3
	default:
		return 4
	}
}

// renderHeatmap returns rows (7 lines) covering the last `weeks` weeks,
// ending today. Days are columns, weekdays are rows (Sun on top).
func renderHeatmap(stats *Stats, weeks int) []string {
	if weeks < 4 {
		weeks = 4
	}
	now := time.Now()
	// Align "today" column: rightmost column is the current week, with today
	// in its weekday row. The grid covers `weeks` columns ending this week.
	startOfThisWeek := now.AddDate(0, 0, -int(now.Weekday()))
	gridStart := startOfThisWeek.AddDate(0, 0, -7*(weeks-1))

	rows := make([]string, 7)
	for wd := 0; wd < 7; wd++ {
		var b strings.Builder
		for w := 0; w < weeks; w++ {
			day := gridStart.AddDate(0, 0, w*7+wd)
			if day.After(now) {
				b.WriteString("  ")
				continue
			}
			n := 0
			if stats != nil {
				n = stats.Days[dayKey(day)]
			}
			b.WriteString(heatStyles[heatBucket(n)].Render("■ "))
		}
		rows[wd] = b.String()
	}
	return rows
}

// ── sparkline of chapter views ────────────────────────────────────────────

var sparkChars = []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

var sparkSt = lipgloss.NewStyle().
	Foreground(lipgloss.AdaptiveColor{Light: "#7C4E00", Dark: "#CBA252"})

var sparkBaseSt = lipgloss.NewStyle().
	Foreground(lipgloss.AdaptiveColor{Light: "#C8C8C8", Dark: "#3A3A3A"})

// renderSparkline returns a single-line bar chart of views per chapter in
// canonical order, downsampled to `width` columns. Anonymous on purpose: no
// chapter labels (they'd never fit nicely). A subtle baseline of dots is
// always drawn so the chart's extent is visible even with zero views.
func renderSparkline(stats *Stats, index []ref, width int) string {
	if width < 8 || len(index) == 0 {
		return ""
	}
	counts := make([]int, len(index))
	if stats != nil {
		for i, r := range index {
			counts[i] = stats.Views[chapterKey(r.book.slug, r.num)]
		}
	}
	// Downsample: pick the max in each bucket. Max preserves "I read this
	// chapter a lot" more honestly than averaging across many zero neighbours.
	buckets := make([]int, width)
	max := 0
	for i := 0; i < width; i++ {
		lo := i * len(counts) / width
		hi := (i + 1) * len(counts) / width
		if hi <= lo {
			hi = lo + 1
		}
		if hi > len(counts) {
			hi = len(counts)
		}
		m := 0
		for j := lo; j < hi; j++ {
			if counts[j] > m {
				m = counts[j]
			}
		}
		buckets[i] = m
		if m > max {
			max = m
		}
	}
	steps := len(sparkChars) - 1
	var b strings.Builder
	for _, v := range buckets {
		if v <= 0 {
			b.WriteString(sparkBaseSt.Render("·"))
			continue
		}
		// Floor at ▃ (index 2) so a single read is visible at a glance,
		// scale up to █ for the most-read bucket.
		idx := 2
		if max > 1 {
			idx = 2 + (v-1)*(steps-2)/(max-1)
		}
		if idx > steps {
			idx = steps
		}
		b.WriteString(sparkSt.Render(string(sparkChars[idx])))
	}
	return b.String()
}
