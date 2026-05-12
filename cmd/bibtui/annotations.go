package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"
)

// ── canonical verse reference (translation-agnostic) ────────────────────────

type AnnotationRef struct {
	Book    string `json:"book"`
	Chapter int    `json:"chapter"`
	Verse   int    `json:"verse"`
}

func (r AnnotationRef) String() string {
	return fmt.Sprintf("%s %d:%d", r.Book, r.Chapter, r.Verse)
}

func (r AnnotationRef) Equal(o AnnotationRef) bool {
	return r.Book == o.Book && r.Chapter == o.Chapter && r.Verse == o.Verse
}

// ── annotation ────────────────────────────────────────────────────────────

// Intensity is an optional 1-3 scale.  1= subtle, 2= moderate, 3= strong.
type Annotation struct {
	Ref       AnnotationRef `json:"ref"`
	Text      string        `json:"text"`
	Intensity *int          `json:"intensity,omitempty"`
	CreatedAt time.Time     `json:"created_at"`
}

// ── annotation group ──────────────────────────────────────────────────────

// A named collection of annotations.  Many groups may be visible at once,
// each with its own colour so the user can tell them apart at a glance.
type AnnotationGroup struct {
	Name        string       `json:"name"`
	Color       string       `json:"color"` // lipgloss-compatible hex or name
	Annotations []Annotation `json:"annotations"`
}

// ── store ─────────────────────────────────────────────────────────────────

const defaultStorePath = "annotations.json"
const exampleStorePath = "annotations.example.json"

type AnnotationStore struct {
	Groups       map[string]*AnnotationGroup `json:"groups"`
	ActiveGroups map[string]bool             `json:"active_groups"`

	path string `json:"-"`
}

func NewAnnotationStore(path string) *AnnotationStore {
	if path == "" {
		path = defaultStorePath
	}
	return &AnnotationStore{
		Groups:       make(map[string]*AnnotationGroup),
		ActiveGroups: make(map[string]bool),
		path:         path,
	}
}

func (s *AnnotationStore) Load() error {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		data, err = os.ReadFile(exampleStorePath)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	return json.Unmarshal(data, s)
}

func (s *AnnotationStore) Save() error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0644)
}

// Add creates a new annotation inside the named group.
func (s *AnnotationStore) Add(group string, a Annotation) {
	g, ok := s.Groups[group]
	if !ok {
		g = &AnnotationGroup{Name: group, Color: "#8888CC"}
		s.Groups[group] = g
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now()
	}
	g.Annotations = append(g.Annotations, a)
	s.sortGroup(g)
}

// Delete removes an annotation from a group by ref.  If ref is nil, the
// group itself is deleted.
func (s *AnnotationStore) Delete(group string, ref *AnnotationRef) {
	g, ok := s.Groups[group]
	if !ok {
		return
	}
	if ref == nil {
		delete(s.Groups, group)
		delete(s.ActiveGroups, group)
		return
	}
	filtered := g.Annotations[:0]
	for _, a := range g.Annotations {
		if !a.Ref.Equal(*ref) {
			filtered = append(filtered, a)
		}
	}
	g.Annotations = filtered
	if len(g.Annotations) == 0 {
		delete(s.Groups, group)
		delete(s.ActiveGroups, group)
	}
}

// AtRef returns every active annotation that touches the given verse,
// grouped by annotation group so the caller can render them with the
// correct colour / intensity.
func (s *AnnotationStore) AtRef(ref AnnotationRef) []GroupAnnotation {
	var out []GroupAnnotation
	for name, g := range s.Groups {
		if !s.ActiveGroups[name] {
			continue
		}
		for _, a := range g.Annotations {
			if a.Ref.Equal(ref) {
				out = append(out, GroupAnnotation{
					Group:      *g,
					Annotation: a,
					GroupName:  name,
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].GroupName != out[j].GroupName {
			return out[i].GroupName < out[j].GroupName
		}
		return out[i].Annotation.Text < out[j].Annotation.Text
	})
	return out
}

// ToggleGroup turns a group's visibility on screen on or off.
func (s *AnnotationStore) ToggleGroup(name string) {
	if _, ok := s.Groups[name]; !ok {
		return
	}
	s.ActiveGroups[name] = !s.ActiveGroups[name]
}

func (s *AnnotationStore) sortGroup(g *AnnotationGroup) {
	sort.Slice(g.Annotations, func(i, j int) bool {
		ri, rj := g.Annotations[i].Ref, g.Annotations[j].Ref
		if ri.Book != rj.Book {
			return ri.Book < rj.Book
		}
		if ri.Chapter != rj.Chapter {
			return ri.Chapter < rj.Chapter
		}
		return ri.Verse < rj.Verse
	})
}

// ── notes ─────────────────────────────────────────────────────────────────

const notesGroupName = "notes"
const notesGroupColor = "#66AA66"

// EnsureNotesGroup creates the "notes" group if it doesn't exist and ensures
// it is active so notes are visible.
func (s *AnnotationStore) EnsureNotesGroup() {
	if _, ok := s.Groups[notesGroupName]; !ok {
		s.Groups[notesGroupName] = &AnnotationGroup{
			Name:  notesGroupName,
			Color: notesGroupColor,
		}
	}
	s.ActiveGroups[notesGroupName] = true
}

// NotesAtRef returns all annotations in the "notes" group matching ref.
func (s *AnnotationStore) NotesAtRef(ref AnnotationRef) []Annotation {
	g, ok := s.Groups[notesGroupName]
	if !ok {
		return nil
	}
	var out []Annotation
	for _, a := range g.Annotations {
		if a.Ref.Equal(ref) {
			out = append(out, a)
		}
	}
	return out
}

// AddNote creates a new note in the "notes" group.
func (s *AnnotationStore) AddNote(ref AnnotationRef, text string) {
	s.EnsureNotesGroup()
	s.Add(notesGroupName, Annotation{
		Ref:       ref,
		Text:      text,
		CreatedAt: time.Now(),
	})
}

// UpdateNote updates the note at the given index (among notes matching ref).
func (s *AnnotationStore) UpdateNote(ref AnnotationRef, idx int, text string) {
	g, ok := s.Groups[notesGroupName]
	if !ok {
		return
	}
	var positions []int
	for i, a := range g.Annotations {
		if a.Ref.Equal(ref) {
			positions = append(positions, i)
		}
	}
	if idx < 0 || idx >= len(positions) {
		return
	}
	g.Annotations[positions[idx]].Text = text
}

// DeleteNoteAt deletes the note at the given index (among notes matching ref).
func (s *AnnotationStore) DeleteNoteAt(ref AnnotationRef, idx int) {
	g, ok := s.Groups[notesGroupName]
	if !ok {
		return
	}
	var positions []int
	for i, a := range g.Annotations {
		if a.Ref.Equal(ref) {
			positions = append(positions, i)
		}
	}
	if idx < 0 || idx >= len(positions) {
		return
	}
	pos := positions[idx]
	g.Annotations = append(g.Annotations[:pos], g.Annotations[pos+1:]...)
	if len(g.Annotations) == 0 {
		delete(s.Groups, notesGroupName)
		delete(s.ActiveGroups, notesGroupName)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────

type GroupAnnotation struct {
	Group      AnnotationGroup
	Annotation Annotation
	GroupName  string
}

func sortedGroupNames(store *AnnotationStore) []string {
	if store == nil {
		return nil
	}
	var out []string
	for name := range store.Groups {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
