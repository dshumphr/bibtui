/**
 * proxenos-bibtui — omp extension for the "conversation-first" flow.
 *
 * The agent (live omp session) launches bibtui itself and becomes the pump:
 * instead of a `proxenos attach` bridge spawning headless harness turns, this
 * extension watches the stream file and injects the user's bibtui questions
 * into the CURRENT session via pi.sendUserMessage — same conversation, full
 * context, no second session, no extra paid-turn fork.
 *
 * Handshake (who knows what):
 *   1. Agent calls the bibtui_open tool → extension launches bibtui with
 *      --proxenos <stream> (bibtui bootstraps stream + contract idempotently).
 *   2. Extension polls the stream file (leading-\n NDJSON framing, EOF rule)
 *      and injects every non-agent user.message into the live session.
 *   3. The user asks questions in the harness chat; bibtui has no ask box.
 *      Reading activity arrives as silent context, and the agent acts on the
 *      app through its accepts verbs (nav.goto, note.create, …).
 *
 * bibtui stays harness-agnostic: this file is the only omp-specific glue.
 *
 * Install: copy to ~/.omp/agent/extensions/ (or project .omp/extensions/),
 * or run `omp -e /path/to/proxenos-bibtui.ts`.
 */
import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";
import { execFile, spawn } from "node:child_process";
import { promisify } from "node:util";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

const execFileP = promisify(execFile);

interface Envelope {
	v: number;
	id: string;
	ts: string;
	src: string;
	type: string;
	body: unknown;
	reply_to?: string;
}

interface Watcher {
	stream: string;
	filePath: string;
	offset: number;
	pendingCarry: string;
	timer: NodeJS.Timeout;
	seen: Set<string>;
	spawnedPid?: number;
	/** tern block id when bibtui was spawned via the tern CLI (shutdown kills it). */
	ternBlockId?: string | null;
	/** Event types that start a turn. Everything else lands in the digest. */
	wake: Set<string>;
	/** Wake on book transitions (read.location with a different book than before). */
	milestones: boolean;
	lastBook?: string;
	/** Context lines awaiting the next turn (hidden, zero-cost delivery). */
	digest: string[];
	digestTimer: NodeJS.Timeout | undefined;
}

const POLL_MS = 500;
const STREAM_WAIT_MS = 10_000;
const DIGEST_FLUSH_MS = 2_000;
const DIGEST_MAX_LINES = 24;
const DIGEST_LINE_MAX = 140;

/**
 * Multiplexer omp can spawn into, detected from the session environment.
 * null = none recognized (tern, WezTerm, plain terminal) — the launch chain
 * then tries the tern CLI before falling back to a detached pty.
 */
function detectMultiplexer(): "herdr" | "tmux" | "zellij" | "cmux" | "orca" | null {
	const e = process.env;
	if (e.HERDR_ENV) return "herdr";
	if (e.TMUX) return "tmux";
	if (e.ZELLIJ) return "zellij";
	if (e.CMUX_WORKSPACE_ID || e.CMUX_SURFACE_ID) return "cmux";
	if (e.ORCA_PANE_KEY) return "orca";
	return null;
}

/**
 * Spawn argv in a new Tern tab and return the block id. Verified against
 * Stencil Tern: `tern split PANE right --cwd DIR --keep-open -- ARGV...` puts
 * the command in a block beside the calling pane (a new tab when TERN_PANE is
 * unknown), and every window showing that session — including a laptop window
 * attached over tern's remote service — displays it live. argv goes straight
 * to execFile: no shell, no quoting. Failures fall through to the detached-pty
 * path.
 */
async function spawnInTern(args: string[], cwd: string, target?: string): Promise<string | null> {
	// Split beside the target pane. The extension runs in omp's shared kernel,
	// which has no TERN_PANE, so the agent passes its own pane id (from
	// `echo $TERN_PANE` in its shell); @focused is the last resort.
	const pane = target || process.env.TERN_PANE || "@focused";
	try {
		const { stdout } = await execFileP(
			"tern",
			["split", "--json", pane, "right", "--cwd", cwd, "--keep-open", "--", ...args],
			{ timeout: 20_000 },
		);
		const created = JSON.parse(stdout) as { block?: number | string };
		if (created?.block !== undefined) return String(created.block);
	} catch (err) {
		console.error(`[proxenos-bibtui] tern split failed, trying a new tab: ${String(err)}`);
	}
	try {
		const { stdout } = await execFileP(
			"tern",
			["new", "tab", "--json", "--cwd", cwd, "--keep-open", "--", ...args],
			{ timeout: 20_000 },
		);
		const created = JSON.parse(stdout) as { block?: number | string };
		return created?.block !== undefined ? String(created.block) : null;
	} catch (err) {
		console.error(`[proxenos-bibtui] tern spawn failed: ${String(err)}`);
		return null;
	}
}

/**
 * Resolve the app directory: it must contain an executable `bibtui` file.
 * A common mistake is passing the repo's PARENT (dir=/tmp where /tmp/bibtui
 * is the repo) — accept that shape too by looking one level down. Returns
 * null when neither shape holds, so callers can fail loudly instead of
 * spawning `./bibtui` into "Is a directory" (exit 126).
 */
function resolveAppDir(dir: string): string | null {
	const isExec = (p: string): boolean => {
		try {
			const st = fs.statSync(p);
			return st.isFile() && (st.mode & 0o111) !== 0;
		} catch {
			return false;
		}
	};
	if (isExec(path.join(dir, "bibtui"))) return dir;
	const nested = path.join(dir, "bibtui");
	if (isExec(path.join(nested, "bibtui"))) return nested;
	return null;
}

/**
 * Is the app instance this watcher launched still alive? spawnedPid covers
 * the pty path; tern sessions are checked against `tern ls` (we hold the
 * session id, not a pid). User-launched apps (launch:'none') are never
 * "provably alive" — callers must treat that as unknown.
 */
async function appAlive(w: Watcher): Promise<boolean> {
	if (w.spawnedPid) {
		try {
			process.kill(w.spawnedPid, 0);
			return true;
		} catch {
			return false;
		}
	}
	if (w.ternBlockId) {
		try {
			const { stdout } = await execFileP("tern", ["ls", "--json"], { timeout: 5000 });
			return stdout.includes(w.ternBlockId);
		} catch {
			return false;
		}
	}
	return false;
}

function sleep(ms: number): Promise<void> {
	const { promise, resolve } = Promise.withResolvers<void>();
	setTimeout(resolve, ms);
	return promise;
}

function streamsDir(): string {
	return path.join(
		process.env.XDG_DATA_HOME ?? path.join(os.homedir(), ".local", "share"),
		"proxenos",
		"streams",
	);
}

export default function proxenosBibtuiExtension(pi: ExtensionAPI) {
	const z = pi.zod;
	const watchers = new Map<string, Watcher>();

	/** Compact one-line summary used by both tiers. */
	function eventLine(e: Envelope): string {
		const body = JSON.stringify(e.body ?? {});
		return `${e.type} ${body.length > DIGEST_LINE_MAX ? body.slice(0, DIGEST_LINE_MAX) + "…" : body}`;
	}

	/** Free tier: queue a context line, flushed (debounced) as hidden nextTurn context. */
	function queueDigest(w: Watcher, line: string): void {
		w.digest.push(line);
		if (w.digest.length > DIGEST_MAX_LINES) {
			w.digest.splice(0, w.digest.length - DIGEST_MAX_LINES);
		}
		if (w.digestTimer) return;
		w.digestTimer = setTimeout(() => {
			w.digestTimer = undefined;
			if (w.digest.length === 0) return;
			const lines = w.digest.splice(0, w.digest.length).join("\n");
			pi.sendMessage(
				`[bibtui ${w.stream}] recent app activity (context only — no reply needed unless the user asks):\n${lines}`,
				{ deliverAs: "nextTurn" },
			);
		}, DIGEST_FLUSH_MS);
	}

	function handleRecord(w: Watcher, raw: string): void {
		const line = raw.trim();
		if (line === "") return;
		let e: Envelope;
		try {
			e = JSON.parse(line) as Envelope;
		} catch {
			return; // torn tail — the offset logic keeps the bytes for next poll
		}
		if (!e || typeof e.type !== "string") return;
		pi.logger.debug("px-bibtui: event", { stream: w.stream, type: e.type, src: e.src, wake: w.wake.has(e.type) });
		if (w.seen.has(e.id)) return;
		w.seen.add(e.id);
		if (w.seen.size > 1024) {
			const it = w.seen.values();
			for (let i = 0; i < 256; i++) {
				const v = it.next().value;
				if (v === undefined) break;
				w.seen.delete(v);
			}
		}
		// The agent's own sends echo onto the stream; never react to those.
		if (e.src.startsWith("agent:")) return;

		// Wake tier: user questions always wake; configured types wake per-event.
		if (e.type === "user.message" || w.wake.has(e.type)) {
			const body = e.body as { text?: string; ref?: string };
			const ref = body?.ref ? ` (at ${body.ref})` : "";
			const detail = body?.text ? ` ${body.text}` : "";
			pi.logger.debug("px-bibtui: wake", { stream: w.stream, type: e.type, id: e.id });
			pi.sendUserMessage(
				`[bibtui${ref}] ${e.type}:${detail}\n` +
					`(event ${e.id} on stream "${w.stream}" — context from the reading app; ` +
					`the user asks questions in THIS chat, so answer here, not on the stream)`,
			);
			return;
		}

		// Milestone tier: book transitions (the "quiz me at end of a book" trigger).
		if (w.milestones && e.type === "read.location") {
			const body = e.body as { book?: string; chapter?: number };
			const book = body?.book;
			if (book && w.lastBook !== undefined && book !== w.lastBook) {
				pi.sendUserMessage(
					`[bibtui] milestone: the user just finished ${w.lastBook} and is now in ${book} ` +
						`(chapter ${body?.chapter}). If you promised them something at a milestone ` +
						`(e.g. review questions), act on it now; otherwise acknowledge nothing and stay quiet ` +
						`unless there is something worth saying.`,
				);
			}
			if (book) w.lastBook = book;
			queueDigest(w, eventLine(e));
			return;
		}

		// Everything else: free tier. No turn, hidden context on the next one.
		queueDigest(w, eventLine(e));
	}

	function startWatcher(
		stream: string,
		filePath: string,
		spawnedPid?: number,
		wakeTypes?: string[],
		milestones?: boolean,
	): Watcher {
		const existing = watchers.get(stream);
		if (existing) {
			// Reconfigure in place — a second bibtui_open call is a settings update,
			// never a duplicate watcher or a second launch.
			if (wakeTypes) existing.wake = new Set(wakeTypes);
			if (milestones !== undefined) existing.milestones = milestones;
			return existing;
		}
		const w: Watcher = {
			stream,
			filePath,
			offset: 0,
			pendingCarry: "",
			timer: null as never,
			seen: new Set(),
			spawnedPid,
			wake: new Set(wakeTypes ?? []),
			milestones: milestones ?? false,
			digest: [],
			digestTimer: undefined,
		};
		watchers.set(stream, w);

		// Start at the tail: replaying history is the attach bridge's first-attach
		// job; here the agent watched bibtui launch, so waking only on what
		// happens next is the correct default.
		try {
			w.offset = Math.max(0, fs.statSync(filePath).size);
		} catch {
			w.offset = 0;
		}

		w.timer = setInterval(() => {
			let fd: number;
			try {
				fd = fs.openSync(filePath, "r");
			} catch {
				return; // not created yet / transient
			}
			try {
				const size = fs.fstatSync(fd).size;
				if (size < w.offset) {
					// replaced or truncated (compact / new generation): re-baseline at end
					w.offset = size;
					w.pendingCarry = "";
					return;
				}
				if (size === w.offset) return;
				const buf = Buffer.alloc(size - w.offset);
				fs.readSync(fd, buf, 0, buf.length, w.offset);
				const chunk = w.pendingCarry + buf.toString("utf8");
				w.pendingCarry = "";
				// leading-\n framing: records are \n-separated; the file never ends
				// with a newline in steady state, so the final segment may be the
				// newest complete record (EOF rule) or a torn prefix.
				let start = 0;
				for (;;) {
					const nl = chunk.indexOf("\n", start);
					if (nl === -1) break;
					handleRecord(w, chunk.slice(start, nl));
					start = nl + 1;
				}
				const tail = chunk.slice(start);
				if (tail === "") {
					w.offset = size;
					return;
				}
				try {
					JSON.parse(tail); // complete record, unterminated — deliverable now
					handleRecord(w, tail);
					w.offset = size;
				} catch {
					// torn prefix: keep offset at the tail start so nothing is skipped
					w.pendingCarry = tail;
					w.offset = size - Buffer.byteLength(tail);
				}
			} finally {
				fs.closeSync(fd);
			}
		}, POLL_MS);
		return w;
	}

	pi.registerTool({
		name: "bibtui_open",
		label: "Open bibtui",
		description:
			"Launch the bibtui Bible reading app attached to a shared proxenos event stream, " +
			"then watch the stream so you know what the user is reading. bibtui has no chat box: " +
			"the user asks their questions here, in this session. Prefer this over telling the " +
			"user to run bibtui themselves when they want to read together. " +
			"Use launch:'none' only if the user explicitly wants to run bibtui themselves — " +
			"the watcher still works, the user just opens bibtui in their own split/pane " +
			"with `./bibtui --proxenos <stream>`. " +
			"COST MODEL: nothing wakes a turn by default — navigation, notes and translation " +
			"changes arrive silently as hidden context on your next turn. " +
			"If the user asks for something at a milestone ('quiz me when I finish a book'), " +
			"call this again with milestones:true — it reconfigures in place without relaunching. " +
			"Calling it again with default launch also guarantees the app is open: if the previous " +
			"instance exited, a fresh one is launched automatically — never improvise tmux/screen " +
			"commands for this. " +
			"Never add chatty types to wakeTypes: each is a paid turn per event.",
		approval: "exec",
		parameters: z.object({
			dir: z.string().optional().describe("Directory containing the bibtui binary and bibles/ (default: current directory)"),
			stream: z.string().optional().describe("Proxenos stream name or <relay>:<stream> reference (default: bibtui-omp). Use a relay ref when the stream lives on a relay — e.g. the agent host's relay — so this UI-side app and the agent-side watcher share one stream across machines."),
			dataDir: z.string().optional().describe("Directory holding stream files when watching a relay's LOCAL data dir on this machine (its <data-dir>/streams). Ignored for relay refs (no local file — the watcher tails via CLI)."),
			translations: z.string().optional().describe("Space-separated translation codes, e.g. 'kjv web' (default: kjv)"),
			launch: z.enum(["auto", "none"]).optional().describe("'auto' (default) spawns bibtui in a detected multiplexer pane; 'none' only watches the stream — the user runs bibtui themselves"),
			block: z.string().optional().describe("Tern block to split beside (the pane you are talking in). Get it from your shell: echo $TERN_PANE — pass that id so the app opens beside this conversation, not in whatever tab happens to be focused."),
			wakeTypes: z.array(z.string()).optional().describe("Event types that start a model turn (default: only user.message). Keep minimal — every wake costs a turn. All other events arrive free as hidden context on the next turn."),
			milestones: z.boolean().optional().describe("Also wake when the user finishes a book (read.location book transition). For promises like 'quiz me at the end of each book'."),
		}),
		async execute(_toolCallId, params, _signal, _onUpdate, ctx) {
			const requestedDir = params.dir ?? process.cwd();
			const appDir = resolveAppDir(requestedDir);
			const dir = appDir ?? requestedDir;
			const stream = params.stream ?? "bibtui-omp";
			const translations = (params.translations ?? "kjv").split(/\s+/).filter(Boolean);
			const args = ["./bibtui", "--proxenos", stream, ...translations];

			// Reconfigure vs relaunch: an existing watcher is updated in place, but
			// "reconfigure" must never stand in for "the app is open" — omp's xd://
			// kernel retains extension state across sessions, so a watcher can
			// outlive its app. If the caller wants the app open (launch:'auto')
			// and the previously launched instance is dead, fall through and
			// launch a fresh one against the same watcher.
			const existing = watchers.get(stream);
			if (existing) {
				startWatcher(stream, "", undefined, params.wakeTypes, params.milestones);
				const wantsLaunch = (params.launch ?? "auto") === "auto";
				const alive = await appAlive(existing);
				if (!wantsLaunch || alive) {
					const state = alive
						? `bibtui is running (pid ${existing.spawnedPid ?? "?"}${existing.ternBlockId ? `, tern session ${existing.ternBlockId}` : ""})`
						: "watch-only mode — the user runs bibtui themselves";
					return {
						content: [
							{
								type: "text",
								text:
									`Watcher for stream "${stream}" reconfigured: ` +
									`wakeTypes=[${params.wakeTypes?.join(", ") ?? "user.message (default)"}], ` +
									`milestones=${params.milestones ?? "unchanged"}. ${state}. ` +
									"No new app instance was launched.",
							},
						],
						details: { stream, reconfigured: true, appAlive: alive },
					};
				}
				// Dead app, caller wants it open: clean stale handles and relaunch.
				existing.spawnedPid = undefined;
				existing.ternBlockId = null;
			}

			let launchedVia: string | null = null;
			let spawnedPid: number | undefined;
			let ternBlockId: string | null = null;
			const wantsLaunch = (params.launch ?? "auto") === "auto";
			if (wantsLaunch) {
				// Validate before creating any block/session — a wrong dir used to
				// spawn a doomed block ("bash: ./bibtui: Is a directory", exit 126).
				if (!appDir) {
					return {
						content: [
							{
								type: "text",
								text: `No executable bibtui in ${requestedDir} (nor ${path.join(requestedDir, "bibtui")}). ` +
									`Pass dir=<repo root containing the bibtui binary and bibles/>, e.g. dir=/tmp/bibtui. ` +
									`Nothing was launched.`,
							},
						],
						details: { stream, launched: false, dir: requestedDir },
					};
				}
				const mux = detectMultiplexer();
				if (mux && ctx.ui?.openTerminal) {
					try {
						// The request type is a per-multiplexer union (each with its own
						// capability-specific extras); the plain pane request below is valid
						// for every supported multiplexer, but TS can't prove that across the
						// union — hence the single cast at the call boundary.
						const req = { multiplexer: mux, placement: "pane", command: args, cwd: dir } as Parameters<
							NonNullable<typeof ctx.ui.openTerminal>
						>[0];
						await ctx.ui.openTerminal(req);
						launchedVia = `${mux} pane`;
					} catch (err) {
						pi.logger.debug("proxenos-bibtui: openTerminal failed, falling back", { err: String(err) });
					}
				}
				if (!launchedVia) {
					// Not inside an omp-recognized multiplexer: tern (omp lacks an
					// adapter, #15366) or a plain terminal. Try the tern CLI directly;
					// it no-ops fast if tern isn't installed, and we fall on.
					ternBlockId = await spawnInTern(args, dir, params.block);
					if (ternBlockId !== null) launchedVia = "tern block";
				}
				if (!launchedVia) {
					// Last resort: the TUI gets its own pty via script(1) so bubbletea
					// doesn't die on a closed stdout. Output goes nowhere — fine for
					// headless; the watcher still works, only typing needs a real pane.
					const child = spawn("script", ["-qec", args.join(" "), "/dev/null"], {
						cwd: dir,
						detached: true,
						stdio: "ignore",
					});
					child.unref();
					spawnedPid = child.pid;
					launchedVia = "detached pty";
				}
			}

			const filePath = (() => {
				// Relay ref (<relay>:<stream>): the stream file lives in the relay's
				// data dir on THIS machine (the agent host runs the relay), so the
				// file watcher reads it there; sends still go through the CLI with
				// the full ref. Bare names keep the default XDG streams dir.
				const colon = stream.indexOf(":");
				if (colon < 0) {
					return path.join(params.dataDir ?? streamsDir(), `${stream}.jsonl`);
				}
				const bare = stream.slice(colon + 1);
				const dir = params.dataDir ?? path.join(os.homedir(), ".local", "state", "proxenos-relay", "streams");
				return path.join(dir, `${bare}.jsonl`);
			})();
			let found = false;
			for (let i = 0; i < STREAM_WAIT_MS / 250; i++) {
				if (fs.existsSync(filePath)) {
					found = true;
					break;
				}
				await sleep(250);
			}
			if (!found) {
				const userCmd = `cd ${dir} && ./bibtui --proxenos ${stream}`;
				return {
					content: [
						{
							type: "text",
							text: wantsLaunch
								? `bibtui was launched but stream "${stream}" never appeared at ${filePath}. ` +
									`Check that ./bibtui exists in ${dir} and proxenos is on PATH.`
								: `No stream "${stream}" yet. Ask the user to run: ${userCmd} ` +
									`(in their own split/pane), then call bibtui_open again with launch:'none'.`,
						},
					],
					details: { stream, launched: false },
				};
			}

			const w = startWatcher(stream, filePath, spawnedPid, params.wakeTypes, params.milestones);
			w.ternBlockId = ternBlockId;

			const opening = launchedVia
				? `bibtui is open (${launchedVia}) on stream "${stream}".` +
					(launchedVia === "tern block"
						? ` It is a split beside this conversation in the same tern tab, live in every window attached to this session.`
						: "")
				: `Watching stream "${stream}" — waiting for the user to open bibtui in their own pane (${args.join(" ")}).`;
			const configLine =
				`Wake rules: none${w.wake.size ? " + " + [...w.wake].join(", ") : ""}` +
				(w.milestones ? " + book-finish milestones" : "") +
				". Other events arrive as silent context on your next turn (no reply needed).";
			return {
				content: [
					{
						type: "text",
						text:
							opening + "\n" + configLine + "\n" +
							"Reading activity arrives here as `[bibtui]` context lines (no reply needed). The user asks questions in this chat; answer here. To act in the app, send its accepts verbs on the stream:\n" +
							`  proxenos send ${stream} note.create '{"id":"…","book":"john","chapter":3,"verse":16,"text":"…"}' --as agent:omp\n` +
							`  proxenos send ${stream} nav.goto '{"book":"john","chapter":3,"verse":16}' --as agent:omp\n` +
							`Full vocabulary: proxenos tail ${stream} --tail 20 --pretty`,
					},
				],
				details: { stream, launchedVia, filePath },
			};
		},
	});

	pi.on("session_shutdown", async () => {
		for (const w of watchers.values()) {
			clearInterval(w.timer);
			clearTimeout(w.digestTimer);
			if (w.spawnedPid) {
				try {
					process.kill(-w.spawnedPid, "SIGTERM"); // script(1) made it a group leader
				} catch {
					/* already gone */
				}
			}
			if (w.ternBlockId) {
				// Only kill sessions the extension spawned, never user-created ones.
				try {
					await execFileP("tern", ["close", w.ternBlockId], { timeout: 5000 });
				} catch {
					/* already gone or tern absent */
				}
			}
		}
		watchers.clear();
	});
}
