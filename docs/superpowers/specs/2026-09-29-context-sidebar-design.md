# herdr-tree — context sidebar and turn sizes

Date: 2026-09-29
Status: draft for review. Experimental: built on `feat/context-sidebar`, off
`feat/timeline-v2`, so it can be dropped without touching the rest.
Builds on `2026-09-28-undo-and-context-meter-design.md` (the context number,
§3) and the context-editing spec (§3.4 and its compacted-group amendment).

---

## 1. Why

The header number says how big a line's context is. It does not say where
the size is. This feature shows it: a sidebar with the session's context
split by type, and a size on every turn, so the user can see what is worth
squashing or dropping instead of guessing.

## 2. What was measured first

A read-only pass over the user's 164 local transcripts, 89 MB of message
content. These are bytes over whole files, not tokens or current lines:

| Type | Share of bytes |
|---|---|
| Thinking | 32% |
| Tool calls (inputs) | 26% |
| Tool results | 25% |
| Replies | 7% |
| Typed and injected | 9% |

Two findings shape the design:

- **Thinking bytes are almost all signature.** 93% of thinking blocks have
  empty text; the 28 MB is the encrypted signature. Bytes cannot size
  thinking.
- **Earlier thinking stays in context.** Fitting each reply's context growth
  against what came before it, over about 19,000 reply pairs, thinking's
  signature bytes carry about 0.4–0.6 tokens each, inside a tool loop and
  across a typed prompt. Everything else carries 0.1–0.2 tokens per byte.
  This is strong evidence, not proof.

## 3. The numbers

The Claude adapter computes them during `Discover`, in the same pass that
already reads each transcript. No model or network call is made.

Only the **current line** counts: the chain from the tip after the last
native compaction, without rewound stretches. The compaction's summary entry
counts as injected.

### 3.1 Per turn

A turn's size is the **real growth in context** it caused. A turn's **end**
is its last usage-bearing reply's context number (§3.1 of the meter spec:
input + cache read + cache creation) plus that reply's `output_tokens` (the
largest over its message id's split entries): what the context holds once
the turn is done. A turn's size is its end minus the previous turn's end (0
before the first turn). *(Amended 2026-09-29, final review:)* the previous
turn is the one the head continues — the nearest turn on the head's
`parentUuid` chain — not the turn before it in the file, which after a
rewind is the abandoned one. The sizes of a line's turns
sum to its last turn's end.

The size falls back to the turn's bytes ÷ 4, marked estimated, when:

- this turn or the previous one has no usage-bearing reply, as after an
  edit, in an older transcript, or with only synthetic replies;
- the growth is ≤ 0;
- a `compact_boundary` lies between the two replies used.

### 3.2 Per type, for the line

Each entry on the line is sorted into one of six types:

| Type | Entries |
|---|---|
| thinking | assistant `thinking` blocks |
| tool calls | assistant `tool_use` blocks |
| tool results | user `tool_result` blocks |
| replies | assistant `text` blocks |
| typed | what a person typed: `KindHuman` prompts |
| injected | everything else a user entry carries: reminders, attachments, the compaction summary, herdr-tree's seeds |

The raw sizes are:

- **Tool calls, tool results, replies, typed, injected:** bytes ÷ 4.
- **Thinking:** for each reply, its `output_tokens` minus the bytes ÷ 4 of
  its visible blocks, never below 0. The split entries of one reply share
  one usage and are counted once; the largest `output_tokens` among them is
  used. *(Amended 2026-09-29, final review:)* a reply with no usage on any
  of its entries — every reply after an edit, since a splice strips usage —
  takes its thinking as its thinking blocks' signature bytes × 0.5 (the
  measured 0.4–0.6 tokens per signature byte).

The six raw sizes are then scaled so they sum to the line's context number.
When the line has only an estimate (`~`) or no number, the sizes are shown
as they are, marked `~`.

### 3.3 Where it lives

- `adapter.Session.Breakdown` — six ints in the fixed order above, plus
  `Estimated bool`.
- `adapter.Node.TurnTokens int` and `TurnEstimated bool`, set on each turn's
  head node, 0 elsewhere.
- `tree.Build` carries both to its nodes, as it does `ContextTokens`. The
  tree and the TUI know nothing about Claude.

## 4. The sidebar

- A right-hand column, 26 wide, shown when the overlay is at least **110
  columns** wide. Narrower, it is hidden and the tree keeps its full width.
  **`c`** toggles it; the toggle lasts for the overlay's life. The footer
  lists `c context` only when the pane is wide enough.
- It describes the **session you are in** (`u.current`, resolved with
  `Current` like the scope). It follows edits, undo and redo, and does not
  change with the cursor.
- Its layout:

```
context  84k
thinking     ▮▮▮▮▮▯▯▯ 32%
tool calls   ▮▮▮▮▯▯▯▯ 26%
tool results ▮▮▮▮▯▯▯▯ 25%
replies      ▮▯▯▯▯▯▯▯  7%
typed        ▮▯▯▯▯▯▯▯  7%
injected     ▯▯▯▯▯▯▯▯  3%
```

  The bar has 8 cells, rounded. With an estimate the first line reads
  `context ~31k`. With no number and no bytes it reads `context —` and no
  types. *(Amended 2026-09-29, final review:)* with bytes but no number it
  reads `context —` above the six bars.
- It is separated from the tree by a muted `│` column. Rendering stays plain
  text so it can be asserted; colours follow the palette: the bar in the
  tree's muted grey, and `thinking` in Claude's terracotta, since it is the
  model's own.
- Modes that take the whole screen (a confirmation, the summarising view, the
  review) hide it while they are open.

## 5. Turn sizes

- Every turn row that stands for a whole turn shows its size after its body
  count, muted: `▸ user: test the index on a local copy first  (8)  12k`,
  or `~12k` when estimated. That means a folded head, or the prompt row of
  an open turn. Body rows show none.
- Formatting is `humanTokens` (`<1k`, `12k`, `1.2M`).
- The closed compacted-group row shows the group's total, summed from its
  turns, as `· 2 turns · ~40k`. It is always marked `~`, since the numbers
  come from before the compaction.
- Truncation for width drops the size before the title, as the context
  number is dropped before the id.

## 6. Safety and cost

- Read-only: nothing is written; no new file reads (the `Discover` pass).
- No message content reaches a status line, an error or a log. The numbers
  are sizes only.
- The cost is one more walk over entries already in memory per session.

## 7. Testing

- Claude adapter, with real-shaped fixtures: usage on replies, split replies
  sharing one usage, signature-only thinking, a turn after an edit (no
  usage), a compaction:
  - the per-turn growth, and its byte fallback;
  - the thinking remainder, clamped at 0 and counted once per reply;
  - the six sizes summing to the context number after scaling;
  - only the current line counted (a rewound stretch and pre-compact turns
    add nothing).
- TUI:
  - the sidebar shown at 110 columns, hidden at 109, toggled by `c`;
  - it follows `u.current` after an edit, not the cursor;
  - turn sizes on folded heads and open prompt rows only;
  - the group total on a closed group.
- Mutation-check: drop the thinking clamp; count split replies twice; drop the
  width guard.

## 8. Open questions

None blocking. Possible later: a per-range breakdown while selecting with `s`
(what a squash would remove), and slim (strip big tool results), which the
breakdown would show the value of.
