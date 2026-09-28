# Decisions

The rulings and user choices that shape the code, distilled from the working
ledger of the context-editing work. Each: the decision, why, and the cost if it
is wrong. The spec (`superpowers/specs/2026-09-23-context-editing-design.md`)
has the full behaviour; this is the reasoning behind the parts that are not
obvious from it.

## Splice and turns

- **Edits work on whole turns only.** A range widens to its turns' prompts and
  last entries. Why: a mid-turn cut orphans a tool_use or puts two same-role
  messages side by side. Cost: none known; measured on 112 transcripts.
- **Every edit is one `Splice`** (keep the tip's chain, drop a range, insert
  one seed or carried turns, re-attach). Why: one code path to validate.
- **A splice keeps uuids; a cross-line move renews uuids, requestIds,
  `message.id` and tool_use ids consistently within the moved turn.** Why: the
  target may hold copies with the same ids, and a repeated requestId or message
  id mis-assigns turns. A same-line move keeps every id so branches and labels
  still resolve. Cost: nothing — ids are opaque.
- **Inherited dangling tool_uses are not an error.** An interrupted session's
  tip is an unanswered tool_use; a seed after it is what typing into the
  resumed session produces. `splicecheck` counts them separately. Cost: a
  missed defect class if Claude Code ever rejects that shape.
- **`splicecheck` breaks refusals down by reason.** Why: an unexplained 43%
  refusal rate can hide defects.
- **Move is one section, no range move.** User choice: squash a stretch first,
  then move its `⤶` row. Cost: two steps for a stretch.
- **Moving within the same line is one splice; into another line it is target
  first, then drop from the source.** Why: a failure leaves a copy, never a
  loss.
- **Branches graft at the end of the turn, not the row's entry.** Why: grafting
  at a prompt leaves it unanswered. The edge names the turn's last *node*
  (`Span.EndNode`), not its last entry, which is often a `turn_duration` system
  entry that is no row. Old edges that name a non-node are healed in `Build`
  by falling back to the last copied turn.

## Store and tree

- **A splice replaces the line: `replaced_by` on the old record, a new record
  carrying a copy of the old `grafted_from`.** Why: the replacement takes the
  old line's place. Hidden only if the replacement's file exists.
- **`replaced_by` is one-way in `Save`'s merge.** Why: another overlay's save
  must not un-hide a replaced line.
- **An edit re-reads the store from disk before paying and before splicing, and
  refuses if a line it writes was replaced elsewhere.** Why: two overlays on
  one line silently produced an unmarked duplicate. Cost: a short race window
  remains (below).
- **`b`/branch here from a replaced line is not refused.** A graft writes
  nothing to the old line and `Build` re-hangs it via `Resolve`. Cost: the
  branch hangs off the resolved line, not the one the user saw.
- **A branch renders from where it diverges;** its copied prefix is not drawn
  again. User choice.
- **Labels follow their turn along the `replaces` chain, and `L` clears older
  versions' label for that turn.** User choice; clearing must work on an
  inherited label.
- **Default scope is the family; your path gets a cyan `▎` margin bar; a branch
  is always indented.** User choices. The bar is a separate segment so no row
  changes colour.
- **Every session has a header line; `⤶` rows are section heads.** User
  choices.
- **`←` jumps to the nearest preceding row with smaller depth** (the visual
  parent). Why: pure, screen-exact, never lands on an invisible node.

## TUI flow

- **The range menu is `squash · drop`. `squash into…` was built and then
  removed** at the user's call: squash then move does the same with less
  machinery.
- **Squash shows a summarising view and a review before anything is written.**
  User choice. esc in review keeps the summary stored for `p`.
- **A squash titles itself** from the reply's first line (≤80 chars, followed
  by a blank line); no extra call. `p` placements carry the title too.
- **⏎ continues, `b` branches.** ⏎ on an earlier turn still confirms and opens
  a pane (user-initiated); `b` opens nothing and asks nothing.
- **No edit opens a pane.** User choice after the first manual run: edits only
  write; ⏎ is what moves you.
- **Git vocabulary** (squash, drop, merge here, branch here, checkout), no
  backward compatibility for the old markers. User choice ("not used on real
  data yet"). `p` is "place a summary": git has no verb for it.

## Live handover

- **The handover happens on ⏎, not on the edit.** ⏎ on a replacement walks
  `replaces` for a pane still running an older version, confirms, opens the new
  line focused, re-checks the old pane, and only then closes it. If the open
  fails, the old pane is left. Cost of the order: none; the old pane is all the
  user has until the new one exists.
- **Busy is an allowlist: only `idle` (or no agent) is safe.** Why: herdr's
  full `agent_status` vocabulary is unknown. Cost: spurious refusals if herdr
  uses another word for idle.
- **Empty `agent_status` counts as safe.** Cost: a closed pane if herdr ever
  reports a live agent with no status.
- **A herdr error blocks even a plain ⏎ resume.** Fail closed. Cost: one
  refused ⏎ during a herdr hiccup.
- **Scope follows `Resolve(current)`; sending still targets only the agent
  actually running** `current`, never a resolved session.

## Prompts

- **One prompt for every summary: the handover/compaction prompt**
  (`CompactPrompt`), wording approved by the user verbatim. The report-style
  prompt went with `squash into…`.
- **Seeds are marked by effect, not origin.** `⤶ squashed` only when the line
  contracted (a graft/splice that removes turns of the same session);
  everything else, including a merge of the session's own summary, is
  `⤶ merged from`. A `⤶ squashed` row moved cross-line is relabelled
  `⤶ merged from <source8>`, the rest of its text kept.
- **A summary delivered to a live tip is recognised through Claude Code's
  `<pasted_content>` wrapper.** Verified live: delivered as one message,
  wrapped. Only exactly that wrapper is unwrapped.

## Safety

- **Source transcripts are never modified; sessions are never deleted;
  writes are 0600 and atomic.**
- **No message content in any status line or log.** Errors are passed through
  `scrubbed`; the summary is shown only in the review view.
- **Tests stub `herdr` and `claude` on `PATH`,** because agents twice reached
  the user's live Herdr despite being told not to.
- **Scenario tests live in `internal/tui` and import `internal/claude`,
  test-only.** The layering rule protects the production binary.
- **Bugs found by scenarios are recorded as skipped tests, then fixed in their
  own task.** Each fix gets its own review.

## Known open items / deferred minors

- The herdr `agent_status` word at a permission prompt is unverified.
- A short race remains between the last changed-elsewhere check and the
  splice's `Save` (needs a lock or compare-and-swap); a drop's check sits
  before the `live()` query rather than right before `Splice`.
- A corrupt `tree.json` loads as empty, so `ReplacedOnDisk` reads it as "not
  replaced".
- Parked: the cross-line relabel does not unwrap a `<pasted_content>`-wrapped
  `⤶ squashed`. Unreachable today: only the live-tip send produces a wrapper,
  and it always sends the `⤶ merged from` form.
- Pre-`/compact` turns render but every edit on them is refused; consider
  marking or hiding them.
- `parseTitle` takes a first line like `state: …` followed by a blank line as
  the title if the model skips the title.
- `server_tool_use` / `mcp_tool_use` ids are not renewed on a cross-line move.
- `✂ 1 turns dropped` wording.
- A drop marker whose anchor is later dropped or squashed falls back to the
  last row.
- A rewind inside a branch to before its graft point renders under the graft
  node.
- Several new body nodes under a superseded head render flat at one depth.
- Label clear and `Replace` run in memory before `Save`; a failed save leaves
  memory ≠ disk until reload (the status says so). No reload after a failed
  close or save.
- The handover's "already gone" status also covers a different-pane case.
- `scrubbed` skips seed lines under 12 characters.
- `b`'s guard lacks `p`'s `n.Node.ID == ""` clause (equivalent today).
- `u.placing` is not cleared after `placeChosen` (harmless).
- `cutNote` shows one marker if a node had both a before- and after-drop
  (unreachable: one record carries at most one cut).
- Performance: `buildLine` rebuilds a uuid map `Select` already built;
  `tree.Build`'s `nodeOf` rebuilds a node-id map per edge; a full
  `splicecheck` takes ~30 min because `Splice` re-parses per call.
- `ErrAgentBlocked` has no `errors.Is` consumer; it surfaces as text.
- Tests: multi-hop `replaces` walk and its cycle guard; `Fold` from a branch's
  later turn; `p`/`L` on a branch's first turn; the compacted record's kind
  and title — untested. `TestASquashSeedStartsTheFirstSection` passes on
  pre-fix code; `TestBranchHereIsTodaysFoldBack` and
  `TestAFailedSummaryWritesAndClosesNothing` hold vacuous assertions.
