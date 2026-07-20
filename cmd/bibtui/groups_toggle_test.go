package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func groupsToggleTestModel() model {
	store := NewAnnotationStore("")
	store.Groups = map[string]*AnnotationGroup{
		"crossrefs": {Name: "crossrefs"},
		"notes":     {Name: "notes"},
	}
	return model{
		mode:         modeReader,
		index:        []ref{{book: book{slug: "genesis", name: "Genesis"}, num: 1}},
		translations: []string{"kjv"},
		allTrans:     []string{"kjv"},
		store:        store,
		activeGroups: map[string]bool{"crossrefs": false, "notes": true},
	}
}

func TestGroupsToggleRepeatedCycles(t *testing.T) {
	m := groupsToggleTestModel()

	m = m.withGroupsToggled() // collapse
	if m.activeGroups["notes"] || m.activeGroups["crossrefs"] {
		t.Fatalf("collapse left a group active: %#v", m.activeGroups)
	}

	m = m.withGroupsToggled() // restore
	if !m.activeGroups["notes"] || m.activeGroups["crossrefs"] {
		t.Fatalf("restore did not recover the exact prior set: %#v", m.activeGroups)
	}
	if m.savedGroups != nil {
		t.Fatalf("restore left stale saved groups: %#v", m.savedGroups)
	}

	m = m.withGroupsToggled() // collapse again — the old groupsClosed bug died here
	m = m.withGroupsToggled() // restore again
	if !m.activeGroups["notes"] || m.activeGroups["crossrefs"] {
		t.Fatalf("second toggle cycle did not restore the exact prior set: %#v", m.activeGroups)
	}
}

func TestCollapsedGroupsSurviveSessionResume(t *testing.T) {
	t.Chdir(t.TempDir())

	collapsed := groupsToggleTestModel()
	collapsed.activeGroups = map[string]bool{"crossrefs": false, "notes": false}
	collapsed.savedGroups = map[string]bool{"crossrefs": false, "notes": true}
	saveSession(collapsed)

	session := loadSession()
	if session == nil {
		t.Fatal("saved session did not reload")
	}
	if !session.SavedGroups["notes"] {
		t.Fatalf("saved session lost collapsed-panel restore state: %#v", session.SavedGroups)
	}

	resumed := groupsToggleTestModel().applySession(session)
	resumed = resumed.withGroupsToggled()
	if !resumed.activeGroups["notes"] || resumed.activeGroups["crossrefs"] {
		t.Fatalf("A after resume did not restore the pre-collapse set: %#v", resumed.activeGroups)
	}
}

func TestGroupsToggleWorksInOuterNoteMode(t *testing.T) {
	m := groupsToggleTestModel()
	m.noteUI = noteUIOuter
	m.noteCursorVerse = 1
	m.verseMap = map[int]int{1: 0}

	updated, _ := m.updateOuterNote(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	got := updated.(model)
	if got.activeGroups["notes"] || got.activeGroups["crossrefs"] {
		t.Fatalf("A in outer note mode did not collapse groups: %#v", got.activeGroups)
	}
	if !got.savedGroups["notes"] {
		t.Fatalf("A in outer note mode did not remember the active set: %#v", got.savedGroups)
	}
}

func TestPanelTogglePreservesTopVerseAndUsesRightGutter(t *testing.T) {
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
		mode:         modeReader,
		ready:        true,
		width:        80,
		height:       4,
		index:        []ref{{book: book{slug: "genesis", name: "Genesis"}, num: 1}},
		translations: []string{"kjv"},
		store:        store,
		activeGroups: map[string]bool{"notes": true},
	}
	m = m.withContent()
	openVerse2Offset := m.verseMap[2]
	m.scroll = openVerse2Offset

	m = m.withGroupsToggled() // close the tall annotation column
	if got := m.activeRef().Verse; got != 2 {
		t.Fatalf("collapse moved the top verse: got %d, want 2", got)
	}
	if m.verseMap[2] == openVerse2Offset {
		t.Fatal("test setup did not change verse height across the toggle")
	}
	verse1Line := m.lines[m.verseMap[1]]
	if !strings.HasSuffix(verse1Line, "│•") {
		t.Fatalf("breadcrumb is not in the far-right gutter: %q", verse1Line)
	}

	m = m.withGroupsToggled() // restore the column
	if got := m.activeRef().Verse; got != 2 {
		t.Fatalf("restore moved the top verse: got %d, want 2", got)
	}
}
