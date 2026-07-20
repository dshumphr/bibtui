# Implementation plan: bibtui ↔ proxenos

Executes the contract in [`proxenos-contract.md`](proxenos-contract.md).
Two parts, in dependency order: **Part A (Emits)** ships first — it's
additive, needs no schema change, and alone gives an attached agent a live
audit trail plus the ability to answer questions. **Part B (Accepts)** is
the invasive half — schema migration, an authorship guard, and the inbound
poll loop — and is where the agent actually gets to act.

Both parts are gated behind one new flag: `./bibtui --proxenos <stream>`
(absent = feature off entirely, no proxenos process ever spawned, no
`.jsonl` file touched — "default to no feature"). All new code lives in
two new files, `cmd/bibtui/proxenos.go` (Part A) and
`cmd/bibtui/proxenos_inbound.go` (Part B), to keep the integration
excisable from the reading-app core.

## Shared foundation (built once, in Part A, used by both)

- **Model fields** (`main.go`, `model` struct): `proxStream string`,
  `proxSrc string` (constant `"app:bibtui"`), `askOpen bool`,
  `askInput string`, `askInputCursor int` — the send-half of the ask
  overlay. (`pendingQID`, `thinking`, `lastAnswer` are Part B fields, added
  when the receive-half lands, listed there so each part's diff is
  self-contained.)
- **`go:embed` the contract**: `//go:embed proxenos-contract.json` a
  package-level `contractJSON []byte` in `proxenos.go`, sourced from
  `docs/proxenos-contract.json` (copied in, single source of truth checked
  by CI: a test byte-compares the embedded copy against `docs/`). Ships in
  the binary; no runtime dependency on repo layout.
- **`emit(typ string, body any)`**: marshals `body`, writes it to a temp
  file is *not* needed here — `send`'s body arg is argv, not `--contract`'s
  file arg — so `emit` runs `exec.Command("proxenos", "send", stream, typ,
  string(jsonBody), "--as", proxSrc)` in a goroutine, fire-and-forget,
  swallowing/logging errors to a small ring buffer surfaced only in a
  debug view (never a modal, never blocks the UI thread, never panics
  bibtui if the `proxenos` binary is missing — the whole feature degrades
  to "off").
- **Startup bootstrap** (`main.go:main`, after `store`/`stats` load, before
  `tea.NewProgram`): if `--proxenos` set, `exec.Command("proxenos", "ls",
  "--json")` (or `tail --tail 1`) to check existence; if absent, write
  `contractJSON` to a temp file and run `proxenos new <stream> --contract
  <tmp>`. Non-fatal on error (log + disable the feature for this run,
  don't block reading a Bible over a middleware hiccup).

## Part A — Emits

Goal: every user action in the contract's `emits` table lands on the
stream. No inbound handling yet; `answer`/accepted actions from an agent
are invisible to bibtui after this part (verifiable only via `proxenos
tail`, not yet in the TUI).

### A1. Lifecycle
- `main()`: after successful bootstrap, `emit("app.started", {translations:
  startTrans, resumed: session != nil})`.
- `main()`: wrap `p.Run()`'s return path — on both the error and success
  branch, `emit("app.exited", {})` before `os.Exit`. Best-effort: accept
  that SIGKILL/terminal-kill skips this, document it as-is (matches the
  contract's "best-effort" note already).

### A2. `read.location`
Three call sites in `main.go` `Update()`, all already mutate `m.pos` then
call `m.withContent()` / `saveSession(m)`:
- goto resolution (`tea.KeyEnter` branch inside `m.gotoOpen`, ~line 276-290)
- `]` / `[` (~line 409-424)
- home `n` (new session) and `r` (resume) (~line 325-342)

Rather than four call sites independently building the body, add one
helper `func (m model) proxLocationBody() body` reading `m.index[m.pos]`
and `m.translations`, and call `emit("read.location", m.proxLocationBody())`
at the tail of each of the four branches, right after `saveSession(m)`
(or, for `n`/`r` which don't currently call `saveSession`, right after
`m.recordView()`). No debounce needed — these are already discrete
keypress-driven transitions.

### A3. `read.translations`
`o` picker's `" "`/`"enter"` case (~line 359-363): after
`m = m.withToggled(...)`, `emit("read.translations", {translations:
m.translations})`.

### A4. `annotation.groups_changed`
`a` picker's `" "`/`"enter"` case (~line 381-387): after the toggle,
`emit("annotation.groups_changed", {active: m.activeGroups})`.

### A5. `note.created` / `note.updated` / `note.deleted`
`updateInnerNote()` in `main.go`:
- save branch (~line 861-870): after `m.store.Save()`, `author` field
  needed for the body isn't in scope yet without the Part B schema change
  — for A-only, hardcode `"author": "user"` (every note created through
  this UI path *is* user-authored until Part B lets an agent's note reach
  the same store). Distinguish create vs. update by whether
  `m.noteEditIdx >= 0` was true on entry; emit `note.created` or
  `note.updated` accordingly with `{book, chapter, verse, text}` (create)
  or `{text}` (update). No `id` field yet — added when Part B's schema
  migration lands (A ships without it, B's migration is additive so A's
  emit call gains an `id` field in one line at that point, not a rewrite).
- delete-confirm `"y"`/`"Y"` branch (~line 820-829): after
  `m.store.DeleteNoteAt(...)` + `Save()`, `emit("note.deleted", {})` (again,
  `id` added once Part B exists).

### A6. `user.message` — the ask overlay (send-half)
New keybinding: `?` in reader mode (`Update()`'s main `switch msg.String()`,
alongside `o`/`n`/`a`), guarded by `m.proxStream != ""` (no overlay, no
keybinding stolen, when the feature is off). Opens `m.askOpen = true`,
mirroring `gotoOpen`'s text-input handling exactly (a new `if m.askOpen`
block ahead of the main switch, same shape as the existing `if m.gotoOpen`
block: rune append, backspace, esc-to-cancel). On `KeyEnter`:
`emit("user.message", {text: m.askInput, book, chapter, verse, ref})` using
`m.activeRef()` for position; close the overlay. (Part B adds the
"thinking…" rendering and the `answer` receive path on top of this same
overlay state — A only needs the input box and the send.)

### A7. Verification
`./bibtui --proxenos bibtui-dev kjv`, second terminal `proxenos tail -f
bibtui-dev --pretty`. Walk every keybinding (`]`/`[`, `:goto`, `o` toggle,
`a` toggle, `n` note create/edit/delete, `?` ask) and confirm exactly the
expected single event lands per action — nothing on plain `j`/`k` scroll,
nothing double-fired. No mock agent needed yet.

## Part B — Accepts

Goal: every action in the contract's `accepts` table actually moves the
app, with the authorship guard closing the note-tampering hole, and the
`answer` reply completing the ask overlay A6 started.

### B1. Schema migration (`annotations.go`)
Add `ID string` and `Author string` to `Annotation` (contract doc's exact
shape). Migration: `AnnotationStore.Load()` back-fills `ID` with a fresh
ULID and `Author` with `"user"` for any annotation missing them, then
re-saves once — existing `annotations.json`/`annotations.example.json`
files need no manual edit. New store methods:
- `FindNoteByID(id string) (*Annotation, group string, ok bool)`
- `AddNoteWithID(ref AnnotationRef, id, author, text string)`
- `UpdateNoteByID(id, author, text string) error` — returns a typed
  `ErrNotAuthor` when `Author != author`, distinct from "not found," so the
  caller can pick the right `action.rejected` reason.
- `DeleteNoteByID(id, author string) error` — same error shape.

Once these land, go back and finish A5's `id` field (one-line addition to
each `emit("note.*", ...)` call) and A6/B4's note bodies.

### B2. Cursor persistence
New file `cursor.go` (or a section of `proxenos.go`):
`proxenos-state.json` — `{"last_applied_id": "..."}` — same load/save
pattern as `session.go`. `loadProxCursor()` / `saveProxCursor(id string)`.

### B3. The poll loop
`proxenos_inbound.go`:
- `startProxPoll(streamPath string, self string, send func(tea.Msg))`
  launched from `main()` right after `tea.NewProgram(m, ...)` is
  constructed (needs `p.Send`) but before `p.Run()`.
- Startup catch-up: open the stream file, scan from byte 0 splitting on the
  leading-`\n` record framing (skip blank lines, `json.Unmarshal` each
  segment into the `Event{V,ID,TS,Src,Type,Body,ReplyTo,To}` shape —
  mirrors proxenos's own envelope, PLAN.md §2.1/§2.2). Skip through the
  cursor's `last_applied_id` if found; if not found (fresh stream, or
  compacted/generation-changed — detect via a `stream.meta` `generation`
  mismatch against a remembered one, same field the bridge itself checks),
  skip all history and start applying only from here forward.
- Live tailing: keep the file open, `time.Tick(250ms)`, read newly
  appended bytes, `stat` by `(dev, inode)` each tick to detect
  replace/compaction and reopen from the top when it changes — the same
  poll shape proxenos's own `attach`/`tail -f` use (PLAN.md §6.1).
- Self-filter: skip any record where `Src == self` (i.e. `"app:bibtui"`).
- Dispatch by `Type` to one handler per accepted action (B5); each handler
  returns `(tea.Msg, ok bool, rejectReason string)`; the poller calls
  `send(msg)` on success or `emit("action.rejected", {...})` on failure,
  then persists the cursor either way (a rejected command still advances
  past it — retrying a rejected command forever would just spam the
  rejection).

### B4. Model wiring
- New `tea.Msg` types: `proxNavGotoMsg`, `proxNavStepMsg`,
  `proxTranslationsSetMsg`, `proxGroupsSetMsg`, `proxNoteCreateMsg`,
  `proxNoteUpdateMsg`, `proxNoteDeleteMsg`, `proxAnswerMsg` — one struct
  per accepted type, carrying its validated/parsed body.
- `Update()` gains a case per message type, each calling the *same*
  helper the matching keybinding already uses. This requires two small
  refactors so both paths share code instead of duplicating it:
  - extract `func (m model) gotoTo(bookIdx, chapter, verse int) model` out
    of the existing goto-`KeyEnter` branch; both `:goto` and
    `proxNavGotoMsg` call it.
  - extract `func (m model) stepChapter(dir int) model` out of the
    existing `]`/`[` branches; both keys and `proxNavStepMsg` call it.
  - `translations.set` / `groups.set` / note create/update/delete need no
    extraction — they call `withToggled`-adjacent logic directly (set
    semantics differ enough from the toggle-driven picker UI that sharing
    isn't natural; each is ~3 lines against the store/model already).
- `proxAnswerMsg`: sets `m.thinking = false`, `m.lastAnswer = body.Text`,
  only if `body.ReplyTo == m.pendingQID` (else ignored — a stale/duplicate
  answer must not clobber a newer question's pending state). Rendering:
  the ask overlay (A6) switches from "thinking…" to the answer text,
  dismissed by any key or replaced by the next question.
- Presence timeout: `?`'s `KeyEnter` (A6) also returns a
  `tea.Tick(2*time.Minute, ...)` cmd tagged with the sent id; if it fires
  and `m.pendingQID` still equals that id (no answer arrived), set a
  "agent not responding — check `proxenos ls`" message in the same overlay
  slot.

### B5. Per-action validation (the `action.rejected` half)
Each handler in `proxenos_inbound.go`:
- `nav.goto{book,chapter,verse?}`: resolve `book` through the existing
  `bookCandidates`/`findIndexPos` (goto.go) — zero matches or >1 matches
  (ambiguous) → reject with `reason` naming which; chapter out of range for
  the resolved book → reject with `reason:"no such chapter"`.
- `nav.step{direction}`: `direction` not `"next"`/`"prev"` → reject
  `reason:"invalid direction"`; at the first/last chapter → **not** a
  rejection, a no-op `tea.Msg` (matches the contract: "no-op, not
  rejected").
- `translations.set{translations}`: filter to `m.allTrans`; empty result
  after filtering → reject `reason:"no valid translations"` (never silently
  apply an empty set).
- `groups.set{active}`: unknown group names in the map are dropped
  individually (not a rejection — a partially-stale agent contract
  shouldn't break the whole command); reject only on a fully malformed
  body (`active` not an object).
- `note.create{id,book,chapter,verse,text}`: if `id` already exists with a
  *different* `Author`, reject `reason:"id belongs to another author"`
  (closes the id-reuse-to-hijack-a-user-note hole flagged in the contract
  doc); otherwise create-or-overwrite with `Author: src`.
- `note.update{id,text}` / `note.delete{id}`: not found → reject
  `reason:"no such note"`; found with `Author != src` → reject
  `reason:"not authored by this agent"`; else apply.
- `answer{text,refs}`: always accepted (nothing to validate structurally
  beyond `text` present) — routed to B4's `proxAnswerMsg` handling.

### B6. Verification
`proxenos attach bibtui-dev --as main --agent test` (the compiled-in mock
adapter — deterministic, no API key) driving canned turns, or manual
`proxenos send bibtui-dev <type> '<json>' --as agent:main` for each
accepted type:
1. `nav.goto`/`nav.step` — confirm the TUI actually navigates, same as
   pressing the key.
2. `translations.set`/`groups.set` — confirm the pane/annotation-column
   changes.
3. `note.create` then `note.update` with the same `id` — confirm one note,
   edited, not two.
4. `note.delete` on a **user-authored** note from `agent:main` — confirm
   `action.rejected{reason:"not authored by this agent"}` on the stream
   and the note still present in `annotations.json`.
5. Malformed body (missing `book` on `nav.goto`) — confirm
   `action.rejected`, not a panic or silent no-op.
6. Full loop: `?` a question, `agent:main` sends `answer` with matching
   `reply_to` — confirm "thinking…" clears and the answer renders; send a
   second `answer` with a stale/mismatched `reply_to` — confirm it's
   ignored.
7. Restart bibtui mid-session after an agent `nav.goto` was already
   applied and the user has since navigated elsewhere — confirm the old
   `nav.goto` is **not** replayed (cursor test, the correctness case the
   contract doc calls out under "Cursor & idempotency").
