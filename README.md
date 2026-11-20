# bibtui

A terminal Bible study companion built with the [Charm](https://charm.sh) ecosystem.

## Usage

```
./bibtui [--proxenos <stream>] [translation...]
```

Translations: `kjv` (default), `web`, `bsb` — anything under `bibles/`.

## Keys

| Key | Action |
|-----|--------|
| `j` / `k` | Scroll |
| `]` / `[` | Next / previous chapter |
| `n` | Note |
| `a` | Group |
| `q` | Quit |

## Reading with an agent

Optional, and the app works fine without it. With
[proxenos](https://github.com/dshumphr/proxenos) and an omp + Tern setup,
bibtui can share your reading position with an agent in the chat:

```sh
proxenos new bibtui --contract docs/proxenos-contract.json   # once
./bibtui --proxenos bibtui kjv
```

Then, in omp (with the extension installed — see
[`omp-extension/README.md`](omp-extension/README.md)):

> open bibtui and let's read John 3 together

The agent opens bibtui beside the conversation, sees your reading position as
silent context, and can save notes into the app when you ask. The event
contract — what bibtui emits and accepts — is in
[`docs/proxenos-contract.md`](docs/proxenos-contract.md) and embedded in the
binary as `docs/proxenos-contract.json`.

The extension is the only harness-specific piece; bibtui itself only speaks
the stream. Without an agent, `--proxenos` still emits the same events, and
`proxenos tail <stream> -f` shows them.

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
