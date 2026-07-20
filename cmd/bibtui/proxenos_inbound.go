package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// ── inbound: the accepts half of the contract ────────────────────────────
//
// bibtui reads the stream file directly rather than shelling to
// `proxenos tail -f`: the NDJSON framing is documented and stable ("the
// log is the API" — proxenos PLAN.md §2.1/§2.2), and a direct read means
// no long-lived subprocess to manage through every bubbletea exit path.
// Outbound still goes through the CLI (proxenos.go) — that direction
// benefits from id-stamping and validation the CLI gives for free;
// this direction benefits from not needing any of that.

const proxPollInterval = 250 * time.Millisecond

// proxEvent mirrors proxenos's wire envelope (docs/proxenos-contract.md;
// proxenos PLAN.md §2.1).
type proxEvent struct {
	V       int             `json:"v"`
	ID      string          `json:"id"`
	TS      string          `json:"ts"`
	Src     string          `json:"src"`
	Type    string          `json:"type"`
	Body    json.RawMessage `json:"body"`
	ReplyTo string          `json:"reply_to,omitempty"`
	To      []string        `json:"to,omitempty"`
}

// ── cursor persistence ───────────────────────────────────────────────────
//
// Full-history replay on every launch is not safe here: nav.goto /
// translations.set / groups.set are "apply now" commands, and
// resurrecting an old one after the user has since navigated elsewhere
// on their own would fight the user on every restart. So bibtui persists
// the id of the last event it has scanned past and, on (re)attach, skips
// to it before applying anything — the same role byte-offset cursors play
// in proxenos's own bridge state, translated to an id because the CLI
// surface doesn't expose byte offsets.

const proxCursorPath = "proxenos-state.json"

type proxCursor struct {
	LastSeenID string `json:"last_seen_id"`
}

func loadProxCursor() proxCursor {
	data, err := os.ReadFile(proxCursorPath)
	if err != nil {
		return proxCursor{}
	}
	var c proxCursor
	if json.Unmarshal(data, &c) != nil {
		return proxCursor{}
	}
	return c
}

func saveProxCursor(c proxCursor) {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(proxCursorPath, data, 0644)
}

// ── wire framing ─────────────────────────────────────────────────────────

// parseProxRecords splits a byte range on the leading-"\n" record
// framing and decodes each segment. A snapshot read up to the current
// end-of-file never splits a record that's still being written: proxenos
// guarantees one write(2) per event under O_APPEND (plan §2.3), so any
// bytes a stat()-bounded read observes belong to already-completed
// writes. Malformed segments are logged and skipped, never fatal — the
// same tolerance proxenos's own `tail` has for a torn line.
func parseProxRecords(data []byte) []proxEvent {
	var out []proxEvent
	for _, seg := range bytes.Split(data, []byte("\n")) {
		seg = bytes.TrimSpace(seg)
		if len(seg) == 0 {
			continue
		}
		var ev proxEvent
		if err := json.Unmarshal(seg, &ev); err != nil {
			proxLogf("inbound: skipping malformed record: %v", err)
			continue
		}
		out = append(out, ev)
	}
	return out
}

// ── poll loop ────────────────────────────────────────────────────────────

// startProxPoll launches the inbound consumer for the life of the
// process. There is no explicit shutdown: process exit is the only
// teardown, which is fine — the cursor is persisted after every tick
// that applied something, never deferred to exit.
func startProxPoll(stream, path string, send func(tea.Msg)) {
	if stream == "" || path == "" {
		return
	}
	go runProxPoll(stream, path, send)
}

func runProxPoll(stream, path string, send func(tea.Msg)) {
	cursor := loadProxCursor()

	data, err := os.ReadFile(path)
	if err != nil {
		proxLogf("inbound: initial read of %s failed: %v", path, err)
		return
	}
	records := parseProxRecords(data)
	applyProxCatchup(stream, records, &cursor, send)
	saveProxCursor(cursor)

	readOffset := int64(len(data))
	var curDev, curIno uint64
	if st, err := os.Stat(path); err == nil {
		curDev, curIno = statDevIno(st)
	}

	ticker := time.NewTicker(proxPollInterval)
	defer ticker.Stop()
	for range ticker.C {
		st, err := os.Stat(path)
		if err != nil {
			continue // stream momentarily gone — try again next tick
		}
		dev, ino := statDevIno(st)
		if dev != curDev || ino != curIno {
			// Replaced or compacted. Re-baseline from the top with the
			// same cursor-skip rule: a compacted stream's old ids won't
			// be found, so this naturally falls through to "start fresh".
			proxLogf("inbound: stream replaced; re-baselining")
			curDev, curIno = dev, ino
			data, err = os.ReadFile(path)
			if err != nil {
				continue
			}
			applyProxCatchup(stream, parseProxRecords(data), &cursor, send)
			saveProxCursor(cursor)
			readOffset = int64(len(data))
			continue
		}
		if st.Size() <= readOffset {
			continue
		}
		buf, err := readRange(path, readOffset, st.Size())
		if err != nil {
			continue
		}
		applied := false
		for _, r := range parseProxRecords(buf) {
			applyProxRecord(stream, r, send)
			cursor.LastSeenID = r.ID
			applied = true
		}
		readOffset = st.Size()
		if applied {
			saveProxCursor(cursor)
		}
	}
}

// applyProxCatchup skips forward through records to the last-applied id
// (or, if not found — fresh stream or a generation change — skips all of
// them, i.e. baselines at "now") and applies everything after.
func applyProxCatchup(stream string, records []proxEvent, cursor *proxCursor, send func(tea.Msg)) {
	startIdx := len(records)
	if cursor.LastSeenID != "" {
		for i, r := range records {
			if r.ID == cursor.LastSeenID {
				startIdx = i + 1
				break
			}
		}
	}
	for _, r := range records[startIdx:] {
		applyProxRecord(stream, r, send)
		cursor.LastSeenID = r.ID
	}
}

func statDevIno(st os.FileInfo) (uint64, uint64) {
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		return uint64(sys.Dev), uint64(sys.Ino)
	}
	return 0, 0
}

func readRange(path string, from, to int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, to-from)
	if _, err := f.ReadAt(buf, from); err != nil {
		return nil, err
	}
	return buf, nil
}

// applyProxRecord decodes one record into an accepted action and either
// dispatches it (as a tea.Msg, applied by Update on the bubbletea
// goroutine — never here, never touching model state from this
// goroutine) or rejects it. Structural validation (missing/malformed
// fields) happens here; validation that needs live app state (book
// resolution, note authorship, valid translation codes) happens in
// Update's apply* handlers, which have the current model in scope.
func applyProxRecord(stream string, r proxEvent, send func(tea.Msg)) {
	if r.Src == proxAppSrc {
		return // ignore our own events echoing back
	}
	msg, reason := decodeProxAction(r)
	if reason != "" {
		sendProxEvent(stream, "action.rejected", map[string]any{
			"action": r.Type,
			"reason": reason,
		})
		return
	}
	if msg != nil {
		send(msg)
	}
	// msg == nil, reason == "" → not an accepted type (stream.meta,
	// stream.contract, or a future/unknown type); ignore silently.
}

// ── message types (one per accepted action) ─────────────────────────────

type proxNavGotoMsg struct {
	Book    string
	Chapter int
	Verse   int
}

type proxNavStepMsg struct{ Direction string }

type proxTranslationsSetMsg struct{ Translations []string }

type proxGroupsSetMsg struct{ Active map[string]bool }

type proxNoteCreateMsg struct {
	ID     string
	Author string
	Ref    AnnotationRef
	Text   string
}

type proxNoteUpdateMsg struct {
	ID     string
	Author string
	Text   string
}

type proxNoteDeleteMsg struct {
	ID     string
	Author string
}

type proxAnswerMsg struct {
	Text    string
	ReplyTo string
}

type proxAnswerTimeoutMsg struct{ QID string }

// ── decode: wire body → message, or a rejection reason ──────────────────

func decodeProxAction(r proxEvent) (tea.Msg, string) {
	switch r.Type {
	case "nav.goto":
		var body struct {
			Book    string `json:"book"`
			Chapter int    `json:"chapter"`
			Verse   int    `json:"verse"`
		}
		if err := json.Unmarshal(r.Body, &body); err != nil || body.Book == "" {
			return nil, "malformed body"
		}
		return proxNavGotoMsg{Book: body.Book, Chapter: body.Chapter, Verse: body.Verse}, ""

	case "nav.step":
		var body struct {
			Direction string `json:"direction"`
		}
		if err := json.Unmarshal(r.Body, &body); err != nil {
			return nil, "malformed body"
		}
		if body.Direction != "next" && body.Direction != "prev" {
			return nil, "invalid direction"
		}
		return proxNavStepMsg{Direction: body.Direction}, ""

	case "translations.set":
		var body struct {
			Translations []string `json:"translations"`
		}
		if err := json.Unmarshal(r.Body, &body); err != nil || len(body.Translations) == 0 {
			return nil, "malformed body"
		}
		return proxTranslationsSetMsg{Translations: body.Translations}, ""

	case "groups.set":
		var body struct {
			Active map[string]bool `json:"active"`
		}
		if err := json.Unmarshal(r.Body, &body); err != nil {
			return nil, "malformed body"
		}
		return proxGroupsSetMsg{Active: body.Active}, ""

	case "note.create":
		var body struct {
			ID      string `json:"id"`
			Book    string `json:"book"`
			Chapter int    `json:"chapter"`
			Verse   int    `json:"verse"`
			Text    string `json:"text"`
		}
		if err := json.Unmarshal(r.Body, &body); err != nil || body.ID == "" || body.Book == "" || body.Text == "" {
			return nil, "malformed body"
		}
		return proxNoteCreateMsg{
			ID:     body.ID,
			Author: r.Src,
			Ref:    AnnotationRef{Book: body.Book, Chapter: body.Chapter, Verse: body.Verse},
			Text:   body.Text,
		}, ""

	case "note.update":
		var body struct {
			ID   string `json:"id"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(r.Body, &body); err != nil || body.ID == "" || body.Text == "" {
			return nil, "malformed body"
		}
		return proxNoteUpdateMsg{ID: body.ID, Author: r.Src, Text: body.Text}, ""

	case "note.delete":
		var body struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(r.Body, &body); err != nil || body.ID == "" {
			return nil, "malformed body"
		}
		return proxNoteDeleteMsg{ID: body.ID, Author: r.Src}, ""

	case "answer":
		var body struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(r.Body, &body); err != nil {
			return nil, "malformed body"
		}
		return proxAnswerMsg{Text: body.Text, ReplyTo: r.ReplyTo}, ""

	default:
		return nil, "" // not an accepted type — ignore silently
	}
}

// ── apply: message → model change, validated against live app state ────

// applyNavGoto resolves msg.Book through the same book-candidate matching
// the keyboard `:goto` flow uses. An exact slug or display-name match
// disambiguates a multi-match prefix (e.g. "john" also prefixing "1 John"
// wouldn't — but "1" would prefix "1 John"/"1 Peter"/… and needs this).
func (m model) applyNavGoto(msg proxNavGotoMsg) (model, bool, string) {
	cands := bookCandidates(msg.Book)
	if len(cands) == 0 {
		return m, false, "unknown book"
	}
	if len(cands) > 1 {
		q := strings.ToLower(msg.Book)
		exact := -1
		for _, c := range cands {
			if strings.ToLower(books[c].slug) == q || strings.ToLower(books[c].name) == q {
				exact = c
				break
			}
		}
		if exact < 0 {
			return m, false, "ambiguous book"
		}
		cands = []int{exact}
	}
	pos := findIndexPos(m.index, cands[0], msg.Chapter)
	if pos < 0 {
		return m, false, "no such chapter"
	}
	return m.gotoTo(pos, msg.Verse), true, ""
}

// applyNavStep steps a chapter. Hitting either end is a no-op per the
// contract, never a rejection.
func (m model) applyNavStep(msg proxNavStepMsg) model {
	if msg.Direction == "next" {
		return m.stepChapter(1)
	}
	return m.stepChapter(-1)
}

// applyTranslationsSet applies an absolute translation set, filtered to
// known codes. Unknown codes are dropped individually; an empty result
// after filtering is a rejection, never a silent no-op.
func (m model) applyTranslationsSet(msg proxTranslationsSetMsg) (model, bool, string) {
	var valid []string
	for _, t := range msg.Translations {
		for _, a := range m.allTrans {
			if a == t {
				valid = append(valid, t)
				break
			}
		}
	}
	if len(valid) == 0 {
		return m, false, "no valid translations"
	}
	sort.Strings(valid)
	m.translations = valid
	m = m.withContentAnchored()
	m.emit("read.translations", map[string]any{"translations": m.translations})
	return m, true, ""
}

// applyGroupsSet merges an active-group map: only listed, known keys
// change. Rejected only if none of the listed keys are known groups —
// a partially-stale caller shouldn't lose the whole command.
func (m model) applyGroupsSet(msg proxGroupsSetMsg) (model, bool, string) {
	if m.store == nil || len(msg.Active) == 0 {
		return m, false, "no groups"
	}
	known := sortedGroupNames(m.store)
	applied := false
	for name, active := range msg.Active {
		for _, k := range known {
			if k == name {
				m.activeGroups[name] = active
				applied = true
				break
			}
		}
	}
	if !applied {
		return m, false, "no known group names"
	}
	m = m.withContentAnchored()
	m.emit("annotation.groups_changed", map[string]any{"active": m.activeGroups})
	return m, true, ""
}

// applyNoteCreate, applyNoteUpdate, and applyNoteDelete are the id-
// addressed counterparts to the keyboard note UI, routed through the
// same authorship-guarded store methods (annotations.go).

func (m model) applyNoteCreate(msg proxNoteCreateMsg) (model, bool, string) {
	if m.store == nil {
		return m, false, "no annotation store"
	}
	if err := m.store.AddNoteWithID(msg.Ref, msg.ID, msg.Author, msg.Text); err != nil {
		if errors.Is(err, ErrNotAuthor) {
			return m, false, "id belongs to another author"
		}
		return m, false, err.Error()
	}
	m.store.Save()
	m.activeGroups[notesGroupName] = true
	m = m.withContentAnchored()
	return m, true, ""
}

func (m model) applyNoteUpdate(msg proxNoteUpdateMsg) (model, bool, string) {
	if m.store == nil {
		return m, false, "no annotation store"
	}
	if err := m.store.UpdateNoteByID(msg.ID, msg.Author, msg.Text); err != nil {
		return m, false, proxNoteErrorReason(err)
	}
	m.store.Save()
	m = m.withContentAnchored()
	return m, true, ""
}

func (m model) applyNoteDelete(msg proxNoteDeleteMsg) (model, bool, string) {
	if m.store == nil {
		return m, false, "no annotation store"
	}
	if err := m.store.DeleteNoteByID(msg.ID, msg.Author); err != nil {
		return m, false, proxNoteErrorReason(err)
	}
	m.store.Save()
	m = m.withContentAnchored()
	return m, true, ""
}

func proxNoteErrorReason(err error) string {
	switch {
	case errors.Is(err, ErrNoteNotFound):
		return "no such note"
	case errors.Is(err, ErrNotAuthor):
		return "not authored by this agent"
	default:
		return err.Error()
	}
}

// proxAnswerTimeout returns a tea.Cmd that fires a proxAnswerTimeoutMsg
// after the presence-timeout window if nothing has answered by then
// (app-integration.md "Presence" — a timeout must be a loud, honest
// message, never a silent hang).
func proxAnswerTimeout(qid string) tea.Cmd {
	return tea.Tick(2*time.Minute, func(time.Time) tea.Msg {
		return proxAnswerTimeoutMsg{QID: qid}
	})
}
