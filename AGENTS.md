# AGENTS.md — herdr-context

For coding agents working on this repo. Read this, then `docs/DECISIONS.md`,
then the current spec. The code is the authority; the docs describe it.

herdr-context is a Go Bubble Tea plugin for the Herdr terminal multiplexer. It
draws a repo's Claude Code sessions as one tree and edits their context:
branch, squash, drop, move, and place a stored summary. The user-facing
behaviour is in `docs/GUIDE.md`.

## Docs

| Doc | Status |
|-----|--------|
| `README.md` | Current. The front page: what it is, install, keys, cost. Keep it tight. |
| `docs/GUIDE.md` | Current. The user guide: every key, marker and flow, safety, limits. |
| `docs/DECISIONS.md` | Current. The rulings that shape the code, and the deferred minors. |
| `docs/superpowers/specs/2026-09-23-context-editing-design.md` | **Current binding spec** (v3), with amendments; its status block says what no longer binds. |
| `docs/superpowers/plans/2026-09-23-context-editing.md` | Historical. The v3 plan's first nine tasks; later tasks were briefed outside it. |
| `docs/superpowers/specs/2026-09-28-undo-and-context-meter-design.md` | Current. Undo/redo and the context meter; amends the context-editing spec (§4). |
| `docs/superpowers/specs/2026-09-22-timeline-design.md` | Partly superseded. v2 timeline: summaries, fold-back, colours. v3 overrides §4, §5, §2's "never marked" trunk and §6b's "no trunk style". |
| `docs/superpowers/plans/2026-09-22-timeline-v2.md` | Historical. v2 plan. |
| `docs/superpowers/specs/2026-09-21-herdr-context-design.md` | Partly superseded. v1: discovery, store, adapter boundary, grafting. Its TUI section and keys are replaced. |
| `docs/superpowers/plans/2026-09-21-herdr-context-v1.md` | Historical. v1 plan. |
| `docs/HANDOFF-2026-09-23.md` | Historical. The state before the context-editing work. |

## Code map

| Package | What it is |
|---------|------------|
| `cmd/herdr-context` | The plugin binary. `open` (Herdr action: finds the pane's repo and session, opens the overlay) and `pane` (the overlay: wires herdr funcs into `tui.Run`). `prefix_test.go` asserts the tui/claude marker copies agree. |
| `internal/adapter` | Agent-neutral types (`Session`, `Node`, `Kind`, `Span`, `Edit`, `Spliced`) and the `Adapter` interface. Knows nothing about Claude. |
| `internal/claude` | Everything about Claude Code's transcript format. |
| `internal/herdr` | Shells out to the `herdr` CLI. Herdr owns panes and processes. |
| `internal/repo` | `Root`: a directory to the repo that owns it, so all worktrees share one tree. |
| `internal/store` | The only data herdr-context owns: `tree.json` per repo (edges, labels, summaries, replacements). |
| `internal/tree` | `Build`: sessions + store into the forest. `Trunk`: your path. No agent format knowledge. |
| `internal/tui` | The overlay. Must not import `internal/claude` (tests may). |
| `cmd/splicecheck` | Read-only check: splices every local transcript into a temp dir and validates each result. |
| `cmd/timelinecheck`, `cmd/graftcheck` | Helpers for the manual `scripts/verify-*.sh`. Not part of the plugin. |

Key files and functions:

- `internal/claude/graft.go` — `Select` (the ancestor chain kept by a graft), `Graft`, `GraftSeeded`, `writeSession` (0600 atomic), `checkVersion` (2.1 only).
- `internal/claude/line.go` — `buildLine`: a session's current line split into turns; `tipReaching`; `ErrNotOnLine`; `contextTokens` (§3.1 the line's last reply's context number).
- `internal/claude/splice.go` — `Widen` (a range to whole turns),
  `WidenBranch` (one turn for ⏎/`b`/branch here, on the line it is on even
  off the current one), `Splice` (squash / drop / merge / move in one operation; cross-line moves renew ids).
- `internal/claude/entries.go` — `Classify`, `Entries`, `SummaryPrefix` / `CompactionPrefix`, the `<pasted_content>` unwrap.
- `internal/claude/summarise.go` — `CompactPrompt`, `Summarise` (`claude -p --resume` on a throwaway graft, timeout, stderr scrubbed).
- `internal/claude/discover.go` — `Discover`, `ProjectsDir` (`CLAUDE_PROJECTS_DIR` overrides).
- `internal/claude/context.go` — `turnSizes` (§3.1 per-turn growth and its fallbacks), `breakdown` (§3.2 the line's context split by type), `outputTokens` / `turnBytes` / `blockBytes` (its helpers).
- `internal/herdr/herdr.go` — `AgentState` (pane + `agent_status`), `AgentForSession`, `AgentPrompt`, `ClosePane`, `Split`, `AgentStart`, `checkArg`.
- `internal/store/store.go` — `Load`, `Save` (merge with disk, `replaced_by` one-way), `Replace`, `Current` (skips undone versions), `CurrentOnDisk`, `Lineage`, `Group` (a move's two records), `Versions`, `SetLabel`, `AddSummary`.
- `internal/tree/tree.go` — `Build`: hiding replaced lines, re-attaching branches through `Current`, `attachPoint`, drop/move markers along the `replaces` chain.
- `internal/tui/view.go` — `Update` (all key handling), `View` (rows, header lines, path bar, footer), `renderRow`, `headerLine`, `cutNote`, `foldBackSeed`, `parseTitle`, `scrubbed`, `Run`.
- `internal/tui/edit.go` — `editConfirm`, `squashCmd`, `editCmd` (re-checks then splice), `changedElsewhere`, `busy`, `openTip` / `handoverCmd`, `placeChosen`, `foldAt`, `pickUp` / `putDown` / `carryCmd`, the summarising and review views.
- `internal/tui/undo.go` — `undoCmd` / `redoCmd` / `toggle` (`u` / `U`; a move group together; rolled back if `Save` fails).
- `internal/tui/model.go` — `Model`: `Rows`, folding, `Window`, ranges, `ScopeTo`, `RevealTip`.
- `internal/tui/sidebar.go` — `sidebarLines` (§4 the context sidebar: a header line, then one bar per type).

## Hard rules

- **Never modify a source transcript.** Every edit writes a new session file.
- **Never delete a session.** A replaced line is hidden (`replaced_by`), its file kept.
- **Files are written 0600, atomically** (temp file, chmod, rename) — transcripts and `tree.json`.
- **Never log message content.** Summaries, seeds and turn titles are all content: none may reach a status line, an error, stderr or a test log. Errors that may quote a seed go through `scrubbed`.
- **Never touch the user's live Herdr or `~/.config/herdr`.** No `herdr` commands, no plugin install, no config edits.
- **Stub herdr and claude in tests.** `stubHerdr` (`internal/herdr/herdr_test.go`), `stubClaude` (`internal/claude/summarise_test.go`), and the tui scenario harness `newWorld` (`internal/tui/scenario_test.go`), which puts stub `claude`/`herdr` first on `PATH` and sets `CLAUDE_PROJECTS_DIR` and `HERDR_PLUGIN_CONFIG_DIR` to temp dirs. Anything that can `exec` gets a stub on `PATH` — including during mutation testing.
- **Never run `claude`, `scripts/verify-*.sh`, `cmd/timelinecheck` or `cmd/graftcheck` without the user's explicit OK.** They spend budget.
- `cmd/splicecheck` is read-only and free, but takes about 30 minutes. Ask before a full run.
- **`bin/herdr-context.exe` is the user's live plugin.** Herdr runs it from this checkout. Rebuild only deliberately, when the user asks.
- **Push only when the user asks**, each time.

## Releasing

Users need no Go: the manifest's build step is `scripts/fetch.sh`, which
downloads the release binary for `herdr-plugin.toml`'s `version` and checks it
against `checksums.txt`, and builds from source only as a fallback.
`.github/workflows/release.yml` builds and attaches those binaries when a tag
is pushed. So: bump `version` in the manifest, commit, then push the tag
`v<version>` — the two must match, or installs of that version fall back to Go.
A linked checkout (`herdr plugin link`) runs no build: build it yourself with
`go build -o bin/herdr-context.exe ./cmd/herdr-context`.

## Testing

```bash
go vet ./... && go test ./...
```

- No test makes a network or model call.
- The scenario harness (`newWorld`) drives the real overlay against the real
  Claude adapter on real-shaped transcript files: `turnLines` writes a
  prompt, a Bash tool call, its result and a reply per turn. Set
  `w.durations = true` to end each turn with the `turn_duration` system entry
  Claude Code writes — a real turn's last entry is not a row, and a bug hid
  behind its absence once.
- Fixtures must be shapes Claude Code actually writes. A fixture that cannot
  occur is how tests end up unable to fail.
- Mutation-check risky logic: change it to a live-but-wrong expression (not a
  deletion that just fails to compile) and watch a test go red.
- Rendering is plain text (`renderRow`, `headerLine`, `cutNote`) so it can be
  asserted; lipgloss emits no colour without a tty, so colour itself is not
  testable.

## How work is done here

1. **Spec first.** The user agrees the behaviour; it is written into the
   context-editing spec as an amendment before any code.
2. **One implementer** per task, from a brief, test first.
3. **A task review** against the spec and the brief.
4. **Fix rounds** until the review is clean. Rulings (what was decided, why,
   cost if wrong) go in the ledger and, distilled, in `docs/DECISIONS.md`.

Where code and spec disagree, the code wins and the spec is amended — say so,
don't silently change either.

## Current state

Done (branch `feat/timeline-v2`): tree with family scope, path bar, session
headers and always-indented branches; ⏎ continue / `b` branch; squash with
summarising view, review and titles; drop; single-section move within and
across lines; `p` merge here / branch here / send to the live tip; live
handover on ⏎; busy and changed-elsewhere refusals; labels that follow their
turn; `u`/`U` undo and redo per line; the context meter on headers and in
the squash review. Live-verified by the user: splice resume, ⏎ handover, merge then
continue, and delivery to a live tip (one message, wrapped in
`<pasted_content>`, now unwrapped by the classifier).

Open:

- The herdr `agent_status` word at a permission prompt is unverified. The busy
  guard allows only `idle` (and empty), so an unknown word refuses rather than
  risks a running turn.
- Deferred minors: see `docs/DECISIONS.md`.
