# bibtui ↔ omp extension

This file is the **harness-side glue** for the supported setup. It is what
makes "open bibtui and let's read John 3" work from an omp chat.

It is not part of the bibtui binary. It is a single TypeScript extension
(`proxenos-bibtui.ts`) that omp loads; bibtui itself only speaks the
proxenos stream, so it stays usable without any agent.

## What it does

1. **Registers `bibtui_open`** — the tool the agent calls when the user asks
   to read together. It takes `dir` (where bibtui lives), `stream`, and
   optional `block`, `launch`, `wakeTypes`, `milestones`.
2. **Spawns bibtui in a Tern split** beside the conversation:
   `tern split <block> right --cwd <dir> --keep-open -- ./bibtui --proxenos <stream> kjv`.
   If the agent does not pass `block`, the extension asks the user's pane
   via `$TERN_PANE`; when unavailable it falls back to `@focused`.
3. **Watches the stream file** (the one proxenos writes). It applies the
   contract's own rules: leading-newline framing, cursor idempotency, and a
   digest buffer. Reading activity (`read.location`, `note.created`, …) is
   delivered as **silent context** on the next turn — it does not wake the
   agent. Only `user.message` and explicitly configured milestones
   (`wakeTypes`, `milestones`) wake a turn.
4. **Teaches the agent the contract** in the tool result: how to send
   `note.create`, `nav.goto`, etc., and the event envelope. The app's
   `stream.contract` event remains the source of truth.
5. **Cleans up** on session shutdown: closes the Tern block it created and
   stops its watcher. It never kills an app the user launched themselves.

## Install

```sh
cp proxenos-bibtui.ts ~/.omp/agent/extensions/
```

Requirements:
- `proxenos` on `PATH` (the app's `--proxenos` integration shells out to it).
- [Tern](https://stencil.so/tern) installed and running on the host where omp
  runs (`tern` on `PATH`; the extension uses `tern split`, `tern close`,
  `tern ls`).
- bibtui built at `dir` (defaults to the session's cwd) with `bibles/` present.

## Notes

- `block` is the pane to split beside. The reliable way to get it is
  `echo $TERN_PANE` inside the pane where omp is running. If omitted, the
  extension uses `$TERN_PANE` from its own environment and falls back to
  `@focused`.
- `dir` must contain the `bibtui` binary. If you pass the repo's parent
  (`dir=/tmp` for `/tmp/bibtui`), the extension finds `bibtui/bibtui`.
- The extension is deliberately *bibtui-specific*: it knows the app's binary
  name and default translations. A general extension would read the app's
  contract instead; that is the next step, not this file.
