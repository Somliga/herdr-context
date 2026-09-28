# herdr-tree — undo/redo and the context meter

Date: 2026-09-28
Status: draft for review
Scope: sub-project A of the next round (A: undo + meter; B: code branching;
C: other agents). Builds on `2026-09-23-context-editing-design.md`, which
stands unchanged except where §4 below says so.

---

## 1. Why

Edits (squash, drop, move, merge here) already keep the version they replace:
nothing is deleted, the old line is only hidden. Undo turns that into
something the user can act on — edits become safe to try. The context meter
says how big each line's context is, so the user knows when a squash is worth
it and what it saved.

## 2. Undo and redo

### 2.1 Keys

- **`u`** on any row of a line undoes that line's most recent edit.
  Pressing it again undoes the one before, back to the line's original.
- **`U`** redoes, one version at a time, most recently undone first.
- Both are swallowed while a range, a menu, a confirmation, a review or a
  move is in progress. Neither asks for confirmation: both are reversible by
  the other key.
- The footer gains `u undo  U redo` (within the 80-column budget, §5.3d/§2.9
  of the context-editing spec's footer rules).

### 2.2 The model

An edit writes a new session N whose record says `replaces: O`, and sets
`replaced_by: N` on O. Undo adds one optional field to N's record:

- **`undone: true`** — N is set aside; O is current again.

The **current version** of a line is the version that nothing *non-undone*
replaces. Concretely, `Resolve` and the tree's hiding rule skip replacements
marked `undone`:

- O is hidden iff `O.replaced_by` names a present session that is not
  `undone`.
- An `undone` session is hidden.
- Resolving O follows `replaced_by` only through non-undone sessions.

**Undo** on a line whose current version is V (V has `replaces: P`): set
`undone: true` on V. P becomes current. If V has no `replaces`, there is
nothing to undo: status `nothing to undo on <id8>`.

**Redo** on a line whose current version is P: if `P.replaced_by` names a
session R with `undone: true`, clear R's flag — R is current again. Otherwise
status `nothing to redo on <id8>`. Undo also records `undone_at` (RFC 3339)
beside `undone`, for merging (below). Repeated undo walks back (V, then P, …);
repeated redo walks forward along the same `replaced_by` links.

**A new edit after undo** replaces the current version P as any edit does:
`P.replaced_by` now names the new session, which is not undone, so there is
nothing to redo: the undone sessions keep their `replaces: P` and
`undone: true` and are off the redo path. Nothing is deleted.

The one-way merge rule (context-editing §5.1: `replaced_by` is never cleared
by a stale save) extends: `undone` is merged by `undone_at` — the later
timestamp wins — so two overlays converge.

### 2.3 Moves undo as one edit

A cross-line move writes two records: the target's new version and the
source's new version. Both carry **`edit: <id>`** (a new random id per
edit). `u` or `U` on either line applies to every record with that edit id,
together: the moved turn is back where it was, never in both lines or in
neither. A same-line move writes one record and needs no group. Other edits
write one record; they may carry an `edit` id too, harmlessly.

Undoing a group requires each of its lines to be at that version: if either
line has been edited since (its current version is not the group's record),
the undo is refused: `<id8> was edited since — undo that first`.

### 2.4 Branches off an undone version

A branch grafted from a version that is later undone re-attaches to the now
current version at the same turn uuid, when that version has it; otherwise
it renders as a root marked `from a removed stretch`. This is the existing
re-attach rule (context-editing §5.3) with `Resolve` extended to walk from an
undone session **back** through its `replaces` to the current version.

### 2.5 Safety

- No pane opens or closes (context-editing §6).
- The busy check (§6.1 allowlist) runs on any live pane holding **any
  version** of the line; busy → refused, nothing written.
- The changed-in-another-overlay check (§5.1) runs before writing: if the
  store on disk shows a different current version than this overlay saw,
  refused: `this line was changed in another overlay — reopen the tree`.
- `⏎` on the current version hands over (§6.2) from a pane holding any other
  version of the line, including an undone one.
- Status after undo: `undone <kind> on <id8> — ⏎ on it to continue there`;
  after redo: `redone <kind> on <id8> — ⏎ on it to continue there`, where
  `<kind>` is the edit's word (squash, drop, move, merge). The tree reloads
  with the cursor on the restored line's tip.
- Undo and redo write only the store. No transcript is written or read beyond
  the usual rebuild.

## 3. Context meter

### 3.1 The number

A line's context is what its **last reply on the current line** read: the
last assistant entry on the tip's chain that carries `message.usage`, summed
as `input_tokens + cache_read_input_tokens + cache_creation_input_tokens`.
The split entries of one reply share one usage; it is counted once.

If the line has no such reply (a new branch, a squashed line not yet replied
to, an older transcript format), the number is **absent** — never estimated.

**Amended 2026-09-28 (user, after live testing):** an edited line with no
reply of its own shows an **estimate, marked `~`** (`b68a21b1 · ~31k`),
because the tree is usually read right after an edit. Each edit (squash,
drop, move, merge) computes it at splice time: the source line's shown number
(real or `~`) × kept bytes ÷ line bytes + seed text bytes ÷ 4, where kept
bytes are what was copied from transcripts (a cross-line move's target adds
the moved turn). It is stored as `est_tokens` on the new record. The next
reply's real number replaces it. Splices strip `usage` from copied entries, so
a stale real number never shows. A graft keeps its copied usage: a branch
shows its fork point's real number.

### 3.2 Where it shows

- **Session header** (context-editing §5.3e): `↳ 1a2b3c4d · 84k`, muted,
  after the id. Formatting: `<1k` below 1000, `12k`, `889k`, `1.2M`. When
  truncating for width the id is kept before the number.
- **Squash review** (§2.9): below the `Then:` line, `context 84k → ~31k` —
  the estimate is the current number × (1 − range bytes ÷ line bytes) +
  summary bytes ÷ 4, marked `~`. Omitted when the line has no number.
- Nowhere else. The header of a freshly edited line shows its `~` estimate
  (§3.1, amended) until its next reply.

### 3.3 Where it lives

The Claude adapter computes it while parsing each transcript in `Discover`
(no extra read) and sets a new `adapter.Session.ContextTokens int` (0 =
unknown). The tree carries it to the header row; the TUI only formats it. The
squash review's byte shares come from the transcript the squash already
parses.

## 4. Changes to the context-editing spec

- §5.1/§5.2 hiding and `Resolve` skip `undone` replacements (§2.2 above).
- §5.3 re-attachment walks back from an undone version (§2.4).
- §5.3e session headers may carry the context number (§3.2).
- §2.9 squash review gains the context estimate line (§3.2).

## 5. Testing

- Store: undo/redo field semantics, `Resolve` and hiding with undone
  sessions, `undone_at` merge between two stores, group lookup by `edit`.
- Tree (real `Build`): undo shows the previous version; three undos walk
  back three versions; redo walks forward; a new edit after undo replaces the
  current version and redo is no longer offered; a branch off an undone
  version re-attaches or becomes a marked root; cut/move markers follow the
  current version.
- TUI scenarios (real files, `newWorld`, `world.durations`): `u`/`U` after
  squash, drop, merge and a same-line move; a cross-line move undone from the
  target and from the source restores both lines; a group undo refused when
  one line was edited since; busy and changed-elsewhere refusals; `⏎`
  handover from a pane on an undone version; statuses; cursor on the
  restored tip.
- Meter: the sum from real-shaped usage (split reply entries counted once);
  absent when no usage; header formatting and width truncation; the squash
  review estimate and its omission.
- Mutation-check each rule (skip `undone` in hiding; undo only one line of a
  group; count split entries twice).

## 6. Open questions

None blocking. Possible later: a version-history list (restore any version,
not only step back), and a per-turn size.
