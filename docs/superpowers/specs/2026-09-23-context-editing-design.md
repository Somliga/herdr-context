# herdr-tree — context editing

Date: 2026-09-23
Status: current and binding, as amended through 2026-09-28. Implemented on
`feat/timeline-v2`, including the second whole-branch review's fixes. Where
the text below and the code differ, the code is authoritative. In short, what is current:

- The range menu is `squash · drop`. **squash into… is removed** (§0 note):
  §2.7 is kept only as a marker, and every other mention of it has been taken
  out of the text.
- **move is one section** (`m`, §2.8); to move a stretch, squash it first.
- ⏎ continues here, `b` branches (§2.5c); **no edit opens a pane**, ⏎ moves
  you and hands over a replaced line's pane (§6.2).
- Squash shows a summarising view and a review before it writes (§2.9) and
  titles itself (§2.10); every summary uses the handover prompt.
- `p` places a summary: sent to the live tip, or merge here / branch here
  (§2.5); a delivered summary is recognised through `<pasted_content>` (§2.5d).
- Tree: family scope, `▎` path bar, always-indented branches, session header
  lines, `⤶` rows as section heads (§5.3b–§5.3f).
- §6.1 and §5.4 now match the code: the busy guard is an allowlist (only
  `idle`, or no agent, is safe) and names the status; the drop marker reads
  `✂ N turns dropped before this` / `after this`.
- ⏎, `b` and branch here work from any turn with a row, including one before
  a native `/compact` (§2.5b); edits still need the current line (§3.3).
- A move within the line leaves no marker; a mid-line squash or merge lands
  the cursor on its seed (§2.8, §5.3f).
- Decisions and deferred minors: `docs/DECISIONS.md`.

Earlier status: approved; amended 2026-09-23 after the first manual run (§2.2,
§2.3, §2.5, §4, §6): no edit ever opens a pane by itself
Scope: v3. Builds on the timeline spec (`2026-09-22-timeline-design.md`),
which stands unchanged except where noted in §9.

---

## 0. Vocabulary

User-visible terms follow git: **squash** replaces a range with its summary,
**drop** removes a range outright, **merge** places a stored summary at a
turn, **branch** starts a new line, and **checkout** moves onto a
replacement. `rebase` is deliberately unused: nothing here replays turns onto
another base.

> **Amended 2026-09-28:** `squash into…` is removed. To carry a stretch into
> another line, squash it, then move (§2.8) the `⤶` row there. A `⤶ squashed`
> row moved into another line is relabelled `⤶ merged from <source8>` (§2.8).
> The report-style summary prompt goes with it; every squash uses the handover
> prompt. §2.7 is kept only as a marker of the removed operation; the other
> passages that described it were amended in place.

## 1. The model

v2 could produce a summary and place it, but placing it at a turn always
rewound the line to that turn: everything after it was left behind on the old
line. Compacting a stretch in the middle of a conversation was not possible,
and even compacting "what I just did" needed two steps and a graft point
picked by hand, one turn before the range.

v3 lets the user edit a line's context directly. Select a range, then:

- **squash** — the range is replaced by its summary;
- **drop** — the range is removed.

And `p`, folding a stored summary in at a turn, gains:

- **merge here** — the summary goes in after that turn and everything after
  it is kept.

All three are one operation: **splice**. Keep the line up to its tip, drop a
range of whole turns (possibly empty), optionally put one seeded entry in its
place, and re-attach what followed.

```
before   t1 ── t2 ── t3 ── t4 ── t5 ── t6          ← tip
                      └── range ──┘

squash   t1 ── t2 ── ⤶ squashed t3..t5 ── t6
drop     t1 ── t2 ── t6                              (tree shows ✂ 3 turns dropped)
merge    t1 ── t2 ── ⤶ merged from … ── t3 ── … ── t6  (merge after t2)
```

A splice writes a **new session** and **replaces** the old line with it: the
old session is hidden from the tree and its file is kept on disk (§5). This is
different from branching, which leaves both lines visible.

## 2. Interaction

### 2.1 Selecting

`s` fixes the range's end at the cursor; the user moves to the start and
presses `s` or `⏎`. `esc` cancels. Unchanged from v2 §4, except that the
footer reads `s select` rather than `s summarise`.

### 2.2 The range menu

With a range fixed, `⏎` opens a menu over it:

```
squash · drop · esc back
```

- **squash** — the range is replaced by its summary in its own
  line (a compaction).
- **drop** — the range is removed.

**No edit opens a pane.** Every edit only writes; the tree reloads with the
cursor on the result, and moving there is the user's own `⏎` (§6).

### 2.3 One confirmation

Choosing an option opens one dialog that states everything the operation will
do. Nothing asks again afterwards; after `⏎` the operation runs to completion
or stops at the first failure (§6).

- **squash**: v2's cost text ("the model reads this session up
  to the end of the range … that whole prefix is billed"), then:
  `Then: turns <a>–<b> are replaced by the summary · a new session replaces
  this line in the tree (the old one is hidden, kept on disk)`.
- **drop**: `Removes turns <a>–<b>. Costs nothing. No note is left in the
  conversation.` plus the same replacement line.

The turns named are the range **after widening** (§3.1), so the user sees
exactly what goes.

### 2.4 Refusals

Each refusal is a status line with its reason. The range stays fixed so it can
be adjusted.

- The range crosses sessions (v2, unchanged).
- The session's live agent is busy (§6.1).
- A drop would remove every turn.
- The range is not on the chain up to the session's tip (§3.3).

### 2.5 `p` — place a summary

After a summary is picked:

- **At the tip of a session open in a live pane**: unchanged from v2 §5 — the
  summary is delivered as a message. No menu.
- **Anywhere else**, a two-option menu:
  - **merge here** — splice with an empty range after this turn, seeded with
    the summary. Replaces the line (§5). Opens nothing.
  - **branch here** — v2's seeded graft: a new line that ends at this turn plus
    the summary. The old line stays visible. Opens nothing (v2 opened a pane;
    the user now presses `⏎` on it).

Merging far back in a long line gives the model a history in which later
turns follow a summary they were written without. That is usually harmless and
is not warned about.

"Merge and remove everything after" is not offered: it is a drop plus a
merge, and `s` → drop covers it.

### 2.5b Branching starts after the whole turn

`⏎` on any row of a turn that is not the line's tip, `b`, and **branch here**
(from `p`), graft at the **last entry of that turn** — the
same whole-turn rule as §3.1 — not at the row's own entry. Grafting at a
prompt would leave it unanswered, so the resumed agent answers it again, and
a seed placed after it would make two user messages in a row. `⏎` on a line's
tip still resumes it unchanged.

> **Amended 2026-09-28:** a branch works from any turn that has a row, not
> only from the session's current line. A turn before a native `/compact`
> (§3.4), or on a stretch the session rewound away from, is widened on the
> line it is on: the one ending at the latest entry, in file order, whose
> ancestor chain holds it (`WidenBranch`). The graft then carries that
> history up to the end of the turn. Only if no such line exists is the graft
> at the row's own entry. Squash, drop, move and merge here still refuse such a turn
> (§3.3): they rewrite the current line, which does not hold it.

### 2.5c `⏎` continues, `b` branches

- **`⏎` — continue here.** On a line's tip: resume it (or the §6.2 handover).
  On an earlier turn: continue from the end of that turn — a graft (§2.5b)
  that is then opened in a pane, after a confirmation. Unchanged behaviour,
  now named for what it does.
- **`b` — branch here.** Grafts from the end of the turn under the cursor
  (§2.5b), opens nothing, asks nothing: the tree reloads with the cursor on
  the new branch, status `branched <new8> — ⏎ on it to open it`. On a tip it
  makes a branch with nothing of its own yet (one row, §5.3b). Swallowed while
  a range, a menu, a confirmation or a move is active. The turn it came from
  is not unfolded: a folded turn shows the branches off its body.
- The footer reads `⏎ continue here · b branch · …`.

### 2.5d A delivered summary is still recognised

When a summary is sent to the live tip (§2.5), Claude Code — not us — writes
the entry, and it stores a long typed-in message wrapped as
`<pasted_content id="…">` after leading blank lines (observed 2026-09-28). The
classifier unwraps exactly that: leading whitespace, then one
`<pasted_content id="…">` opening line, then the text. If what remains starts
with a `⤶` prefix, the entry is that summary kind (orange/blue, a turn
opener) and its row title is the `⤶` line. Any other paste is untouched.

### 2.6 The summary is still stored

A squash stores its summary exactly as v2 does, so `p` can merge the same
summary into another line later.

### 2.7 squash into… — removed

**Removed 2026-09-28** (§0 note). It chose a target in another line, then
summarised the range, merged it there and dropped it from its own line. The
same is done now with less machinery: squash the range, then move (§2.8) its
`⤶` row into the other line. Its text is no longer part of this spec.

### 2.8 move — carry turns verbatim

**Picking up.** `m` on a turn picks up that one section (its prompt, or its
`⤶` row, and everything under it). There is no range move: to move a stretch,
squash it first (one `⤶` section), then move that. The range menu stays
`squash · drop`. Status `moving 1 turn — ⏎ puts it here · esc
puts it back`.

**Moving.** The picked-up turn is drawn as a dimmed `⇢ …` block directly
after the turn under the cursor, following it; their origin shows a dimmed
`⋯ 1 turn moving`. Nothing is written; nothing is billed. `s`, `p`, `b`,
`m` are swallowed.

**`⏎` commits** after the turn under the cursor (whole turns, §3.1):
- **Same line:** one splice — the turns are removed and re-inserted after the
  target turn; the line is replaced by the reordered version (§5).
- **Another line:** the turns are inserted into the target line after the
  target turn (the target is replaced, §5), then dropped from the source (the
  source is replaced, a drop). Target first, then source — if the drop fails
  the status says `moved into <x>, but the source was not dropped: <err>`: a
  copy, nothing lost. If the drop is written but the store cannot be saved,
  it says `moved into <x>, and the source's drop was written but not
  recorded: <err>` — the drop's file exists and this overlay shows the drop, but the record is not saved to disk (the next successful save writes it).
- The busy checks (§6.1) and the changed-elsewhere check (§5.1) run on every
  line written. No pane opens (§6).
- **Markers** (another line only): the source shows `⇢ 1 turn moved to
  <id8>` where they were (a drop marker with a destination); the target shows
  `⇠ moved from <id8>` on the first moved turn. A move within the line is a
  reorder and leaves no marker (amended 2026-09-28): both would name the line
  itself, and they piled up and went stale with each further edit.

**`esc`** cancels: nothing was written.

**Relabelling.** A `⤶ squashed` row moved into another line becomes
`⤶ merged from <source8>` (the rest of its text unchanged): it is knowledge
arriving there, not a contraction of that line.

**Refused** (status, still moving): the target turn is the picked-up turn
itself, or the turn right before it (a no-op); a move that would take every
turn out of its line.

**Ids and links.** The first moved entry is parented to the target turn's
last entry, the entry after the insertion re-parented to the last moved one,
and the moved set keeps its internal links.
A **same-line** move keeps every id — nothing is duplicated within one file, and
branches and labels on the moved turn keep resolving. A **cross-line** move
rewrites, consistently within the moved turn, its uuids, requestIds,
`message.id`s and tool_use ids (each `tool_result`'s `tool_use_id` with them):
the target may hold copies carrying the same ids, and a repeated requestId or
message id would merge or mis-assign turns. Every tool call block's id is
renewed — `tool_use`, `server_tool_use`, `mcp_tool_use`, any `*_tool_use` —
with every `*_tool_result` that names it.

What follows from renewing ids, and is accepted: the moved turn's label (set
under its old uuid) does not follow it into the other line, and a branch that
hung off it re-attaches as `from a removed stretch`. Moving one of a branch's
copied turns (§5.3b) into that branch gives the branch the turn twice — the
user's choice; nothing is lost.

### 2.9 Watch it summarise, read it before it lands

For a **squash**, after the confirmation:

- **Summarising view.** The overlay shows what is running: `Summarising
  turns <a>–<b> of <id8>`, the Preview figures (`the model is reading <t>
  turns · <e> entries · <size>`), a spinner and the elapsed time, ticking every
  second, and `ctrl+c leaves (the call is already billed)`. The first ctrl+c
  only warns (`this call is already billed; ctrl+c again to leave it
  running`); the second leaves.
- **Review view.** When the summary arrives it is stored (§2.6) and shown in
  full in a scrollable box, headed `Squash turns <a>–<b> — review the
  summary`, with the `Then:` line of the confirmation below it. `↑↓` scroll,
  `⏎` commits — the rest of the squash runs exactly as before (busy and
  changed-elsewhere checks, then the splice) — and `esc` cancels: nothing is
  written to any conversation; the summary stays stored for `p`.
- A summary failure clears the summarising view: the tree comes back with the
  error on its status line (`summarise failed: <err> — nothing was written`).
  Nothing is written.

The summary is shown only in this view — never in a status line.

### 2.10 A squash names itself

The squash prompt asks the model to begin its reply with one line — a title of
at most 8 words describing the stretch — then a blank line, then the summary.
No extra call. The seed's first line becomes `⤶ squashed: <title>` (a moved
one, §2.8, `⤶ merged from <source8>: <title>`); the review view (§2.9) shows
the title above the summary. If the first line is empty, longer than 80
characters, or not followed by a blank line, it is not a title: the seed keeps
`⤶ squashed <from8>..<to8>` and the whole reply is the summary. Only new
squashes get titles; existing rows are left as they are. Placing a titled
summary with `p` (merge here, branch here, or sent to the live tip) uses it
too: `⤶ merged from <source8>: <title>`, the title line left out of the body.

A row shows `▸` and its folded count only when it has something of its own
folded under it (BodyCount > 0); a head whose only children are the next turn
or a branch is not drawn as foldable.

## 3. The splice

`claude.Splice`, next to `GraftSeeded` in `internal/claude/graft.go`.
`GraftSeeded` is not changed; branching and v2's fold-back keep using it.

### 3.1 Whole turns only

Tree rows are entries — prompts, replies, tool calls — so a selected range can
start or end mid-turn. A **turn** runs from an entry the user typed (a
`KindHuman` prompt, or an injected `⤶` entry) up to, not including, the next
one. Entries before the first prompt are the session's **preamble** and belong
to no turn.

The range is widened outward to whole turns: its start moves back to its
turn's prompt, its end forward to the last entry before the next prompt.

This is the only rule that keeps the result valid. A mid-turn drop leaves a
tool_use without its tool_result, which the Messages API rejects, or puts two
user or two assistant messages side by side. Turn boundaries avoid both.
Measured on 112 real transcripts (973 boundaries): all 9146 tool pairs lie
inside one turn, and the only uuid reference crossing a boundary is each
prompt's `parentUuid` to the previous turn.

### 3.2 What is written

A new session file, mode 0600, in the project directory for the session's cwd.
The source transcript is never modified.

1. Parse the source **fresh** at splice time (turns appended since the range
   was fixed are kept, after the splice point).
2. Keep: `Select(es, tip)` — the ancestor chain of the session's tip under
   Select's four rules. Uuid-less bookkeeping is dropped as in `GraftSeeded`.
3. Drop: every kept entry whose turn lies in the widened range. An entry's turn
   is its chain position's turn; off-chain entries kept by rules 2–4 take the
   turn of the entry that caused them to be kept.
4. Re-attach the first entry after the range (the next turn's prompt):
   - **squash / merge**: to the seed. The seed is one user entry carrying
     the seed text verbatim, parented to the last entry before the range.
   - **drop**: directly to the last entry before the range.
   - If nothing precedes the range but a preamble, "the last entry before the
     range" is the preamble's last entry; if there is no preamble either, it
     is null and the seed or the next prompt becomes the root.
5. If nothing follows the range, the seed (or, for a drop, the last entry
   before the range) is the new leaf.
6. `sessionId`/`session_id` and `cwd` are rewritten as in `GraftSeeded`.
   **Entry uuids are kept.** Branch re-attachment (§5.3) depends on it.

Seeds: squash uses `CompactionPrefix` (`⤶ squashed <from>..<to>`); merge
always uses `SummaryPrefix` (`⤶ merged from <session>`) — nothing was
removed, so nothing contracted (§6b). The existing `ErrUnmarkedSeed` check
applies.

### 3.3 Refused, nothing written

- The transcript has unparseable lines (`ErrPartialTranscript`) or an
  unsupported version (`checkVersion`).
- The range's start or end is not in `Select(es, tip)`: the range lies on a
  stretch the session has already rewound away from.
- A drop whose widened range covers every turn. (A squash of every turn is
  allowed; the result is the preamble plus the summary.)

### 3.4 Native `/compact`

Claude Code's own `/compact` writes a `compact_boundary` where the parent chain
restarts. Splice follows `parentUuid` as graft does, so it sees only the line
after the last boundary. The tree also shows the turns before it; a branch
can start from one of them (§2.5b), an edit cannot.

## 4. Squash, end to end

1. Confirm (§2.3), with the busy check (§6.1).
2. `adapter.Summarise` over the widened range — unchanged from v2, billed.
3. Store the summary (§2.6).
4. Busy re-check (§6.1, step 3).
5. Splice with the compaction seed, save (§5), reload the tree with the cursor
   on the seed (§5.3f). Nothing is opened (§6).

If the summary call fails, nothing is written or hidden.

## 5. Store and tree

### 5.1 Store

`store.Branch` gains optional fields; no migration.

- `replaced_by` (session id) — set on the **old** session's record. If the old
  session has no record, one is created carrying only this field.
- `kind` — on the **new** session's record: `compacted`, `cut` or `inserted`.
  Absent means a v2 branch.
- `replaces` — on the new record: the old session's id.
- `cut` — for `kind: cut`, the number of turns removed and the uuid of the
  first entry after the cut (where the marker renders); empty when nothing
  follows, and the marker then renders on the line's last row.

The new record's `grafted_from` is a **copy of the old record's**, not a
pointer at the old session: the replacement takes the old line's place,
including where it hung. A compacted branch stays under the trunk turn it
left; a compacted root stays a root.

`replaced_by` is one-way. `Save`'s merge never lets a record without it
overwrite one on disk that has it, so a second overlay saving an unrelated
change cannot un-hide a replaced line.

An edit is refused if its line was replaced since the overlay loaded — by
another overlay, say. Immediately before anything is paid for or written, the
store is re-read from disk; if the source line (or, for a merge, the target
line) now has `replaced_by`, nothing is summarised or written and the status
says `this line was changed in another overlay — reopen the tree`. The same
check runs again right before the splice, after a summary.

A session is hidden only if the session it resolves to is present. If the
replacement's file is gone, the old line shows again rather than vanishing.

### 5.2 Hiding

A session whose record has `replaced_by` is not rendered. `Discover` still
finds its file; nothing is deleted. The replacement renders from its first
turn in the old line's place, because its record carries the old line's
`grafted_from` (§5.1).

No "show hidden" toggle is built.

### 5.3 Re-attaching branches

When a branch's `grafted_from` names a hidden session, the lookup follows
`replaced_by` — repeatedly, through any number of splices — and attaches to the
node with the same uuid in the newest line. If that node was compacted or cut
away, the branch renders as a root line marked `from a removed stretch`.

Stored edges are never rewritten; resolution happens when the tree is built.

### 5.3b A branch renders from where it diverges

A grafted session carries copies of every turn up to its graft point, with the
same uuids. When it is attached under a turn, its leading turns whose ids are
also in the line it hangs from are not rendered again — the branch's first row
is its first turn of its own, carrying `↳ <session id>`. A branch with no turn
of its own yet (opened, nothing typed) keeps one row: its copy of the graft
point, so it stays visible and `⏎` opens it. Labels on those hidden copies do
not show; labels on the parent's turns are unaffected. A line that replaced
another (§5.2) is not attached under anything and is unaffected.

### 5.3c Labels follow their turn

A label names a turn by session and uuid. A replacement keeps its turns'
uuids, so the tree looks a label up along the line's `replaces` chain: a
label set on a turn of an earlier version of the line shows on the same turn
of the newest one. A label on a turn that was squashed or dropped away has no
row to show on and is not shown. Setting or clearing a label (`L`) writes it
under the session it is set on and removes the same turn's label from the
older versions along the chain, so the label shown is always the one last
set, and an empty label really clears it.

### 5.3d The family, and your path through it

The default scope is the **family** of the session you are in: the top-most
root line whose tree contains it (after `Resolve`, §6.3), with every branch
anywhere in that tree. `a` still shows all sessions.

Your path through the family — the trunk the tree already computes: your
session and each session it was grafted from, up to each branch point — is
marked by a cyan `▎` in the left margin of every row on it. The parent's turns
after the point you branched from, and sibling branches, have no bar. The bar
is drawn as its own segment, like the `✂` note, so no row's colour changes;
this overrides the timeline spec's §6b "no trunk style" with a margin mark
rather than a row colour. With no current session there is no bar.

**A branch is always indented** one level under the turn it came from,
whether or not you are on it; the parent's own continuation after that turn
stays at the parent's depth and follows the branches under it. Indentation
means "branched here"; the bar means "your path". This replaces v2's rule that
the line you are on stays level while the left-behind tail is indented.

### 5.3e Each session has a header line

Every session's first shown row is drawn as two lines: a header line with the
session id (`↳ <id>` for a branch, `<id>` for a root line), then the turn.
A branch's header sits one level under the turn it came from (§5.3d) and its
turns one level under the header; a root line's turns stay at its header's
level. The header is part of its turn's row: the cursor never lands on it,
`⏎`/`b`/`s`/`p`/`L` act on the turn, and the row counter counts rows, not
lines. The header carries the path bar when the session is on your path, and
the viewport fits lines, not rows, so a header never pushes the cursor's row
off screen.

### 5.3f A summary row starts its own section

A `⤶ squashed` or `⤶ merged from` row opens a turn (§3.1), so the tree draws
it as a section head, like a prompt: at the section level after the turn it
follows, never inside that turn's body. What the agent answers to it folds
under it. After a merge or squash the cursor lands on that row — the seed,
wherever in the line it is, not the line's tip — without unfolding the turn
before it. `Splice` reports the seed's uuid (`Spliced.First`) for the reload.

### 5.4 Markers

- **Drop**: the row after the drop shows `✂ <n> turns dropped before this` (or, on
  the last row when nothing follows, `… after this`) in the muted
  style. It comes from the store record; nothing about the drop is in the
  transcript.
- **Squash / merge**: the seeded entry renders blue or orange through its
  `⤶` prefix, per v2 §6b. Unchanged.

A line edited more than once keeps every earlier drop's marker: the tree
collects `cut` records along the `replaces` chain, each on its own anchor turn
(or the last row) in the newest line.

## 6. Live handover

An edit never opens or closes a pane. The handover happens when the user moves.

### 6.1 The edit

1. **At confirm**: resolve the session's live agent and its `agent_status`
   (`parseAgentList` keeps it alongside the pane id). If anything but `idle`
   (a status herdr reports for a running or waiting agent), refuse (§2.4),
   naming the status; nothing is written.
2. (continue only) Summarise.
3. **Re-check `agent_status` immediately before splicing.** If not `idle`:
   write nothing, status `summary stored — agent is <status>; select again or use p`.
4. Splice, save the store, reload with the cursor on the result: a squash's
   or merge's seed (§5.3f), a moved turn, or else the new line's tip.

A pane that was running the old line keeps running it. Anything typed there
lands on the hidden line; the status says `⏎ on it to continue there`.

### 6.2 `⏎` on a replacement

`⏎` on the tip of a line walks its `replaces` chain (a line edited several
times without moving) for a session still open in a pane.

- None found: plain resume, as today.
- Found, and its agent is anything but `idle` (§6.1): refused — closing it
  could kill a running turn.
- Found: confirm `Check out the new line. The pane running the old line is
  closed; text typed but not sent there is lost.` Then open the new session
  **with focus**, and close the old pane only if the open succeeded. If the
  open failed, the old pane is left running.

`herdr pane close` has no guard of its own; the status check is the guard.
Branching and plain resume keep `--no-focus`.

### 6.3 Scope follows the replacement

The tree's scope and trunk are computed from `Resolve(current)`, so editing the
session you are in keeps showing its line. Sending a message (the live-tip
fold) still targets only the agent actually running, never a resolved session.

### 6.4 Status lines

Each says what did happen:

- `squashed 1a2b3c4d → 5e6f7a8b — ⏎ on it to continue there`
- `dropped 8 turns from 1a2b3c4d → 5e6f7a8b — ⏎ on it to continue there`
- `opened 5e6f7a8b, but the old pane did not close: <err>`
- `could not open 5e6f7a8b — old pane left running: <err>`

Errors are passed through `scrubbed` as today; no message content reaches a
status line.

## 7. Cost

- Drop and merge spend nothing: they are local file writes.
- Squash spends one summary call, as v2's summarise does.
- The first message in a new session is sent with a cold prompt cache: the
  whole (now smaller) context is billed uncached once.

The summary call is `claude -p` with the inherited environment. Per Claude
Code's documentation it draws on a claude.ai subscription's limits unless an
API key, auth token, `apiKeyHelper` or cloud provider is configured. herdr-tree
adds no credential of its own.

## 8. Testing

- Unit tests, no API calls:
  - `Splice`: middle range; range at turn 1 with and without a preamble; drop
    vs. squash vs. merge (empty range); nothing after the range; range off
    the tip chain (refused); whole-session drop (refused); turns appended after
    the range was fixed.
  - Widening to whole turns, including ranges that start and end on tool
    calls.
  - `replaced_by` resolution through two splices; branch re-attachment,
    including a branch off a removed turn.
  - The drop marker; the range menu; the `p` merge/branch menu.
- Fixtures copied from real transcript shapes — parallel tool calls,
  attachments, a preamble, a `compact_boundary` — anonymised. A fixture that
  cannot occur in reality is how six v2 tests came to be unable to fail.
- Every herdr call goes through `stubHerdr` on `PATH`, recording argv. The
  handover's step order and every failure branch are tested this way,
  including "resume fails → close is never called".
- Mutation check on the risky logic — the re-parent, the widening, the busy
  re-check, close-after-open ordering: each mutated to a live-but-wrong
  expression, and a test must go red.
- Real-transcript check: a read-only script that splices every possible range
  of a copy of each local transcript into a temp dir and checks every tool_use
  has its tool_result and the chain is unbroken.
- **Not automated**: resuming a spliced session with `claude`. It spends
  budget, needs explicit approval, and is the one proof that Claude Code
  accepts a spliced file. To be run once before merging.

## 9. Changes to the timeline spec

- §4: `s` selects a range; summarising is one option of the range menu.
- §5: fold-back away from a live tip offers merge here beside branch here.
- §6b: "blue means this line contracted" becomes literally true for squash.
- The standing rule "never delete a session the user could still resume"
  stands. A replaced session is hidden, not deleted.

## 10. Open questions

1. Does Claude Code resume a spliced file without complaint? The structure is
   the same one it writes, but a spliced file has been validated only by our
   own checks until the manual run in §8.
2. Where does Herdr put focus when `pane close` closes a pane that did not
   have focus? The new pane is opened with focus first, so this should not
   matter; to be observed in the manual run.
