package main

import (
	"encoding/json"
	"os"
	"time"
)

const defaultSessionPath = "session.json"

type Session struct {
	BookSlug     string          `json:"book"`
	Chapter      int             `json:"chapter"`
	Scroll       int             `json:"scroll"`
	Translations []string        `json:"translations"`
	ActiveGroups map[string]bool `json:"active_groups"`
	SavedGroups  map[string]bool `json:"saved_groups,omitempty"`
	SavedAt      time.Time       `json:"saved_at"`
}

func saveSession(m model) {
	if len(m.index) == 0 || m.mode != modeReader {
		return
	}
	r := m.index[m.pos]
	activeGroups := make(map[string]bool, len(m.activeGroups))
	for k, v := range m.activeGroups {
		activeGroups[k] = v
	}
	var savedGroups map[string]bool
	if m.savedGroups != nil {
		savedGroups = make(map[string]bool, len(m.savedGroups))
		for k, v := range m.savedGroups {
			savedGroups[k] = v
		}
	}
	s := Session{
		BookSlug:     r.book.slug,
		Chapter:      r.num,
		Scroll:       m.scroll,
		Translations: m.translations,
		ActiveGroups: activeGroups,
		SavedGroups:  savedGroups,
		SavedAt:      time.Now(),
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(defaultSessionPath, data, 0644)
}

func loadSession() *Session {
	data, err := os.ReadFile(defaultSessionPath)
	if err != nil {
		return nil
	}
	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil
	}
	return &s
}

// timeAgo returns a human-friendly string for how long ago t was.
func timeAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "1 minute ago"
		}
		return itoa(m) + " minutes ago"
	case d < 24*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1 hour ago"
		}
		return itoa(h) + " hours ago"
	case d < 48*time.Hour:
		return "yesterday"
	case d < 7*24*time.Hour:
		return itoa(int(d.Hours()/24)) + " days ago"
	default:
		return t.Format("Jan 2")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := make([]byte, 0, 10)
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	return string(buf)
}
