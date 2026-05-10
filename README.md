# bibtui

A terminal Bible study companion built with the [Charm](https://charm.sh) ecosystem.

## Usage

```
./bibtui [translation]
```

Translations: `kjv` (default), `web`, `bsb`

## Keys

| Key | Action |
|-----|--------|
| `j` / `k` | Scroll down / up |
| `]` / `[` | Next / previous chapter |
| `q` | Quit |

## Building

```
go build -o bibtui .
```

## Bible data

Translations live in `bibles/<code>/<book>/<chapter>.md`. To regenerate:

```
python3 scripts/fetch_web.py          # WEB
python3 scripts/download_bibles.py    # KJV, BSB
```
