package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ── AI integration (proxenos) ────────────────────────────────────────────
//
// bibtui is an "app" in proxenos terms: it emits events onto a stream when
// the user acts, and (part B) applies events an attached agent sends back.
// See docs/proxenos-contract.md for the full contract. Off by default —
// no --proxenos flag means no proxenos process is ever spawned and no
// stream file is ever touched.

// proxAppSrc is this app's fixed proxenos identity. Never spoofed via
// --as: the CLI stamps src from an explicit --as flag on every send.
const proxAppSrc = "app:bibtui"

//go:embed proxenos-contract.json
var proxenosContractJSON []byte

// parseArgs pulls "--proxenos <stream>" out of the argv, leaving the
// remaining positional args (translation codes) untouched. bibtui has no
// other flags, so this is a minimal hand-rolled scan rather than pulling
// in the flag package for one option.
func parseArgs(args []string) (proxStream string, rest []string) {
	for i := 0; i < len(args); i++ {
		if args[i] == "--proxenos" && i+1 < len(args) {
			proxStream = args[i+1]
			i++
			continue
		}
		rest = append(rest, args[i])
	}
	return proxStream, rest
}

// bootstrapProxenos idempotently ensures the stream exists (seeded with
// bibtui's contract) and that the proxenos binary is actually on PATH.
// Any failure disables the feature for this run rather than blocking the
// user from reading — middleware trouble is never a reason to not read a
// Bible.
func bootstrapProxenos(stream string) bool {
	if stream == "" {
		return false
	}
	if _, err := exec.LookPath("proxenos"); err != nil {
		proxLogf("proxenos not on PATH: %v — AI integration disabled", err)
		return false
	}
	if exec.Command("proxenos", "tail", stream, "--tail", "1").Run() == nil {
		return true // stream already exists
	}
	tmp, err := os.CreateTemp("", "bibtui-contract-*.json")
	if err != nil {
		proxLogf("contract tempfile: %v", err)
		return false
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(proxenosContractJSON); err != nil {
		tmp.Close()
		proxLogf("contract write: %v", err)
		return false
	}
	tmp.Close()
	if out, err := exec.Command("proxenos", "new", stream, "--contract", tmp.Name()).CombinedOutput(); err != nil {
		proxLogf("proxenos new %s failed: %v (%s)", stream, err, strings.TrimSpace(string(out)))
		return false
	}
	return true
}

// emitSync sends one event and blocks until proxenos has appended it (or
// failed to). Used only where the caller is about to exit and a
// fire-and-forget goroutine would be killed before it runs.
func (m model) emitSync(typ string, body any) {
	if m.proxStream == "" {
		return
	}
	data, err := json.Marshal(body)
	if err != nil {
		proxLogf("marshal %s: %v", typ, err)
		return
	}
	cmd := exec.Command("proxenos", "send", m.proxStream, typ, string(data), "--as", proxAppSrc)
	if out, err := cmd.CombinedOutput(); err != nil {
		proxLogf("send %s failed: %v (%s)", typ, err, strings.TrimSpace(string(out)))
	}
}

// emit fires emitSync in the background. Never blocks the UI goroutine;
// never panics bibtui if the proxenos binary or stream disappears
// mid-session — errors land in proxenos.log, not on screen.
func (m model) emit(typ string, body any) {
	if m.proxStream == "" {
		return
	}
	go m.emitSync(typ, body)
}

// proxLocationBody builds the read.location body for the chapter bibtui
// is currently positioned at.
func (m model) proxLocationBody() map[string]any {
	r := m.index[m.pos]
	return map[string]any{
		"translations": m.translations,
		"book":         r.book.slug,
		"chapter":      r.num,
		"ref":          fmt.Sprintf("%s %d", r.book.name, r.num),
	}
}

// ── error surface ────────────────────────────────────────────────────────
//
// bibtui owns the alt-screen terminal; writing integration errors to
// stderr would corrupt the display. They go to a plain log file instead —
// files over apps, and consistent with session.json/stats.json living
// beside the binary.

var proxLogFile *os.File

func proxLogf(format string, args ...any) {
	if proxLogFile == nil {
		f, err := os.OpenFile("proxenos.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return
		}
		proxLogFile = f
	}
	fmt.Fprintf(proxLogFile, "%s "+format+"\n", append([]any{time.Now().Format(time.RFC3339)}, args...)...)
}
