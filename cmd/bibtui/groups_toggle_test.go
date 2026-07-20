package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func panelTestModel() model {
	store := NewAnnotationStore("")
	store.Groups = map[string]*AnnotationGroup{
		"crossrefs": {Name: "crossrefs"},
		"notes":     {Name: "notes"},
	}
	return model{
		mode:                modeReader,
		index:               []ref{{book: book{slug: "genesis", name: "Genesis"}, num: 1}},
		translations:        []string{"kjv"},
		allTrans:            []string{"kjv"},
		store:               store,
		activeGroups:        map[string]bool{"crossrefs": false, "notes": true},
		annotationPanelOpen: true,
	}
}

func assertGroupPreferences(t *testing.T, m model) {
	t.Helper()
	if !m.activeGroups["notes"] || m.activeGroups["crossrefs"] {
		t.Fatalf("group preferences changed: %#v", m.activeGroups)
	}
}

func TestPanelTogglePreservesPreferencesRepeatedly(t *testing.T) {
	m := panelTestModel()
	for cycle := range 3 {
		m = m.withPanelToggled()
		if m.annotationPanelOpen {
			t.Fatalf("cycle %d: panel did not hide", cycle)
		}
		assertGroupPreferences(t, m)

		m = m.withPanelToggled()
		if !m.annotationPanelOpen {
			t.Fatalf("cycle %d: panel did not reopen", cycle)
		}
		assertGroupPreferences(t, m)
	}
}

func TestPanelVisibilitySurvivesSessionResume(t *testing.T) {
	t.Chdir(t.TempDir())
	hidden := panelTestModel()
	hidden.annotationPanelOpen = false
	saveSession(hidden)

	session := loadSession()
	if session == nil || session.AnnotationPanelOpen == nil || *session.AnnotationPanelOpen {
		t.Fatalf("saved session lost hidden-panel state: %#v", session)
	}
	resumed := panelTestModel().applySession(session)
	if resumed.annotationPanelOpen {
		t.Fatal("resumed session reopened a hidden panel")
	}
	assertGroupPreferences(t, resumed)

	resumed = resumed.withPanelToggled()
	if !resumed.annotationPanelOpen {
		t.Fatal("A did not reopen the resumed hidden panel")
	}
	assertGroupPreferences(t, resumed)
}

func TestLegacyCollapsedSessionMigratesPreferences(t *testing.T) {
	legacy := &Session{
		ActiveGroups: map[string]bool{"crossrefs": false, "notes": false},
		SavedGroups:  map[string]bool{"crossrefs": false, "notes": true},
	}
	m := panelTestModel().applySession(legacy)
	if m.annotationPanelOpen {
		t.Fatal("legacy collapsed session did not migrate to a hidden panel")
	}
	assertGroupPreferences(t, m)
}

func TestPanelToggleWorksInOuterNoteMode(t *testing.T) {
	m := panelTestModel()
	m.noteUI = noteUIOuter
	m.noteCursorVerse = 1
	m.verseMap = map[int]int{1: 0}

	updated, _ := m.updateOuterNote(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	got := updated.(model)
	if got.annotationPanelOpen {
		t.Fatal("A in outer note mode did not hide the panel")
	}
	assertGroupPreferences(t, got)
}

func TestPanelTogglePreservesTopVerseAndUsesPreferredGroupBreadcrumbs(t *testing.T) {
	t.Chdir(t.TempDir())
	chapterDir := filepath.Join("bibles", "kjv", "genesis")
	if err := os.MkdirAll(chapterDir, 0755); err != nil {
		t.Fatal(err)
	}
	chapter := "**1** First verse with enough words to wrap in a narrow reading pane.\n" +
		"**2** Second verse should remain anchored at the top.\n"
	if err := os.WriteFile(filepath.Join(chapterDir, "1.md"), []byte(chapter), 0644); err != nil {
		t.Fatal(err)
	}

	store := NewAnnotationStore("")
	store.Groups = map[string]*AnnotationGroup{
		"notes": {
			Name:  "notes",
			Color: "#66AA66",
			Annotations: []Annotation{{
				ID:   "note-1",
				Ref:  AnnotationRef{Book: "genesis", Chapter: 1, Verse: 1},
				Text: strings.Repeat("long annotation text ", 12),
			}},
		},
	}
	m := model{
		mode:                modeReader,
		ready:               true,
		width:               80,
		height:              4,
		index:               []ref{{book: book{slug: "genesis", name: "Genesis"}, num: 1}},
		translations:        []string{"kjv"},
		store:               store,
		activeGroups:        map[string]bool{"notes": true},
		annotationPanelOpen: true,
	}
	m = m.withContent()
	openVerse2Offset := m.verseMap[2]
	m.scroll = openVerse2Offset

	m = m.withPanelToggled()
	if got := m.activeRef().Verse; got != 2 {
		t.Fatalf("hide moved the top verse: got %d, want 2", got)
	}
	if m.verseMap[2] == openVerse2Offset {
		t.Fatal("test setup did not change verse height across the toggle")
	}
	verse1Line := m.lines[m.verseMap[1]]
	if !strings.HasSuffix(verse1Line, "│•") {
		t.Fatalf("selected-group breadcrumb is not in the far-right gutter: %q", verse1Line)
	}

	m.activeGroups["notes"] = false
	m = m.withContentAnchored()
	if strings.Contains(m.lines[m.verseMap[1]], "•") {
		t.Fatal("deselected group still produced a breadcrumb")
	}

	m.activeGroups["notes"] = true
	m = m.withContentAnchored()
	m = m.withPanelToggled()
	if got := m.activeRef().Verse; got != 2 {
		t.Fatalf("show moved the top verse: got %d, want 2", got)
	}
	assertGroupPreferences(t, m)
}

func TestPanelSetDoesNotChangeGroupPreferences(t *testing.T) {
	m := panelTestModel().applyPanelSet(proxPanelSetMsg{Open: false})
	if m.annotationPanelOpen {
		t.Fatal("panel.set false did not hide panel")
	}
	assertGroupPreferences(t, m)
}
