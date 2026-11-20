# bibtui

A terminal Bible study companion built with the [Charm](https://charm.sh) ecosystem.

## Usage

```
./bibtui [--proxenos <stream>] [translation...]
```

Translations: `kjv` (default), `web`, `bsb` — anything under `bibles/`.
`-h`/`--help` prints the same.

## Keys

| Key | Action |
|-----|--------|
| `j` / `k` | Scroll |
| `]` / `[` | Next / previous chapter |
| `n` | Note |
| `a` | Group |
| `q` | Quit |

## Reading together with an agent (omp)

Optional — bibtui is a fine plain reader without it. With
[proxenos](https://github.com/dshumphr/proxenos) and [Tern](https://stencil.so/tern),
an omp session can open bibtui beside the conversation, follow what you're
reading, and save notes into the app when you ask.

### Install by asking your agent

Open omp and paste:

> Set up bibtui so you can open it beside our conversation.
> 1. If `proxenos` is not on my PATH, install it: clone
>    https://github.com/dshumphr/proxenos and `go build -o ~/.local/bin/proxenos .`
> 2. Clone https://github.com/dshumphr/bibtui, then `go build -o bibtui ./cmd/bibtui`
>    in it (keep the repo — `bibles/` lives there).
> 3. Copy its `omp-extension/proxenos-bibtui.ts` to `~/.omp/agent/extensions/`.
> 4. Check `tern --version`. If Tern is missing, stop and tell me — it is the
>    prerequisite for opening panes, and installing it is my job (closed beta).
> 5. Then stop and tell me to start a new omp session — extensions load at
>    session start (I checked: `/reload-plugins` does not pick up a brand-new
>    file, so do not claim it is installed until a new session sees the tool).
>
> In the new session, verify: call `bibtui_open` with `dir=<the repo path>`,
> `stream=bibtui-smoke`, `launch=auto` — a bibtui block should appear beside
> the conversation and the result should name its block id. Then
> `proxenos tail bibtui-smoke --tail 3` should show `stream.meta`,
> `stream.contract` and `app.started`; close the block with
> `tern close <block-id>`, then `proxenos rm bibtui-smoke`. Report exactly
> what happened.

When it works, start reading for real:

> open bibtui and let's read John 3 together

### What that gives you

- The agent opens bibtui in a block **beside the conversation** and closes it
  when the session ends.
- Your reading position is handed to the agent as **silent context** — it
  knows where you are on its next turn, and navigation never costs a turn.
- Ask questions in the chat; the agent answers there and can write anything
  worth keeping into the app as a note (`note.create`), or move you
  (`nav.goto`).

### Manual install

```sh
go build -o ~/.local/bin/proxenos .            # in a proxenos checkout
go build -o bibtui ./cmd/bibtui                # in this repo
cp omp-extension/proxenos-bibtui.ts ~/.omp/agent/extensions/
# then start a new omp session (extensions load at startup; /reload-plugins
# does not pick up a brand-new file)
```

Per-session alternative, when you don't want an extension:

```sh
proxenos new bibtui --contract docs/proxenos-contract.json   # once, idempotent
./bibtui --proxenos bibtui kjv
proxenos tail bibtui -f                                       # watch it, or read the file
```

Nothing wakes an agent by itself — questions live in the chat, so a harness
integration is what adds the turn-taking.

### Where things live

- [`docs/proxenos-contract.md`](docs/proxenos-contract.md) — the full event
  contract; [`docs/proxenos-contract.json`](docs/proxenos-contract.json) is the
  machine-readable version embedded in the binary and seeded into the stream.
- [`omp-extension/README.md`](omp-extension/README.md) — what the extension
  does and its parameters.

## Building

```
go build -o bibtui ./cmd/bibtui
```

## Bible data

Translations live in `bibles/<code>/<book>/<chapter>.md`. To regenerate:

```
python3 scripts/fetch_web.py          # WEB
python3 scripts/download_bibles.py    # KJV, BSB
```
