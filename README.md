<div align="center">

# herdr-context

**Your Claude Code conversations as one tree — and the context, editable**

<a href="https://herdr.dev"><img src="https://img.shields.io/badge/herdr-%E2%89%A5%200.9.0-D97757?style=flat-square" alt="Herdr 0.9.0+"></a>
<img src="https://img.shields.io/badge/platforms-linux%20%C2%B7%20macos-555?style=flat-square" alt="Linux · macOS">
<a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-555?style=flat-square" alt="MIT"></a>

</div>

---

## What it is

A [Herdr](https://herdr.dev) plugin that draws every Claude Code session in a
repo as one tree: each session, each branch, each turn. From that tree you can
edit a session's context: branch from any turn, squash a stretch into a
summary, drop it, move a turn to another line, and undo any of it. It never
changes a transcript. Every edit writes a new session and keeps the old one.

## Why

A long Claude Code session fills its context with old detours and huge tool
outputs until autocompact decides for you what to forget. `/compact` and
`/rewind` are all-or-nothing. herdr-context shows where the context goes, turn by
turn and by type, and lets you trim exactly the part that no longer earns its
place, then continue from the result in any pane.

## What you get

```
  ▎ 1a2b3c4d · 84k                                          │context  84k
  ▎ ⋮ compacted by Claude Code · 12 turns · ~40k            │thinking     ▮▮▮▯▯▯▯▯ 32%
  ▎ ▸ user: add the retry budget  (14)  31k                 │tool calls   ▮▮▯▯▯▯▯▯ 26%
  ▎   ↳ 5e6f7a8b · 12k                                      │tool results ▮▮▯▯▯▯▯▯ 25%
  ▎     ▸ user: try it with a token bucket instead  (6)  9k │replies      ▮▯▯▯▯▯▯▯  7%
  ▎ ⤶ squashed: dispatcher refactor, tests green  (1)  2k   │typed        ▮▯▯▯▯▯▯▯  7%
 >▎ ▸ user: now wire it into the CLI  (9)  18k  ● current   │injected     ▯▯▯▯▯▯▯▯  3%
```

- **The whole family at a glance.** Branches are indented under the turn they
  left. A `▎` bar marks your path. Each session header carries its context
  size.
- **Where the context goes.** Every turn shows its size, and a sidebar splits
  the session into thinking, tool calls, tool results, replies, typed text and
  injected text. `]` and `[` jump through the ten heaviest turns.
- **Edits that fit the tree.**
  - **squash** replaces a stretch with a summary, which you review before it
    lands.
  - **drop** cuts a stretch.
  - **move** takes a turn to another place or another line.
  - **place** puts a stored summary anywhere.
  - `u` / `U` undo and redo each line's edits.
  - While you select a range, the sidebar previews what an edit would leave.
- **Claude Code's own compaction, shown honestly.** The stretch a `/compact`
  summarised folds into one row, so the tree shows what the agent actually
  reads.
- **Safe by construction.**
  - Source transcripts are never modified and sessions are never deleted.
  - Edits are refused while the session's agent is busy.
  - No edit opens a pane: ⏎ is what moves you.

## Quick start

```sh
herdr plugin install Somliga/herdr-context
```

The install downloads a prebuilt binary for your platform and checks it
against the release's checksum. It needs `curl` or `wget`; if neither can
fetch one and Go 1.27+ is installed, it builds from source instead. Then bind a
key in `~/.config/herdr/config.toml`:

```toml
[[keys.command]]
key = "prefix+t"
type = "plugin_action"
command = "herdr-context.open"
```

Press `prefix+t` in a pane inside a repository to open the tree. To update,
run the install again.

> [!NOTE]
> Requires Herdr 0.9.0+ and Claude Code 2.1.x. Linux and macOS, on x86-64
> or ARM64. The
> transcript format is undocumented: herdr-context refuses to write rather than
> guess when it meets another version.

### Or hand it to an agent

```text
Install the herdr-context plugin for Herdr on this machine.

1. herdr plugin install Somliga/herdr-context
2. Add this key binding to ~/.config/herdr/config.toml, unless prefix+t is
   already bound (then ask me for another key):

   [[keys.command]]
   key = "prefix+t"
   type = "plugin_action"
   command = "herdr-context.open"

3. Check it took: `herdr plugin list` shows herdr-context as enabled.

Do NOT run `herdr server stop` or kill the Herdr process: that ends every
program in every pane, including whatever is running you.
```

## Keys

| Key | Action |
| --- | --- |
| `↑` `↓` · `←` `→` | Move · fold / unfold |
| `⏎` | Continue here: resume the line, or start a new session from this turn |
| `b` | Branch from this turn |
| `s` | Select a range, then `squash · drop` |
| `m` | Move this turn |
| `p` | Place a stored summary here |
| `u` / `U` | Undo / redo this line's last edit |
| `]` / `[` | Next / previous heavy turn |
| `c` · `a` · `f` | Sidebar · scope (family / all) · filter (all / typed only) |
| `L` | Label this turn |
| `esc` | Cancel, or close |

The footer always shows what the keys do right now.

## Cost

Only squash bills: one `claude -p` call per summary, on your normal Claude
Code login, and the confirm dialog says how much it reads first. Branch,
drop, move, merge and undo are local file writes.

## Docs

- [User guide](docs/GUIDE.md): every marker, the squash / move / place flows,
  handover, safety and limits.
- [Decisions](docs/DECISIONS.md): why it works the way it does.
- [AGENTS.md](AGENTS.md): for coding agents working on this repo.

## License

[MIT](LICENSE)
