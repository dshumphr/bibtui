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
 *   3. Agent replies via `proxenos send <stream> answer … --as agent:omp
 *      --reply-to <id>`; bibtui's ask overlay renders it (reply_to matching).
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
 * Spawn argv in a new tern tab via the tern CLI and return the block id.
 * omp has no tern launch adapter yet (can1357/oh-my-pi#15366, confirmed
 * absent from providers.ts on main), but the tern CLI itself can spawn
 * blocks — the CLI talks to tern's daemon, so this works from inside an
 * omp pane running in tern. Syntax per the #15366 workaround; unverified
 * here (no tern install on the dev box) — failures fall through to the
 * detached-pty path, never abort the launch.
 */
async function spawnInTern(args: string[], cwd: string): Promise<string | null> {
	try {
		const { stdout } = await execFileP("tern", ["new", "tab", "--json", "--cwd", cwd, "--keep-open", "--", ...args], { timeout: 10_000 });
		const parsed = JSON.parse(stdout) as { id?: string | number; block?: string | number };
		const id = parsed?.id ?? parsed?.block;
		return id !== undefined ? String(id) : null;
	} catch (err) {
		console.error(`[proxenos-bibtui] tern new tab failed: ${String(err)}`);
		return null;
	}
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
					`(event ${e.id} on stream "${w.stream}" — if this expects a reply, send it via: ` +
					`proxenos send ${w.stream} answer '{"text":"…","refs":[…]}' ` +
					`--as agent:omp --reply-to ${e.id} — it renders in bibtui's ask overlay)`,
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
			"then watch the stream: the user's questions typed in bibtui (press ?) are injected " +
			"into this session so you can answer them in-context. Prefer this over telling the " +
			"user to run bibtui themselves when they want to read together. " +
			"Use launch:'none' only if the user explicitly wants to run bibtui themselves — " +
			"the watcher still works, the user just opens bibtui in their own split/pane " +
			"with `./bibtui --proxenos <stream>`. " +
			"COST MODEL: only user.message wakes a turn by default; every other event (navigation, " +
			"notes, translation changes) arrives silently as hidden context on your next turn. " +
			"If the user asks for something at a milestone ('quiz me when I finish a book'), " +
			"call this again with milestones:true — it reconfigures in place without relaunching. " +
			"Never add chatty types to wakeTypes: each is a paid turn per event.",
		approval: "exec",
		parameters: z.object({
			dir: z.string().optional().describe("Directory containing the bibtui binary and bibles/ (default: current directory)"),
			stream: z.string().optional().describe("Proxenos stream name (default: bibtui-omp)"),
			translations: z.string().optional().describe("Space-separated translation codes, e.g. 'kjv web' (default: kjv)"),
			launch: z.enum(["auto", "none"]).optional().describe("'auto' (default) spawns bibtui in a detected multiplexer pane; 'none' only watches the stream — the user runs bibtui themselves"),
			wakeTypes: z.array(z.string()).optional().describe("Event types that start a model turn (default: only user.message). Keep minimal — every wake costs a turn. All other events arrive free as hidden context on the next turn."),
			milestones: z.boolean().optional().describe("Also wake when the user finishes a book (read.location book transition). For promises like 'quiz me at the end of each book'."),
		}),
		async execute(_toolCallId, params, _signal, _onUpdate, ctx) {
			const dir = params.dir ?? process.cwd();
			const stream = params.stream ?? "bibtui-omp";
			const translations = (params.translations ?? "kjv").split(/\s+/).filter(Boolean);
			const args = ["./bibtui", "--proxenos", stream, ...translations];

			// Reconfiguration short-circuit: an existing watcher is updated in place
			// (new wake rules / milestones) and bibtui is NOT spawned again.
			if (watchers.has(stream)) {
				startWatcher(stream, "", undefined, params.wakeTypes, params.milestones);
				return {
					content: [
						{
							type: "text",
							text:
								`Watcher for stream "${stream}" reconfigured: ` +
								`wakeTypes=[${params.wakeTypes?.join(", ") ?? "user.message (default)"}], ` +
								`milestones=${params.milestones ?? "unchanged"}. ` +
								"No new app instance was launched.",
						},
					],
					details: { stream, reconfigured: true },
				};
			}

			let launchedVia: string | null = null;
			let spawnedPid: number | undefined;
			let ternBlockId: string | null = null;
			const wantsLaunch = (params.launch ?? "auto") === "auto";
			if (wantsLaunch) {
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
					ternBlockId = await spawnInTern(args, dir);
					if (ternBlockId !== null) launchedVia = "tern tab";
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

			const filePath = path.join(streamsDir(), `${stream}.jsonl`);
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
					(launchedVia === "tern tab"
						? ` Blocks live in the tern daemon on this machine — they render in the user's tern window when attached (tern ssh <this-host>); attach later and they come back with their scrollback.`
						: "")
				: `Watching stream "${stream}" — waiting for the user to open bibtui in their own pane (${args.join(" ")}).`;
			const configLine =
				`Wake rules: user.message${w.wake.size ? " + " + [...w.wake].join(", ") : ""}` +
				(w.milestones ? " + book-finish milestones" : "") +
				". Other events arrive as silent context on your next turn (no reply needed).";
			return {
				content: [
					{
						type: "text",
						text:
							opening + "\n" + configLine + "\n" +
							"The user's questions typed in bibtui's ask overlay (?) will arrive here as `[bibtui]` messages — answer them in this session and also send the answer to the stream so bibtui renders it:\n" +
							`  proxenos send ${stream} answer '{"text":"…","refs":["John 3:16"]}' --as agent:omp --reply-to <question-id>\n` +
							"note.create also works (stable id → overwrite):\n" +
							`  proxenos send ${stream} note.create '{"id":"…","book":"john","chapter":3,"verse":16,"text":"…"}' --as agent:omp\n` +
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
				// Only kill blocks the extension spawned, never user-created ones.
				try {
					await execFileP("tern", ["kill", w.ternBlockId], { timeout: 5000 });
				} catch {
					/* already gone or tern absent */
				}
			}
		}
		watchers.clear();
	});
}
