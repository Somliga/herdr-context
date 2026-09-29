# Context sidebar and turn sizes — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show where a session's context goes: a right-hand sidebar with its split by type, and a size on every turn row.

**Architecture:** The Claude adapter computes, in the `Discover` pass it already makes, two things: each turn's size, as real context growth from `usage` with a bytes fallback, and the current line's six-type breakdown. They travel on `adapter.Node` and `adapter.Session`; `tree.Build` copies the session breakdown to its nodes. The TUI only formats: turn sizes in `renderRow`, the sidebar joined to the right of the tree rows in `View`.

**Tech Stack:** Go, Bubble Tea, lipgloss (already in the module).

**Spec:** `docs/superpowers/specs/2026-09-29-context-sidebar-design.md`

## Global Constraints

- Branch `feat/context-sidebar` (experimental). Never push, and never touch `feat/timeline-v2` or `main`.
- Read-only: no new file reads and no writes. Everything comes from entries `Discover` already parsed.
- No model, network or `herdr` call. Never run `claude`, `scripts/verify-*.sh`, `cmd/timelinecheck` or `cmd/graftcheck`. Never write `bin/`. Never read `~/.claude/projects` or `~/.config/herdr`.
- Never log message content. Only sizes may reach output.
- `internal/tui` must not import `internal/claude` (tests may). `internal/tree` knows nothing about Claude.
- Rendering stays plain text so it can be asserted. Colour is applied only in `View`.
- The sidebar is 26 columns plus a 1-column `│` separator, shown only at `u.width >= 110`. The key `c` toggles it.
- Type order and labels, verbatim: `thinking`, `tool calls`, `tool results`, `replies`, `typed`, `injected`.
- Token formatting is `humanTokens` (existing): `<1k`, `12k`, `1.2M`. An estimate is prefixed `~`.
- Commits are plain messages ending with a blank line and `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. A turn cut short by an autocompact, whose growth crosses a `compact_boundary`, falls back to bytes and is marked `~`, never a negative or huge number. (Task 1 test.)
2. A session whose replies have no `usage` at all, from an old transcript or a fresh branch, shows turn sizes as `~` from bytes and a sidebar of shares with `context —`, never a crash or a 0% row set that sums to nothing. (Task 1 and Task 3 tests.)
3. An edited line whose number is an estimate (`est_tokens`): the sidebar's first line reads `context ~31k` and the shares still add to about 100%. (Task 3 test.)
4. A pane resized across 110 columns while open: the sidebar appears or disappears on the next `WindowSizeMsg`, and the tree rows never exceed `u.width`. (Task 3 test.)
5. Split replies sharing one `message.id` and one `usage`: their thinking remainder is computed once per reply, not once per entry. (Task 1 test.)

---

## File map

- `internal/adapter/adapter.go`: new `Breakdown` type; `Session.Breakdown`; `Node.TurnTokens`, `Node.TurnEstimated`.
- `internal/claude/context.go` (new): `turnSizes`, `breakdown`, and their helpers. Keeps line.go focused.
- `internal/claude/discover.go`: calls both, and sets the fields.
- `internal/claude/context_test.go` (new).
- `internal/tree/tree.go`: `Node.SessionBreakdown` copied like `SessionTokens`.
- `internal/tui/view.go`: turn size in `renderRow`, group total in `groupText`; the sidebar, `c`, footer.
- `internal/tui/sidebar.go` (new): `sidebarLines(b adapter.Breakdown, total int, est bool) []string`.
- `internal/tui/sidebar_test.go` (new); `internal/tui/scenario_test.go` (scenarios).
- `README.md`, `docs/DECISIONS.md`, `AGENTS.md` (code map): in Task 3.

---

### Task 1: The numbers (Claude adapter)

**Files:**
- Modify: `internal/adapter/adapter.go`
- Create: `internal/claude/context.go`, `internal/claude/context_test.go`
- Modify: `internal/claude/discover.go` (the `adapter.Session{...}` literal in `Discover`, and the node slice from `Entries`)

**Interfaces:**
- Consumes: `Entries(es) []adapter.Node`; `buildLine(es) (*line, error)` (fields `keep`, `chain`); `usageTokens(e) (int, bool)`; `tipOf(es)`; `Entry.IsCompactBoundary()`; `Classify(e, hasOrigin)`; `HasHumanOrigin(es)`.
- Produces:

```go
// adapter.go
// Breakdown is a line's context split by type (§3.2 of the sidebar spec), in
// the order Types names. Estimated means no real total scaled it.
type Breakdown struct {
	Tokens    [6]int
	Estimated bool
}

// Types are Breakdown's labels, in order.
var Types = [6]string{"thinking", "tool calls", "tool results", "replies", "typed", "injected"}

// on Node:
	// TurnTokens is a head node's turn size (§3.1 of the sidebar spec), 0 on
	// other nodes; TurnEstimated marks the bytes fallback.
	TurnTokens    int
	TurnEstimated bool

// on Session:
	Breakdown Breakdown
```

```go
// context.go
func turnSizes(es []Entry, nodes []adapter.Node)          // sets TurnTokens/TurnEstimated on head nodes in place
func breakdown(es []Entry, total int) adapter.Breakdown   // the current line's split, scaled to total when > 0
```

- [ ] **Step 1: Write the failing tests** in `internal/claude/context_test.go`. Use a `lines(t, ...string) []Entry` helper that parses JSONL strings through `ParseFile` on a temp file, so numbers decode as `json.Number` exactly as in production. The fixture shape:

```go
// Two turns. Turn 1: prompt p1, reply a1 (thinking signature-only + text),
// tool call a1t sharing a1's message id and usage, tool result r1, reply a2.
// Turn 2: prompt p2, reply a3. Usages: a1/a1t ctx 1000 out 300; a2 ctx 1600 out 50;
// a3 ctx 2100 out 40.
func twoTurns(t *testing.T) []Entry { /* JSONL literals, real-shaped:
   user {"type":"user","uuid":"p1","parentUuid":null,"sessionId":"S","cwd":"/r","version":"2.1.278","message":{"role":"user","content":"first"},"origin":{"kind":"human"}}
   assistant a1: content [{"type":"thinking","thinking":"","signature":"<400 chars>"},{"type":"text","text":"<40 chars>"}], message.id "m1", requestId "q1", usage {input_tokens:0,cache_read_input_tokens:1000,cache_creation_input_tokens:0,output_tokens:300}
   assistant a1t: parent a1, content [{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"ls"}}], same message.id m1 and same usage
   user r1: parent a1t, content [{"type":"tool_result","tool_use_id":"tu1","content":"<800 chars>"}], toolUseResult {...}
   assistant a2: parent r1, text, id m2, usage ctx 1600 out 50
   system turn_duration d1 (parent a2)
   user p2 (parent d1, origin human) "second"
   assistant a3 (parent p2) text, id m3, usage ctx 2100 out 40 */ }

func TestTurnSizesAreRealGrowth(t *testing.T) {
	es := twoTurns(t)
	nodes := Entries(es)
	turnSizes(es, nodes)
	got := map[string]adapter.Node{}
	for _, n := range nodes {
		got[n.ID] = n
	}
	// end(turn) = its last usage-bearing reply's ctx + output_tokens.
	// turn 1 ends at a2: 1600 + 50 = 1650; turn 2 at a3: 2100 + 40 = 2140.
	if n := got["p1"]; n.TurnTokens != 1650 || n.TurnEstimated {
		t.Fatalf("turn 1 = %d est %v, want 1650 real", n.TurnTokens, n.TurnEstimated)
	}
	if n := got["p2"]; n.TurnTokens != 490 || n.TurnEstimated {
		t.Fatalf("turn 2 = %d est %v, want 2140 − 1650 = 490 real", n.TurnTokens, n.TurnEstimated)
	}
	if got["a1"].TurnTokens != 0 {
		t.Fatal("a body node carries a turn size")
	}
}

func TestTurnSizesFallBackToBytesWithoutUsage(t *testing.T) {
	es := twoTurnsWithoutUsage(t) // same shape, every "usage" removed
	nodes := Entries(es)
	turnSizes(es, nodes)
	for _, n := range nodes {
		if n.ID == "p1" && (!n.TurnEstimated || n.TurnTokens <= 0) {
			t.Fatalf("p1 = %d est %v, want a positive ~ from bytes", n.TurnTokens, n.TurnEstimated)
		}
	}
}

func TestTurnSizesFallBackAcrossACompactBoundary(t *testing.T) {
	// turn 1's reply ctx 90000; a compact_boundary; its continuation reply ctx 8000;
	// turn 2 prompt then reply ctx 9000. Turn 1's growth would be negative: bytes, ~.
	es := compactedMidTurn(t)
	nodes := Entries(es)
	turnSizes(es, nodes)
	for _, n := range nodes {
		if n.ID == "p1" && (!n.TurnEstimated || n.TurnTokens <= 0 || n.TurnTokens > 90000) {
			t.Fatalf("p1 = %d est %v, want a positive bytes estimate", n.TurnTokens, n.TurnEstimated)
		}
	}
}

func TestBreakdownSplitsTheLineAndSumsToItsNumber(t *testing.T) {
	es := twoTurns(t)
	b := breakdown(es, 2100)
	sum := 0
	for _, v := range b.Tokens {
		sum += v
	}
	if sum < 2095 || sum > 2105 || b.Estimated {
		t.Fatalf("sum %d est %v, want ≈2100 real", sum, b.Estimated)
	}
	// thinking: per reply, output − visible bytes/4, once per message id:
	// m1: 300 − (40 text + tool_use block bytes)/4 > 0; m2: 50 − text/4; m3: 40 − text/4.
	if b.Tokens[0] == 0 {
		t.Fatal("no thinking share despite output_tokens beyond the visible blocks")
	}
}

func TestBreakdownCountsASplitReplysThinkingOnce(t *testing.T) {
	es := twoTurns(t)
	once := breakdown(es, 0).Tokens[0]
	doubled := breakdown(append(es, duplicateOfA1t(t)), 0).Tokens[0] // another entry of m1, same usage
	if doubled != once {
		t.Fatalf("thinking %d with another split entry, %d without: counted per entry", doubled, once)
	}
}

func TestBreakdownWithoutANumberIsEstimated(t *testing.T) {
	b := breakdown(twoTurnsWithoutUsage(t), 0)
	if !b.Estimated || b.Tokens[2] == 0 {
		t.Fatalf("got %+v, want estimated shares with tool results", b)
	}
}

func TestBreakdownIgnoresEverythingOffTheCurrentLine(t *testing.T) {
	// A rewound stretch: p1 → a1, then a second prompt p1b whose parent is p1's parent
	// (the user rewound). The tip's line holds p1b only; a1's tool result must not count.
	es := rewound(t)
	if b := breakdown(es, 0); b.Tokens[2] != 0 {
		t.Fatalf("tool results %d from a rewound stretch", b.Tokens[2])
	}
}
```

- [ ] **Step 2: Run to see them fail.** `go test ./internal/claude -run 'TurnSizes|Breakdown'` fails with `undefined: turnSizes`.

- [ ] **Step 3: Implement `context.go`.**

Turn sizes, in file order over non-sidechain entries:
- A **head** is an entry whose `Entries` node is `KindHuman`, `KindSummaryImport` or `KindSummaryCompaction`, or the first node, matching `tree.Build`'s `isHead`.
- `ctx(e)` is `usageTokens(e)` on an assistant entry with a non-zero sum. `out(e)` is `message.usage.output_tokens`.
- A turn runs from its head up to the next head. `end(i)` is `ctx(r) + out(r)`, where `r` is the turn's last usage-bearing reply. `end(0)` before the first turn is 0.
- Size is `end(i) − end(i−1)`.
- Fall back to `turnBytes/4` with `TurnEstimated = true` when:
  - turn `i` or turn `i−1` has no usage-bearing reply (turn `i−1` does not exist for the first turn: that is not a fallback);
  - the growth is ≤ 0;
  - a `compact_boundary` lies between the previous turn's `r` and this turn's `r`.
- `turnBytes` is the sum of `len(message.content)` of the turn's entries, re-marshalled with `Marshal`. Use the raw content length from `e.Raw["message"]["content"]` via `json.Marshal`.
- Set it on the node whose `ID` is the head's uuid.

Breakdown:
- Take the line from `buildLine(es)` and iterate `es` in file order, keeping `l.keep[uuid]` only. If `buildLine` errs, fall back to every non-sidechain entry after the last `compact_boundary`.
- Sort each entry's `message.content` into the six types:
  - **assistant:** each block by type: `thinking` → skip for bytes; `tool_use` → tool calls; `text` → replies.
  - **user:** `tool_result` blocks → tool results; else KindHuman (`Classify`) → typed; else injected. Plain-string content counts as one block.
  - Bytes ÷ 4 per block, with `json.Marshal` of the block.
- Thinking, per assistant `message.id`, taken once: `max(0, out − visibleBytes/4)`, where `visibleBytes` is the sum over every entry of that id of its non-thinking blocks.
- If `total > 0`, scale all six so they sum to `total`. Put the rounding remainder on the largest. `Estimated = false`.
- Else `Estimated = true`, unscaled.

- [ ] **Step 4: Wire `Discover`.** After `nodes := Entries(es)`, call `turnSizes(es, nodes)`. Set `Breakdown: breakdown(es, ctx)`, where `ctx` is the `ContextTokens` value already computed.

- [ ] **Step 5: Run.** `go vet ./... && go test -count=1 ./...` → ok.

- [ ] **Step 6: Mutations.**
  - Drop the thinking clamp (`max(0, …)` → the raw difference) with a fixture reply whose visible bytes exceed its output: add one assert in `TestBreakdownSplitsTheLineAndSumsToItsNumber` that no type is negative → red.
  - Take thinking per entry instead of per message id → `TestBreakdownCountsASplitReplysThinkingOnce` red.
  - Revert both.

- [ ] **Step 7: Commit** — `feat: the adapter sizes each turn and splits the line's context by type`.

---

### Task 2: Turn sizes on rows

**Files:**
- Modify: `internal/tui/view.go` (`renderRow` around the `(%d)` count, line ~139; `groupText` ~171)
- Modify: `internal/tree/tree.go` (nothing for turn sizes: `adapter.Node` is embedded as `n.Node`, so `n.Node.TurnTokens` is there already)
- Test: `internal/tui/view_test.go`, `internal/tui/scenario_test.go`

**Interfaces:**
- Consumes: `adapter.Node.TurnTokens`, `TurnEstimated` (Task 1); `humanTokens` (exists); `Row.Group`, `Row.GroupClosed`, `Row.GroupTurns` (exist).
- Produces: `func turnSize(n *tree.Node) string`, which returns `"  12k"`, `"  ~12k"`, or `""` when 0. The row gains a `GroupTokens int` field, set in `Model.Rows` on the group row as the sum of `TurnTokens` of the group's head nodes.

- [ ] **Step 1: Failing tests.**

```go
func TestAFoldedTurnShowsItsSize(t *testing.T) {
	n := &tree.Node{SessionID: "S", IsHead: true, Node: adapter.Node{ID: "p", Title: "user: hi", Kind: adapter.KindHuman, TurnTokens: 12400}}
	got, _ := renderRow(Row{Node: n, Folded: true, BodyCount: 8}, false, "", 0)
	if !strings.HasSuffix(got, "(8)  12k") {
		t.Fatalf("%q", got)
	}
	n.Node.TurnEstimated = true
	if got, _ := renderRow(Row{Node: n, Folded: true, BodyCount: 8}, false, "", 0); !strings.HasSuffix(got, "(8)  ~12k") {
		t.Fatalf("estimate: %q", got)
	}
	body := &tree.Node{SessionID: "S", Node: adapter.Node{ID: "a", Title: "assistant: x", Kind: adapter.KindAssistant}}
	if got, _ := renderRow(Row{Node: body}, false, "", 0); strings.Contains(got, "k") && strings.HasSuffix(got, "k") {
		t.Fatalf("a body row shows a size: %q", got)
	}
}

func TestAnOpenTurnShowsItsSizeOnItsPrompt(t *testing.T) {
	n := &tree.Node{SessionID: "S", IsHead: true, Node: adapter.Node{ID: "p", Title: "user: hi", Kind: adapter.KindHuman, TurnTokens: 3000}}
	if got, _ := renderRow(Row{Node: n, Folded: false, BodyCount: 8}, false, "", 0); !strings.HasSuffix(got, "  3k") {
		t.Fatalf("%q", got)
	}
}

func TestTheClosedGroupShowsItsTotal(t *testing.T) {
	r := Row{Group: true, GroupClosed: true, GroupTurns: 2, GroupTokens: 40000,
		Node: &tree.Node{SessionID: "S", IsHead: true, Node: adapter.Node{ID: "u1"}}}
	if got, _ := renderRow(r, false, "", 0); !strings.HasSuffix(got, "· 2 turns · ~40k") {
		t.Fatalf("%q", got)
	}
}
```

Also add a scenario: `newWorld` with `w.durations = true` and a three-turn trunk. Add usage to the replies by rewriting the fixture file, the same technique as `TestScenarioAnEditedLineShowsAnEstimatedContextSize`. Then assert the folded rows' text ends in a size, via `asShown`.

- [ ] **Step 2: Run to see them fail.**

- [ ] **Step 3: Implement.**
  - In `renderRow`, after the `(%d)` count, append `turnSize(r.Node)` when `r.Node.IsHead`.
  - In `groupText`, append `" · ~" + humanTokens(r.GroupTokens)` when `GroupTokens > 0`.
  - In `Model.Rows`, sum `TurnTokens` over the group's head nodes while walking, and set `GroupTokens` on the anchor row. The anchor's row is emitted before the rest of the group is walked, so fill it in after the walk by index; `anchor[sid]` already holds it.
  - Width: `fit` truncates from the end, so a narrow row loses the size first. That is the spec's rule, so nothing extra is needed.

- [ ] **Step 4: Run** `go vet ./... && go test -count=1 ./...` → ok.

- [ ] **Step 5: Mutation.** Show the size on every row, dropping the `IsHead` guard → the body-row assert goes red. Revert.

- [ ] **Step 6: Commit** — `feat: each turn row shows its size; the compacted row its total`.

---

### Task 3: The sidebar

**Files:**
- Create: `internal/tui/sidebar.go`, `internal/tui/sidebar_test.go`
- Modify: `internal/tree/tree.go` (`Node.SessionBreakdown adapter.Breakdown`, set at both `SessionTokens` sites)
- Modify: `internal/tui/view.go` (`uiModel` field `sidebarOff bool`; key `c` in `Update`'s normal-mode switch and in the swallow lists that already swallow `u`/`U`; `View`; footer)
- Modify: `README.md` (Keys table, `What you see`), `docs/DECISIONS.md`, `AGENTS.md` (code map line for `sidebar.go`)

**Interfaces:**
- Consumes: `adapter.Breakdown`, `adapter.Types` (Task 1); `tree.Node.SessionTokens`, `TokensEstimated` (exist); `u.st.Current(u.current)` (exists); `humanTokens`.
- Produces: `func sidebarLines(b adapter.Breakdown, total int, est bool) []string`, which returns exactly 7 lines, each at most 26 runes. `const sidebarWidth = 26`, `const sidebarMin = 110`.

- [ ] **Step 1: Failing tests.**

```go
func TestSidebarLines(t *testing.T) {
	b := adapter.Breakdown{Tokens: [6]int{26880, 21840, 21000, 5880, 5880, 2520}}
	got := sidebarLines(b, 84000, false)
	want := []string{
		"context  84k",
		"thinking     ▮▮▮▯▯▯▯▯ 32%",
		"tool calls   ▮▮▯▯▯▯▯▯ 26%",
		"tool results ▮▮▯▯▯▯▯▯ 25%",
		"replies      ▮▯▯▯▯▯▯▯  7%",
		"typed        ▮▯▯▯▯▯▯▯  7%",
		"injected     ▯▯▯▯▯▯▯▯  3%",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, l := range got {
		if n := len([]rune(l)); n > sidebarWidth {
			t.Fatalf("%q is %d wide", l, n)
		}
	}
}

func TestSidebarWithAnEstimateAndWithNothing(t *testing.T) {
	b := adapter.Breakdown{Tokens: [6]int{0, 100, 300, 0, 0, 0}, Estimated: true}
	if got := sidebarLines(b, 31000, true)[0]; got != "context ~31k" {
		t.Fatalf("%q", got)
	}
	if got := sidebarLines(adapter.Breakdown{Estimated: true}, 0, false); got[0] != "context —" || len(got) != 1 {
		t.Fatalf("nothing known: %q", got)
	}
}
```

Bar rule: `cells = round(8 × share)`, clamped to 0..8, where `share = tokens / sum`. Percent is `round(100 × share)`, right-aligned in 3 columns plus `%`. The labels are padded to 13 columns. **Recheck the literal `want` bars against this rule when implementing, and fix the test's literal, not the rule, if they differ:** 32% × 8 = 2.56 → 3 cells, 26% → 2, 25% → 2, 7% → 1, 3% → 0.

Scenarios in `scenario_test.go`:
- `TestScenarioTheSidebarShowsAtWidth110`: a `newWorld` session with usage, open with `tea.WindowSizeMsg{Width: 110, Height: 40}`. `View()` contains `"│"` and `"thinking"`. At `Width: 109` it contains neither. At 110 after `key('c')` it contains neither, and `c` again brings it back.
- `TestScenarioTheSidebarFollowsTheCurrentSessionNotTheCursor`: two sessions in the family. The cursor moves onto the other session, and the sidebar's `context` line is unchanged.
- `TestScenarioEveryViewLineFitsTheWidth`: at `Width: 110`, every line of `View()` has `lipgloss.Width(line) <= 110`.
- `TestScenarioAnEditedLineSidebarShowsTheEstimate`: after a drop, as in `TestScenarioAnEditedLineShowsAnEstimatedContextSize`, the sidebar's first line starts `context ~`.

- [ ] **Step 2: Run to see them fail.**

- [ ] **Step 3: Implement.**
  - `tree.Build` copies `sess.Breakdown` into `SessionBreakdown` wherever it sets `SessionTokens`.
  - `sidebar.go` implements `sidebarLines` as specified.
  - In `View`, full-screen modes return early already, so they hide it, as the spec requires. In the normal path, when `u.width >= sidebarMin && !u.sidebarOff`:
    - Narrow every tree row and header by `sidebarWidth+1`: pass `u.width-sidebarWidth-1` where `u.width` is used today.
    - Build the rows block into its own builder.
    - Find the current session's node by walking `u.m.Roots` for the first node whose `SessionID == u.st.Current(u.current)`, falling back to `u.current` when `u.st` is nil.
    - Render `sidebarLines(n.SessionBreakdown, n.SessionTokens, n.TokensEstimated)`. Colour `thinking` with `StyleClaude` and the rest with `StyleMuted`, via `render`.
    - `lipgloss.JoinHorizontal(lipgloss.Top, rowsBlock, sep, side)`, where `sep` is a column of `│` rendered with `StyleTool`, as tall as the rows block.
    - The counter, footer and status stay full width below.
  - Handle `c` in the normal key switch: `u.sidebarOff = !u.sidebarOff`. Swallow it in the range, move, menu and review modes, as `u`/`U` are.
  - In the footer's second line, add `  c context` only when `u.width >= sidebarMin`. The narrow footer must still fit 80 columns.

- [ ] **Step 4: Docs.**
  - README Keys table: `| c | Show / hide the context sidebar (panes 110+ columns wide) |`.
  - README "What you see": a bullet on the sidebar, and the turn size after the count.
  - DECISIONS: the thinking finding, the per-turn growth rule and its fallbacks, and the fact that the sidebar is the current session, not the cursor.
  - AGENTS code map: `sidebar.go`, and the context.go line.

- [ ] **Step 5: Run** `go vet ./... && go test -count=1 ./...` → ok.

- [ ] **Step 6: Mutation.** Change `>= sidebarMin` to `> sidebarMin` → the width-110 scenario goes red. Revert.

- [ ] **Step 7: Commit** — `feat: a context sidebar shows where the session's context goes`.

---

## Self-review

| Spec | Task |
|---|---|
| §3.1 per-turn growth, fallbacks (no usage, ≤0, boundary) | 1 |
| §3.2 six types, thinking remainder once per reply, scaling, estimated | 1 |
| §3.2 current line only | 1 (rewound test; pre-compact via `buildLine`) |
| §3.3 adapter/tree fields | 1, 3 |
| §4 sidebar: width 110, `c`, footer, current session, layout, estimate, `—`, colours, hidden in modes | 3 |
| §5 turn sizes: folded head / open prompt, `~`, group total, truncation | 2 |
| §6 read-only, no content | Global Constraints |
| §7 tests and mutations | each task |
