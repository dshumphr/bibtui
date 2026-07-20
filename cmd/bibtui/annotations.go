package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	ID        string        `json:"id,omitempty"`     // stable; empty on annotations predating proxenos integration
	Author    string        `json:"author,omitempty"` // "user", or an agent's src (e.g. "agent:main")
	Ref       AnnotationRef `json:"ref"`
	Text      string        `json:"text"`
	Intensity *int          `json:"intensity,omitempty"`
	CreatedAt time.Time     `json:"created_at"`
}

// newAnnotationID returns a short, stable, opaque identifier — not a
// proxenos ULID (that's the event id, a different namespace), just
// enough entropy to be collision-free for one user's annotation store.
func newAnnotationID() string {
	var b [10]byte
	if _, err := rand.Read(b[:]); err == nil {
		return "n" + hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("n%d", time.Now().UnixNano())
}

// ── annotation group ──────────────────────────────────────────────────────

// A named collection of annotations.  Many groups may be visible at once,
// each with its own colour so the user can tell them apart at a glance.
type AnnotationGroup struct {
	Name        string       `json:"name"`
	Color       string       `json:"color"` // lipgloss-compatible hex or name
	Annotations []Annotation `json:"annotations"`
	Generated   bool         `json:"-"`
}

// ── store ─────────────────────────────────────────────────────────────────

const defaultStorePath = "annotations.json"
const exampleStorePath = "annotations.example.json"
const divergenceStorePath = "divergence.json"

type AnnotationStore struct {
	Groups map[string]*AnnotationGroup `json:"groups"`

	path string `json:"-"`
}

func NewAnnotationStore(path string) *AnnotationStore {
	if path == "" {
		path = defaultStorePath
	}
	return &AnnotationStore{
		Groups: make(map[string]*AnnotationGroup),
		path:   path,
	}
}

func (s *AnnotationStore) Load() error {
	data, err := os.ReadFile(s.path)
	usingExample := false
	if os.IsNotExist(err) {
		usingExample = true
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
	if err := json.Unmarshal(data, s); err != nil {
		return err
	}
	// Schema migration: annotations written before the proxenos
	// integration have no id/author. Backfill in memory always; persist
	// only when we loaded the user's real store — the example fallback
	// is transient by design and must not get silently materialized into
	// a real annotations.json.
	if s.backfillIdentity() && !usingExample {
		return s.Save()
	}
	return nil
}

// backfillIdentity assigns a stable id and a default "user" author to any
// annotation that predates those fields. Returns true if anything changed.
func (s *AnnotationStore) backfillIdentity() bool {
	changed := false
	for _, g := range s.Groups {
		for i := range g.Annotations {
			a := &g.Annotations[i]
			if a.ID == "" {
				a.ID = newAnnotationID()
				changed = true
			}
			if a.Author == "" {
				a.Author = "user"
				changed = true
			}
		}
	}
	return changed
}

func (s *AnnotationStore) Save() error {
	saveable := &AnnotationStore{Groups: make(map[string]*AnnotationGroup)}
	for name, g := range s.Groups {
		if !g.Generated {
			saveable.Groups[name] = g
		}
	}
	data, err := json.MarshalIndent(saveable, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0644)
}

func (s *AnnotationStore) LoadGenerated(path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var tmp AnnotationStore
	if err := json.Unmarshal(data, &tmp); err != nil {
		return err
	}
	for name, g := range tmp.Groups {
		g.Generated = true
		s.Groups[name] = g
	}
	return nil
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
	}
}

// AtRef returns every active annotation that touches the given verse,
// grouped by annotation group so the caller can render them with the
// correct colour / intensity.
func (s *AnnotationStore) AtRef(ref AnnotationRef, active map[string]bool) []GroupAnnotation {
	var out []GroupAnnotation
	for name, g := range s.Groups {
		if !active[name] {
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

// EnsureNotesGroup creates the "notes" group if it doesn't exist.
func (s *AnnotationStore) EnsureNotesGroup() {
	if _, ok := s.Groups[notesGroupName]; !ok {
		s.Groups[notesGroupName] = &AnnotationGroup{
			Name:  notesGroupName,
			Color: notesGroupColor,
		}
	}
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

// AddNote creates a new note in the "notes" group, authored by the local
// user. Returns the created annotation so the caller can learn its id.
func (s *AnnotationStore) AddNote(ref AnnotationRef, text string) Annotation {
	s.EnsureNotesGroup()
	a := Annotation{
		ID:        newAnnotationID(),
		Author:    "user",
		Ref:       ref,
		Text:      text,
		CreatedAt: time.Now(),
	}
	s.Add(notesGroupName, a)
	return a
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
}

// ── id-addressed notes (proxenos accepts path) ──────────────────────────
//
// The keyboard note UI addresses notes positionally (ref + index into the
// verse's slice) because that's what a human paging through a list needs.
// An inbound stream command addresses a specific note by its stable id
// instead, and must never touch a note it doesn't own — these three
// methods are the only place that authorship guard lives.

// ErrNoteNotFound means no annotation with the given id exists.
var ErrNoteNotFound = errors.New("no such note")

// ErrNotAuthor means the id exists but belongs to a different author —
// the guard that keeps one src from editing or deleting another's note.
var ErrNotAuthor = errors.New("not authored by this caller")

// FindNoteByID searches every group for the given id (notes always live
// in "notes", but nothing else in the schema assumes that).
func (s *AnnotationStore) FindNoteByID(id string) (*Annotation, string, bool) {
	for name, g := range s.Groups {
		for i := range g.Annotations {
			if g.Annotations[i].ID == id {
				return &g.Annotations[i], name, true
			}
		}
	}
	return nil, "", false
}

// AddNoteWithID creates a note with a caller-chosen stable id in the
// "notes" group. Per the proxenos idempotency convention, resending the
// same id overwrites — but only if the resend comes from the same
// author; a different author reusing an existing id is rejected rather
// than silently hijacking it.
func (s *AnnotationStore) AddNoteWithID(ref AnnotationRef, id, author, text string) error {
	if existing, _, ok := s.FindNoteByID(id); ok {
		if existing.Author != author {
			return ErrNotAuthor
		}
		existing.Ref = ref
		existing.Text = text
		return nil
	}
	s.EnsureNotesGroup()
	s.Add(notesGroupName, Annotation{ID: id, Author: author, Ref: ref, Text: text})
	return nil
}

// UpdateNoteByID edits an existing note's text — only if author matches.
func (s *AnnotationStore) UpdateNoteByID(id, author, text string) error {
	a, _, ok := s.FindNoteByID(id)
	if !ok {
		return ErrNoteNotFound
	}
	if a.Author != author {
		return ErrNotAuthor
	}
	a.Text = text
	return nil
}

// DeleteNoteByID removes an existing note — only if author matches.
func (s *AnnotationStore) DeleteNoteByID(id, author string) error {
	a, group, ok := s.FindNoteByID(id)
	if !ok {
		return ErrNoteNotFound
	}
	if a.Author != author {
		return ErrNotAuthor
	}
	g := s.Groups[group]
	for i := range g.Annotations {
		if g.Annotations[i].ID == id {
			g.Annotations = append(g.Annotations[:i], g.Annotations[i+1:]...)
			break
		}
	}
	return nil
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
