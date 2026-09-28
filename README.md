# herdr-tree

A Herdr plugin that shows a repo's Claude Code conversations as one tree —
every session, every branch, every turn — and lets you edit their context:
branch from any turn, squash a stretch into a summary, drop it, move a turn to
another line, or place a stored summary anywhere.

## Install

```bash
herdr plugin install Somliga/herdr-tree
```

Herdr builds the plugin on your machine during the install, so you need
**Go 1.27 or later** on your `PATH` (Herdr reports a failed build but does
not install Go for you). Linux and macOS. To update, run the install again.

Then bind a key in `~/.config/herdr/config.toml`:

```toml
[[keys.command]]
key = "prefix+t"
type = "plugin_action"
command = "herdr-tree.open"
```

**Developing it:** link a checkout instead, and rebuild after changes —
Herdr runs `./bin/herdr-tree.exe` from the linked directory:

```bash
herdr plugin link /path/to/herdr-tree
go build -o bin/herdr-tree.exe ./cmd/herdr-tree
```

## Keys

| Key | Action |
|-----|--------|
| ↑ ↓ | Move |
| ← → | Fold / unfold |
| ⏎ | **Continue here.** On a line's tip: resume it (or hand over, below). On an earlier turn: confirm, then a new session continuing from the end of that turn opens in a pane |
| b | **Branch** from the end of this turn. Opens nothing, asks nothing; the cursor lands on the new branch |
| s | **Select** a range: fixes its end here; move to its start and press `s` or ⏎ to open `squash · drop` |
| m | **Move** this turn (one section): ⏎ puts it after the turn under the cursor, esc puts it back |
| p | **Place a summary** at this turn: pick a stored summary, then merge here / branch here |
| L | Label this turn (empty clears) |
| a | Scope: this session's family ↔ all sessions |
| f | Filter: all entries ↔ only what a person typed |
| esc | Cancel the range, move or dialog in progress; otherwise close (`q` also closes) |

The footer always shows what the keys do right now; while a range or a move is
in progress it changes to say so.

## What you see

- **Session headers.** Each session's first row has a header line above it:
  `<id>` for a root line, `↳ <id>` for a branch. A branch whose turn was
  squashed or dropped away shows as a root marked `from a removed stretch`.
- **Your path.** A cyan `▎` in the left margin marks every row on the path to
  the session you are in. The default scope is that session's family: its root
  line and every branch in it.
- **Indentation means "branched here".** A branch is indented one level under
  the turn it came from; the parent line continues at its own depth.
- **Markers:**
  - `⤶ squashed …` (blue) — a stretch of this line replaced by its summary.
  - `⤶ merged from <id> …` (orange) — a summary brought in from another line.
  - `✂ N turns dropped before/after this` (muted) — a drop.
  - `⇢ 1 turn moved to <id>` (muted) — where a turn moved into another line
    used to be.
  - `⇠ moved from <id>` (muted) — the moved turn in its new line. A move
    within one line is a reorder and leaves no marker.
  - `● current` — the tip of the session you are in.

## Squash

Select a range with `s`, press ⏎, choose **squash**.

1. **Confirm.** The dialog names the turns (widened to whole turns) and the
   cost: the model reads the session up to the end of the range, and that
   whole prefix is billed.
2. **Summarising view.** Spinner and elapsed time. `ctrl+c` leaves, but the
   call is already billed.
3. **Review.** The summary is shown in full, with its title above it and what
   will happen below it. `⏎` commits; `esc` cancels — nothing is written, and
   the summary stays stored for `p`.

On commit a new session replaces the line: the stretch becomes one
`⤶ squashed: <title>` row. The model titles each squash itself (no extra
call); if it doesn't, the row reads `⤶ squashed <from>..<to>`.

**Drop** is the same without a summary: free, and no note is left in the
conversation.

## Move

`m` picks up one section — a prompt, or a `⤶` row, with everything under it.
To move a stretch, squash it first, then move the `⤶` row. ⏎ puts it after the
turn under the cursor, in the same line or another one. Moved into another
line, a `⤶ squashed` row becomes `⤶ merged from <source>`. Nothing is billed.

## Place a summary (`p`)

Every squash's summary is stored. `p` picks one and puts it at a turn:

- **At the tip of your session, while its agent is live**, it is sent to the
  agent as your next message. Nothing is copied.
- **Anywhere else**, choose **merge here** (inserted after this turn,
  everything after it kept; replaces the line) or **branch here** (a new line
  ending at this turn plus the summary; the old line stays).

## Edits never open a pane

Squash, drop, move, merge and `b` only write; the tree reloads with the cursor
on the result. ⏎ is what moves you. ⏎ on a line that replaced one still open
in a pane asks once, opens the new line focused, and then closes the old pane
(text typed but not sent there is lost).

## Safety

- Source transcripts are never modified; every edit writes a new session.
- Sessions are never deleted. A replaced line is hidden from the tree, its
  file kept on disk.
- An edit is refused while the session's agent is busy (anything but idle),
  and if the line was changed in another overlay since this one loaded.

## Cost

Only summaries bill: one `claude -p` call per squash, on your normal Claude
Code login. Branch, drop, move and merge are local file writes. The first
message in an edited session is sent with a cold prompt cache.

## Limits

Claude Code's transcript format is undocumented. herdr-tree is verified
against Claude Code 2.1.x and refuses to write rather than guess when it sees
another version. Turns before a native `/compact` still show in the tree,
and ⏎, `b` and branch here work on them: the new line carries the history
before the compact, up to the end of that turn. Squash, drop and move on them
are refused ("that entry is not on this session's current line"): those
rewrite the current line, which no longer holds them.
