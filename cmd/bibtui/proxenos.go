package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
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

// parseArgs pulls "--proxenos <stream>" and "--agent <name>" out of the
// argv, leaving the remaining positional args (translation codes)
// untouched. bibtui has no other flags, so this is a minimal hand-rolled
// scan rather than pulling in the flag package for two options.
//
// --agent names the proxenos adapter to attach (e.g. "omp", "claude",
// "test"). Absent = no agent session is started; bibtui still emits to the
// stream, but nothing wakes a harness. This is the app-owned attach: the
// bridge's lifetime is bibtui's own, so starting a session is just
// starting the app with one flag — no second terminal, no systemd unit.
func parseArgs(args []string) (proxStream, proxAgent string, rest []string) {
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--proxenos" && i+1 < len(args):
			proxStream = args[i+1]
			i++ // skip the value
			continue
		case args[i] == "--agent" && i+1 < len(args):
			proxAgent = args[i+1]
			i++ // skip the value
			continue
		}
		rest = append(rest, args[i])
	}
	return proxStream, proxAgent, rest
}

// ensureStreamSeeded verifies the stream exists (creating it with bibtui's
// embedded contract if absent). Works identically for local and relay refs —
// the CLI owns that distinction.
func ensureStreamSeeded(stream string) error {
	if exec.Command("proxenos", "tail", stream, "--tail", "1").Run() == nil {
		return nil
	}
	tmp, err := os.CreateTemp("", "bibtui-contract-*.json")
	if err != nil {
		return fmt.Errorf("contract tempfile: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(proxenosContractJSON); err != nil {
		tmp.Close()
		return fmt.Errorf("contract write: %w", err)
	}
	tmp.Close()
	if out, err := exec.Command("proxenos", "new", stream, "--contract", tmp.Name()).CombinedOutput(); err != nil {
		return fmt.Errorf("proxenos new %s failed: %v (%s)", stream, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// bootstrapProxenos idempotently ensures the stream exists (seeded with
// bibtui's contract) and that the proxenos binary is actually on PATH.
// Any failure disables the feature for this run rather than blocking the
// user from reading — middleware trouble is never a reason to not read a
// Bible.
//
// A stream name containing ':' (proxenos <relay>:<stream> syntax) is a
// RELAY stream: it lives on a remote relay's data dir, so there is no
// local file to poll — inbound goes through `proxenos tail -f` (path is
// returned empty and the poller picks the relay source). The CLI handles
// new/tail/send against the configured binding identically.
func bootstrapProxenos(stream string) (path string, ok bool) {
	if stream == "" {
		return "", false
	}
	if _, err := exec.LookPath("proxenos"); err != nil {
		proxLogf("proxenos not on PATH: %v — AI integration disabled", err)
		return "", false
	}
	if err := ensureStreamSeeded(stream); err != nil {
		proxLogf("%v", err)
		return "", false
	}
	if strings.Contains(stream, ":") {
		return "", true // relay mode: no local path, poller tails via CLI
	}
	path, err := resolveStreamPath(stream)
	if err != nil {
		proxLogf("resolve stream path for %s: %v", stream, err)
		return "", false
	}
	return path, true
}

// resolveStreamPath asks proxenos for a stream's resolved absolute file
// path via `ls --json`, rather than recomputing $XDG_DATA_HOME/... by
// hand — proxenos config can override the streams directory
// (config.toml's StreamsDir), and the CLI is the only party guaranteed
// to agree with itself on where a stream actually lives.
func resolveStreamPath(stream string) (string, error) {
	out, err := exec.Command("proxenos", "ls", "--json").Output()
	if err != nil {
		return "", err
	}
	var infos []struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	if err := json.Unmarshal(out, &infos); err != nil {
		return "", err
	}
	for _, si := range infos {
		if si.Name == stream {
			return si.Path, nil
		}
	}
	return "", fmt.Errorf("stream %q not found in proxenos ls", stream)
}

// sendProxEvent sends one event and blocks until proxenos has appended it
// (or failed to). The one place that actually shells out; emit/emitSync/
// sendProxEventGetID/action.rejected from the inbound poller all funnel
// through here.
func sendProxEvent(stream, typ string, body any) {
	if stream == "" {
		return
	}
	data, err := json.Marshal(body)
	if err != nil {
		proxLogf("marshal %s: %v", typ, err)
		return
	}
	cmd := exec.Command("proxenos", "send", stream, typ, string(data), "--as", proxAppSrc)
	if out, err := cmd.CombinedOutput(); err != nil {
		proxLogf("send %s failed: %v (%s)", typ, err, strings.TrimSpace(string(out)))
	}
}

// sendProxEventGetID sends one event and returns the id proxenos stamped
// it with. Only the ask flow needs this — everything else is
// fire-and-forget with nothing to correlate against.
func sendProxEventGetID(stream, typ string, body any) (string, error) {
	if stream == "" {
		return "", fmt.Errorf("proxenos integration disabled")
	}
	data, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	out, err := exec.Command("proxenos", "send", stream, typ, string(data), "--as", proxAppSrc).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// emitSync sends one event and blocks until proxenos has appended it (or
// failed to). Used only where the caller is about to exit and a
// fire-and-forget goroutine would be killed before it runs.
func (m model) emitSync(typ string, body any) {
	sendProxEvent(m.proxStream, typ, body)
}

// emit fires sendProxEvent in the background. Never blocks the UI
// goroutine; never panics bibtui if the proxenos binary or stream
// disappears mid-session — errors land in proxenos.log, not on screen.
func (m model) emit(typ string, body any) {
	if m.proxStream == "" {
		return
	}
	go sendProxEvent(m.proxStream, typ, body)
}

// attachAppOwned starts the proxenos bridge (proxenos attach) as a child
// process of bibtui itself, stopped when bibtui exits. This is the "app
// owns its bridge" pattern: the user starts a session by starting the app
// — `./bibtui --proxenos bibtui --agent omp` — instead of provisioning a
// second terminal or a systemd unit by hand. The bridge is one-directional
// (stream → harness) exactly as documented; the harness speaks back via
// `proxenos send` on its own.
//
// stdout/stderr go to proxenos-attach.log beside the other app state files
// (same "files over apps" convention as proxLogf), so attach failures are
// diagnosable without touching the TUI's alt-screen.
//
// On failure the attach is dropped for this run and nil is returned — the
// same degradation rule as bootstrapProxenos: middleware trouble never
// blocks reading.
func attachAppOwned(stream, agent string) *proxAttach {
	// Name the attachment after the process so `proxenos ls` shows who
	// owns it and a second bibtui on the same stream can't silently share
	// one attachment name. One bibtui per (stream, agent) is the sane
	// ceiling; a second run with the same --as fails loudly in the log
	// rather than forking the conversation.
	name := "bibtui-" + agent
	logf, err := os.OpenFile("proxenos-attach.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		proxLogf("attach: log file: %v", err)
		return nil
	}
	cmd := exec.Command("proxenos", "attach", stream, "--as", name, "--agent", agent,
		// Only real questions should wake (and pay for) a harness turn —
		// the same default the operator runbook documents. stream.* types
		// always pass the filter regardless.
		"--types", "user.message")
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // own pgroup; not SIGINT'd by the TUI's Ctrl-C, and stopProxAttach can signal the group
	if err := cmd.Start(); err != nil {
		logf.Close()
		proxLogf("attach: start: %v", err)
		return nil
	}
	fmt.Fprintf(logf, "=== %s: attach pid %d (agent %s) ===\n", time.Now().UTC().Format(time.RFC3339), cmd.Process.Pid, agent)
	proxLogf("attach: pid %d as %s agent %s", cmd.Process.Pid, name, agent)
	a := &proxAttach{cmd: cmd, logf: logf, done: make(chan struct{})}
	// The reaper owns the one Wait: a bridge that dies mid-session is
	// reaped immediately (no zombie) and unblocks stopProxAttach later.
	go func() {
		if err := cmd.Wait(); err != nil {
			proxLogf("attach: bridge exited: %v", err)
		} else {
			proxLogf("attach: bridge exited cleanly")
		}
		close(a.done)
	}()
	return a
}

// stopProxAttach terminates the app-owned bridge: SIGTERM the bridge's
// process group (attach sets Setpgid, so -pid reaches it and any
// grandchildren), wait briefly, then SIGKILL. Called on bibtui's normal
// exit paths; a SIGKILL'd bibtui can't run it — that residue is visible
// in `proxenos ls` and killable by hand (documented).
func stopProxAttach(a *proxAttach) {
	if a == nil || a.cmd.Process == nil {
		return
	}
	proxLogf("attach: stopping bridge pid %d", a.cmd.Process.Pid)
	syscall.Kill(-a.cmd.Process.Pid, syscall.SIGTERM)
	select {
	case <-a.done:
	case <-time.After(2 * time.Second):
		syscall.Kill(-a.cmd.Process.Pid, syscall.SIGKILL)
		<-a.done
	}
	a.logf.Close()
}

// proxAttach carries one app-owned bridge and its single-Wait lifecycle.
type proxAttach struct {
	cmd  *exec.Cmd
	logf *os.File
	done chan struct{} // closed by the reaper once Wait returns
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
