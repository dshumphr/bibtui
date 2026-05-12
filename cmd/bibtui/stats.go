package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

const defaultStatsPath = "stats.json"

// Stats tracks reading activity. Per-chapter view counts and per-day view
// counts are kept independently so the home screen can show both an activity
// heatmap and a "reading shape" sparkline without recomputation.
type Stats struct {
	Views map[string]int `json:"views"` // key: "<book-slug>/<chapter>"
	Days  map[string]int `json:"days"`  // key: "YYYY-MM-DD" (local time)

	path string `json:"-"`
}

func chapterKey(slug string, num int) string {
	return fmt.Sprintf("%s/%d", slug, num)
}

func dayKey(t time.Time) string {
	return t.Format("2006-01-02")
}

func NewStats(path string) *Stats {
	if path == "" {
		path = defaultStatsPath
	}
	return &Stats{
		Views: make(map[string]int),
		Days:  make(map[string]int),
		path:  path,
	}
}

func LoadStats(path string) *Stats {
	s := NewStats(path)
	data, err := os.ReadFile(s.path)
	if err != nil {
		return s
	}
	_ = json.Unmarshal(data, s)
	if s.Views == nil {
		s.Views = make(map[string]int)
	}
	if s.Days == nil {
		s.Days = make(map[string]int)
	}
	return s
}

func (s *Stats) Save() {
	if s == nil {
		return
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(s.path, data, 0644)
}

// RecordView marks a chapter as viewed once, "now".
func (s *Stats) RecordView(slug string, num int) {
	if s == nil {
		return
	}
	s.Views[chapterKey(slug, num)]++
	s.Days[dayKey(time.Now())]++
	s.Save()
}

// ChaptersViewed returns how many distinct chapters in the given index have
// been viewed at least once.
func (s *Stats) ChaptersViewed(index []ref) int {
	if s == nil {
		return 0
	}
	n := 0
	for _, r := range index {
		if s.Views[chapterKey(r.book.slug, r.num)] > 0 {
			n++
		}
	}
	return n
}

// TotalNotes counts how many notes live in the store.
func totalNotes(store *AnnotationStore) int {
	if store == nil {
		return 0
	}
	g, ok := store.Groups[notesGroupName]
	if !ok {
		return 0
	}
	return len(g.Annotations)
}
