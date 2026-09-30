# Undo/Redo and Context Meter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `u`/`U` undo and redo a line's edits (a cross-line move as one), and each session header shows the line's real context size, with an estimate in the squash review.

**Architecture:** The store gains `Undone`/`UndoneAt`/`Edit` on records and one resolver, `Current`, that skips undone replacements; the tree and TUI switch from `Resolve` to `Current`. Undo/redo only write the store. The Claude adapter reads the last reply's `usage` while discovering and exposes it on `adapter.Session`; `Widen` also reports the range's and line's byte sizes for the review estimate.

**Tech Stack:** Go 1.27, Bubble Tea, lipgloss; Claude Code JSONL transcripts.

**Spec:** `docs/superpowers/specs/2026-09-28-undo-and-context-meter-design.md` (binding), on top of `docs/superpowers/specs/2026-09-23-context-editing-design.md`. Read `AGENTS.md` for the repo map and hard rules.

## Global Constraints

- Never modify a source transcript; never delete a session. Undo/redo write only the store.
- Never put message content (summaries, seeds, titles) in a status line.
- No test reaches the real `herdr` or `claude`: tui tests use injected `live`/`closePane` and `fakeAdapter` or the scenario harness `newWorld` (real files, `world.durations` for real-shaped turns); claude tests use `stubClaude`.
- Never run `claude`, `scripts/verify-*.sh`, `cmd/timelinecheck`, `cmd/graftcheck`; never write `bin/`.
- Mutate each behaviour fix live-but-wrong, see a test fail, revert.
- `go vet ./... && go test -count=1 ./...` clean before every commit; commits end `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Never push.
- The footer fits 80 columns (existing fit test).

## Review Focus

1. **Undo after a *second* overlay edited the same line** — must refuse (`this line was changed in another overlay — reopen the tree`), never undo the wrong version. Pinned in Task 3.
2. **Undo on a line whose newest version is open in a pane running a turn** — busy check covers panes holding *any* version, including an undone one. Pinned in Task 3.
3. **Undo, then a new edit, then `U`** — must say `nothing to redo`, and the tree must show the new edit, not the undone one. Pinned in Tasks 1 and 3.
4. **A branch made on a version that is later undone** — must still be visible (re-attached or a marked root), never vanish. Pinned in Task 2.
5. **A reply split across several entries** (Claude Code writes one entry per content block, all carrying the same `usage`) — counted once, not summed. Pinned in Task 4.

---

## File map

| File | Change |
|---|---|
| `internal/store/store.go` | `Undone`, `UndoneAt`, `Edit` fields; `Current`, `Lineage`, `CurrentOnDisk`; merge rule |
| `internal/tree/tree.go` | hide/resolve through `Current`; undone sessions hidden; re-attach from undone |
| `internal/tui/undo.go` (new) | `undoCmd`, `redoCmd`, group lookup, statuses |
| `internal/tui/view.go` | `u`/`U` keys, footer, `Current` for scope, header number |
| `internal/tui/edit.go` | `changedElsewhere` via `CurrentOnDisk`; `openTip` via `Lineage`; `editOp.editID` on moves; review estimate line |
| `internal/adapter/adapter.go` | `Session.ContextTokens`; `Span.RangeBytes`, `Span.LineBytes` |
| `internal/claude/{discover,line,splice}.go` | compute context tokens; fill span bytes |
| `internal/tree/tree.go` | `Node.SessionTokens` |

---

### Task 1: Store — undone versions and one resolver

**Files:**
- Modify: `internal/store/store.go`
- Test: `internal/store/store_test.go`

**Interfaces:**
- Produces:
  - `Branch.Undone bool` (`json:"undone,omitempty"`), `Branch.UndoneAt time.Time` (`json:"undone_at,omitempty"` — last time `Undone` was toggled, by undo **or** redo), `Branch.Edit string` (`json:"edit,omitempty"`).
  - `func (s *Store) Current(sid string) string` — the current version of sid's line.
  - `func (s *Store) Lineage(sid string) []string` — every session whose `Current` is sid (sid included), stable order.
  - `func (s *Store) CurrentOnDisk(sid string) (string, error)` — `Current` evaluated on a fresh `Load`.
  - `func (s *Store) Group(sid string) []string` — sid plus every record sharing its non-empty `Edit`, sorted.

- [ ] **Step 1: Write the failing tests**

```go
func TestCurrentSkipsUndoneReplacements(t *testing.T) {
	s := &Store{Branches: map[string]Branch{}}
	s.Replace("a", "b", Branch{Kind: KindCompacted})
	s.Replace("b", "c", Branch{Kind: KindCut})
	if got := s.Current("a"); got != "c" {
		t.Fatalf("Current(a) = %q, want c", got)
	}
	c := s.Branches["c"]; c.Undone = true; s.Branches["c"] = c
	if got := s.Current("a"); got != "b" {
		t.Fatalf("with c undone, Current(a) = %q, want b", got)
	}
	if got := s.Current("c"); got != "b" {
		t.Fatalf("an undone version resolves back to the current one: Current(c) = %q, want b", got)
	}
	b := s.Branches["b"]; b.Undone = true; s.Branches["b"] = b
	if got := s.Current("c"); got != "a" {
		t.Fatalf("two undone: Current(c) = %q, want a", got)
	}
}

func TestANewEditAfterUndoReplacesTheCurrentVersion(t *testing.T) {
	s := &Store{Branches: map[string]Branch{}}
	s.Replace("a", "b", Branch{Kind: KindCompacted})
	b := s.Branches["b"]; b.Undone = true; s.Branches["b"] = b
	s.Replace("a", "d", Branch{Kind: KindCut})
	if got := s.Current("a"); got != "d" {
		t.Fatalf("Current(a) = %q, want d", got)
	}
	if got := s.Current("b"); got != "d" {
		t.Fatalf("the abandoned undone version resolves to the line's current: %q, want d", got)
	}
}

func TestLineageListsEveryVersionOfTheLine(t *testing.T) {
	s := &Store{Branches: map[string]Branch{}}
	s.Replace("a", "b", Branch{})
	s.Replace("b", "c", Branch{})
	c := s.Branches["c"]; c.Undone = true; s.Branches["c"] = c
	got := s.Lineage("b")
	sort.Strings(got)
	if strings.Join(got, ",") != "a,b,c" {
		t.Fatalf("Lineage(b) = %v, want a,b,c", got)
	}
}

func TestGroupFindsTheOtherHalfOfAMove(t *testing.T) {
	s := &Store{Branches: map[string]Branch{
		"t2": {Edit: "e1"}, "s2": {Edit: "e1"}, "x": {Edit: "e2"}, "y": {},
	}}
	if g := s.Group("t2"); strings.Join(g, ",") != "s2,t2" {
		t.Fatalf("Group(t2) = %v, want s2,t2", g)
	}
	if g := s.Group("y"); strings.Join(g, ",") != "y" {
		t.Fatalf("Group(y) = %v, want y", g)
	}
}

func TestSaveMergesUndoneByTheLaterToggle(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", t.TempDir())
	s1, _ := Load("/repo")
	s1.Replace("a", "b", Branch{})
	if err := s1.Save(); err != nil {
		t.Fatal(err)
	}
	s2, _ := Load("/repo")
	b := s1.Branches["b"]; b.Undone, b.UndoneAt = true, time.Now().UTC(); s1.Branches["b"] = b
	if err := s1.Save(); err != nil {
		t.Fatal(err)
	}
	s2.SetLabel("a", "t1", "x") // an unrelated save from a stale overlay
	if err := s2.Save(); err != nil {
		t.Fatal(err)
	}
	after, _ := Load("/repo")
	if !after.Branches["b"].Undone {
		t.Fatal("a stale save cleared an undo")
	}
}

func TestCurrentOnDiskSeesAnotherOverlaysUndo(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", t.TempDir())
	s1, _ := Load("/repo")
	s1.Replace("a", "b", Branch{})
	_ = s1.Save()
	s2, _ := Load("/repo")
	b := s2.Branches["b"]; b.Undone, b.UndoneAt = true, time.Now().UTC(); s2.Branches["b"] = b
	_ = s2.Save()
	got, err := s1.CurrentOnDisk("a")
	if err != nil || got != "a" {
		t.Fatalf("CurrentOnDisk(a) = %q, %v; want a", got, err)
	}
}
```

- [ ] **Step 2: Run to see them fail**

Run: `go test ./internal/store/ -run 'Current|Lineage|Group|MergesUndone|OnDisk' -v`
Expected: FAIL to compile (`Current` undefined).

- [ ] **Step 3: Implement**

Add the three fields to `Branch` (after `MovedFrom`), with this comment:
`// Undo sets Undone on the version it takes back; the version it replaced is current again. UndoneAt is the last time Undone was toggled (undo or redo), for merging. Edit groups the records one edit wrote (a cross-line move writes two).`

```go
// Current is the version of sid's line that is shown: follow replaced_by
// forward through versions that are not undone; from an undone version,
// first step back through replaces to one that is not.
func (s *Store) Current(sid string) string {
	seen := map[string]bool{}
	for s.Branches[sid].Undone && !seen[sid] {
		seen[sid] = true
		prev := s.Branches[sid].Replaces
		if prev == "" {
			return sid // an undone record with nothing before it: shown as is
		}
		sid = prev
	}
	seen = map[string]bool{}
	for !seen[sid] {
		seen[sid] = true
		next := s.Branches[sid].ReplacedBy
		if next == "" || s.Branches[next].Undone {
			return sid
		}
		sid = next
	}
	return sid // a cycle in a hand-edited store: stop where it closed
}

// Lineage is every session whose Current is sid, sid included, sorted.
func (s *Store) Lineage(sid string) []string {
	out := []string{sid}
	for id := range s.Branches {
		if id != sid && s.Current(id) == sid {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// Group is sid and every record that shares its Edit, sorted.
func (s *Store) Group(sid string) []string {
	e := s.Branches[sid].Edit
	if e == "" {
		return []string{sid}
	}
	var out []string
	for id, b := range s.Branches {
		if b.Edit == e {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// CurrentOnDisk is Current as the store on disk has it, for the
// changed-in-another-overlay check.
func (s *Store) CurrentOnDisk(sid string) (string, error) {
	if s.path == "" {
		return "", ErrNoPath
	}
	onDisk, err := Load(s.RepoRoot)
	if err != nil {
		return "", err
	}
	return onDisk.Current(sid), nil
}
```

Make `Resolve` a thin alias kept for callers until Task 2 migrates them: `func (s *Store) Resolve(sid string) string { return s.Current(sid) }` — then delete `ReplacedOnDisk` once Task 3 replaces its one caller (leave it in Task 1 so the build stays green).

In `Save`'s merge loop, after the `ReplacedBy` rule:

```go
			// Undone toggles both ways: the later toggle wins.
			if b.UndoneAt.After(ours.UndoneAt) {
				ours.Undone, ours.UndoneAt = b.Undone, b.UndoneAt
				s.Branches[id] = ours
			}
```

(`ours` may already have been reassigned by the `ReplacedBy` rule above it — re-read `ours := s.Branches[id]` before this block so both rules compose.)

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/store/ -v 2>&1 | grep -E '^(--- FAIL|ok|FAIL)'`
Expected: `ok`.

- [ ] **Step 5: Mutation check** — make `Current` ignore `Undone` in the forward loop (`if next == ""`); `TestCurrentSkipsUndoneReplacements` must fail. Revert.

- [ ] **Step 6: Commit** — `feat: the store knows an undone version and resolves around it`.

---

### Task 2: Tree — hide and re-attach through `Current`

**Files:**
- Modify: `internal/tree/tree.go`, `internal/tui/view.go` (scope's `Resolve` → `Current`)
- Test: `internal/tree/tree_test.go`

**Interfaces:**
- Consumes: `Store.Current`, `Branch.Undone`.

- [ ] **Step 1: Write the failing tests** (use the existing `sess`/`sessionsIn` helpers in tree_test.go)

```go
func TestUndoShowsThePreviousVersion(t *testing.T) {
	st := &store.Store{Branches: map[string]store.Branch{}}
	st.Replace("v1", "v2", store.Branch{Kind: store.KindCut})
	st.Replace("v2", "v3", store.Branch{Kind: store.KindCompacted})
	v3 := st.Branches["v3"]; v3.Undone = true; st.Branches["v3"] = v3
	got := sessionsIn(Build([]adapter.Session{sess("v1", "t1"), sess("v2", "t1"), sess("v3", "t1")}, st))
	if !got["v2"] || got["v1"] || got["v3"] {
		t.Fatalf("shown %v, want only v2", got)
	}
}

func TestABranchOffAnUndoneVersionStaysVisible(t *testing.T) {
	st := &store.Store{Branches: map[string]store.Branch{
		"br": {GraftedFrom: store.From{SessionID: "v2", Node: "t1"}},
		"gone": {GraftedFrom: store.From{SessionID: "v2", Node: "only-in-v2"}},
	}}
	st.Replace("v1", "v2", store.Branch{Kind: store.KindCompacted})
	v2 := st.Branches["v2"]; v2.Undone = true; st.Branches["v2"] = v2
	roots := Build([]adapter.Session{
		sess("v1", "t1", "t2"), sess("v2", "t1", "only-in-v2"), sess("br", "b1"), sess("gone", "g1"),
	}, st)
	// br re-attaches under v1's t1; gone becomes a marked root
	var brUnderV1, goneMarked bool
	var walk func(n *Node)
	walk = func(n *Node) {
		for _, c := range n.Children {
			if n.SessionID == "v1" && n.Node.ID == "t1" && c.SessionID == "br" {
				brUnderV1 = true
			}
			walk(c)
		}
	}
	for _, r := range roots {
		walk(r)
		if r.SessionID == "gone" && r.FromRemoved {
			goneMarked = true
		}
	}
	if !brUnderV1 || !goneMarked {
		t.Fatalf("brUnderV1=%v goneMarked=%v, want both", brUnderV1, goneMarked)
	}
}
```

- [ ] **Step 2: Run to see them fail** — `go test ./internal/tree/ -run 'Undo|UndoneVersion' -v` → FAIL.

- [ ] **Step 3: Implement** in `Build`:
  - hidden: `if (br.ReplacedBy != "" && !s.Branches[br.ReplacedBy].Undone && present[s.Current(id)]) || (br.Undone && present[s.Current(id)]) { hidden[id] = true }`.
  - edges: `resolved = s.Current(from)` whenever `hidden[from]` (replacing `s.Resolve(from)`); the existing `FromRemoved` logic then applies unchanged.
  - markers: every `s.Resolve(...)` in the cut/move marker loop → `s.Current(...)`.
  - `internal/tui/view.go` rebuild's scope: `u.st.Resolve(u.current)` → `u.st.Current(u.current)`.
  - Delete `Store.Resolve` and move its test expectations onto `Current` (grep `Resolve(` — no callers remain).

- [ ] **Step 4: Run** `go test ./... ` → all ok.

- [ ] **Step 5: Mutation** — drop the `br.Undone` clause from hidden; `TestUndoShowsThePreviousVersion` fails. Revert.

- [ ] **Step 6: Commit** — `feat: the tree shows a line's current version, not an undone one`.

---

### Task 3: TUI — `u` undoes, `U` redoes

**Files:**
- Create: `internal/tui/undo.go`, `internal/tui/undo_test.go`
- Modify: `internal/tui/view.go` (keys, footer), `internal/tui/edit.go` (`changedElsewhere`, `openTip`, `carryCmd`/`editOp` edit id), `internal/store/store.go` (delete `ReplacedOnDisk`)

**Interfaces:**
- Consumes: `Store.Current`, `Lineage`, `Group`, `CurrentOnDisk`, `Branch.Undone/UndoneAt/Edit`, `busy()`, `actionDoneMsg{reload, tip}`, `verb(kind)`.
- Produces: `func undoCmd(st *store.Store, sid string, live LiveFunc) tea.Cmd`, `func redoCmd(st *store.Store, sid string, live LiveFunc) tea.Cmd`, `editOp.editID string`.

- [ ] **Step 1: Write the failing scenario tests** in `internal/tui/undo_test.go` using `newWorld` with `w.durations = true` (read scenario_test.go for `newWorld`, `open`, `drive`, `screen`, `paneLog` first). One test per bullet; each asserts the rendered screen (which line is shown, markers) and `u.status`:
  1. squash a range, `u` → the original line is shown, status `undone squash on <id8> — ⏎ on it to continue there`, cursor on its tip; `U` → the squashed line again, status `redone squash on …`.
  2. drop, squash, drop on one line, then `u` three times → the original; `U` twice → the second version.
  3. `u` on a line never edited → `nothing to undo on <id8>`; `U` with nothing undone → `nothing to redo on <id8>`.
  4. squash, `u`, then a new drop, then `U` → `nothing to redo on …`, and the drop is what's shown.
  5. cross-line move A→B; `u` on B → both A and B show their pre-move versions; `U` on A → both show the moved versions.
  6. cross-line move A→B, then drop on B; `u` on A → refused `<B8> was edited since — undo that first`, nothing changed.
  7. busy: the line's *previous* version is open in a pane that is `working` (paneLog maps that sid → working); `u` → refused `agent is working — wait for it to finish`, store unchanged on disk.
  8. changed elsewhere: a second store `Load`ed, undoes the line and saves; this overlay's `u` → refused `this line was changed in another overlay — reopen the tree`.
  9. `⏎` on the restored line's tip while a pane holds the undone version → the handover confirmation appears (not a plain resume).
  10. The footer (normal mode) contains `u undo` and `U redo`, and still fits 80 columns (extend the existing footer fit test's key list).

- [ ] **Step 2: Run to see them fail** — `go test ./internal/tui/ -run 'Undo|Redo' -v` → FAIL.

- [ ] **Step 3: Implement `internal/tui/undo.go`**

```go
package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"herdr-context/internal/store"
)

// undoCmd takes back the latest edit of sid's line (spec §2): the group's
// records are marked undone, so the versions they replaced are current
// again. It writes only the store.
func undoCmd(st *store.Store, sid string, live LiveFunc) tea.Cmd {
	return func() tea.Msg {
		cur := st.Current(sid)
		rec := st.Branches[cur]
		if rec.Replaces == "" {
			return actionDoneMsg{status: "nothing to undo on " + shortID(cur)}
		}
		return toggle(st, st.Group(cur), true, live, "undone "+verb(rec.Kind)+" on ", rec.Replaces)
	}
}

// redoCmd puts back the most recent undone edit of sid's line.
func redoCmd(st *store.Store, sid string, live LiveFunc) tea.Cmd {
	return func() tea.Msg {
		cur := st.Current(sid)
		next := st.Branches[cur].ReplacedBy
		if next == "" || !st.Branches[next].Undone {
			return actionDoneMsg{status: "nothing to redo on " + shortID(cur)}
		}
		return toggle(st, st.Group(next), false, live, "redone "+verb(st.Branches[next].Kind)+" on ", next)
	}
}

// toggle flips Undone on every record of one edit, after checking each of
// their lines: current at the version the edit expects, unchanged on disk,
// and no busy pane on any version of it.
func toggle(st *store.Store, group []string, undo bool, live LiveFunc, word, tip string) tea.Msg {
	for _, id := range group {
		// expect: for undo, id is its line's current version; for redo,
		// id's predecessor is.
		expect := id
		if !undo {
			expect = st.Branches[id].Replaces
		}
		line := st.Current(expect)
		if line != expect {
			return actionDoneMsg{status: shortID(line) + " was edited since — undo that first"}
		}
		if onDisk, err := st.CurrentOnDisk(expect); err != nil {
			return actionDoneMsg{status: "cannot tell whether this line was changed in another overlay: " + err.Error()}
		} else if onDisk != expect {
			return actionDoneMsg{status: "this line was changed in another overlay — reopen the tree"}
		}
		if live != nil {
			for _, v := range st.Lineage(expect) {
				_, status, err := live(v)
				if err != nil {
					return actionDoneMsg{status: "cannot tell whether this session is open: " + err.Error()}
				}
				if busy(status) {
					return actionDoneMsg{status: "agent is " + status + " — wait for it to finish"}
				}
			}
		}
	}
	now := time.Now().UTC()
	for _, id := range group {
		b := st.Branches[id]
		b.Undone, b.UndoneAt = undo, now
		st.Branches[id] = b
	}
	if err := st.Save(); err != nil {
		return actionDoneMsg{status: "not saved: " + err.Error()}
	}
	return actionDoneMsg{status: word + shortID(tip) + continueThere, reload: true, tip: tip}
}
```

Adjust names to what exists (`shortID`, `verb`, `busy`, `continueThere`, `actionDoneMsg` fields). If `verb` returns a past participle (`squashed`), add a sibling that returns the bare word (`squash`/`drop`/`move`/`merge`) — the spec's status uses the bare word.

- [ ] **Step 4: Wire the keys and the rest**
  - `Update`'s normal key switch: `case "u":` and `case "U":` on the selected row's `SessionID` → `undoCmd`/`redoCmd` with `u.busy = "undoing…"`/`"redoing…"`. Add `"u"`, `"U"` to every swallow list that has `"b"`/`"m"` (range, menus, confirm, review, move mode).
  - Footer line 2: add `u undo  U redo`; keep both lines ≤ 80 columns.
  - `changedElsewhere` (edit.go): replace `st.ReplacedOnDisk(sid)` with `cur, err := st.CurrentOnDisk(sid)` and refuse when `cur != sid`. Delete `Store.ReplacedOnDisk` and its tests, moving their expectations onto `CurrentOnDisk`.
  - `openTip` (edit.go): where it walks `u.st.Versions(n.SessionID)` looking for a pane on an older version, walk `u.st.Lineage(n.SessionID)` instead (excluding `n.SessionID` itself), so a pane on an undone version is found.
  - `editOp` gets `editID string`; `editCmd` copies it into the record it writes (`b.Edit = op.editID`). `carryCmd` sets one fresh id (`newEditID()` — 16 random hex chars from `crypto/rand`) on both its target op and its drop op. Other edits leave it empty.

- [ ] **Step 5: Run** `go test ./internal/tui/ -run 'Undo|Redo|Footer' -v` then `go vet ./... && go test -count=1 ./...` → all ok.

- [ ] **Step 6: Mutations** (record RED, revert): toggle only `group[0]` → test 5 fails; skip the `Lineage` busy walk → test 7 fails; skip `CurrentOnDisk` → test 8 fails.

- [ ] **Step 7: Commit** — `feat: u undoes a line's last edit, U redoes it`.

---

### Task 4: The context meter

**Files:**
- Modify: `internal/adapter/adapter.go`, `internal/claude/discover.go`, `internal/claude/line.go` (a helper), `internal/claude/splice.go` (`Widen`), `internal/tree/tree.go`, `internal/tui/view.go` (`headerLine`), `internal/tui/edit.go` (review line)
- Test: `internal/claude/line_test.go`, `internal/tui/view_test.go`, `internal/tui/review_test.go`

**Interfaces:**
- Produces: `adapter.Session.ContextTokens int` (0 = unknown); `adapter.Span.RangeBytes, LineBytes int64`; `tree.Node.SessionTokens int`; `func contextTokens(es []Entry) int` (claude); `func humanTokens(n int) string` (tui).

- [ ] **Step 1: Failing tests**

```go
// internal/claude/line_test.go
func TestContextTokensCountsTheLastReplyOnce(t *testing.T) {
	u := `"usage":{"input_tokens":2,"cache_read_input_tokens":800,"cache_creation_input_tokens":38,"output_tokens":9}`
	lines := []string{
		`{"type":"user","uuid":"p1","parentUuid":null,"sessionId":"S","message":{"role":"user","content":"hi"}}`,
		`{"type":"assistant","uuid":"a1","parentUuid":"p1","sessionId":"S","requestId":"r1","message":{"id":"m1","role":"assistant","content":[{"type":"text","text":"x"}],` + u + `}}`,
		`{"type":"assistant","uuid":"a2","parentUuid":"a1","sessionId":"S","requestId":"r1","message":{"id":"m1","role":"assistant","content":[{"type":"tool_use","id":"t","name":"Bash","input":{}}],` + u + `}}`,
		`{"type":"system","subtype":"turn_duration","uuid":"d1","parentUuid":"a2","sessionId":"S"}`,
	}
	es := parseLines(t, lines) // write a tiny helper: ParseFile on a temp file
	if got := contextTokens(es); got != 840 {
		t.Fatalf("contextTokens = %d, want 840 (one reply, counted once)", got)
	}
}

func TestContextTokensIsZeroWithoutUsage(t *testing.T) {
	es := parseLines(t, []string{
		`{"type":"user","uuid":"p1","parentUuid":null,"sessionId":"S","message":{"role":"user","content":"hi"}}`,
	})
	if got := contextTokens(es); got != 0 {
		t.Fatalf("contextTokens = %d, want 0", got)
	}
}
```

```go
// internal/tui/view_test.go
func TestHeaderShowsTheContextNumber(t *testing.T) {
	n := &tree.Node{SessionID: "1a2b3c4d-x", IsSessionRoot: true, Grafted: true, SessionTokens: 84210}
	if got := headerLine(Row{Node: n}, 80); got != "↳ 1a2b3c4d · 84k" {
		t.Fatalf("header %q", got)
	}
	n.SessionTokens = 0
	if got := headerLine(Row{Node: n}, 80); got != "↳ 1a2b3c4d" {
		t.Fatalf("no number when unknown: %q", got)
	}
}

func TestHumanTokens(t *testing.T) {
	for n, want := range map[int]string{999: "<1k", 1000: "1k", 84210: "84k", 889384: "889k", 1_234_000: "1.2M"} {
		if got := humanTokens(n); got != want {
			t.Fatalf("humanTokens(%d) = %q, want %q", n, got, want)
		}
	}
}
```

Review: in `review_test.go`, a squash where the fake line has `ContextTokens: 84000`, `Span{RangeBytes: 600, LineBytes: 1000}` and a 400-byte summary shows `context 84k → ~34k` (84000×0.4 + 100 = 33700 → `34k`); with `ContextTokens: 0` the line is absent.

- [ ] **Step 2: Run to see them fail.**

- [ ] **Step 3: Implement**
  - `contextTokens(es)`: walk the tip's chain (`tipOf`, parents via a uuid map) from the tip back; the first assistant entry whose `message.usage` is a map returns `input_tokens + cache_read_input_tokens + cache_creation_input_tokens` (JSON numbers are `float64`). None → 0. Because it stops at the first reply found from the tip, split entries of one reply are never summed.
  - `Discover` sets `ContextTokens: contextTokens(es)`.
  - `tree.Build` copies `sess.ContextTokens` into every node's `SessionTokens` (like `SessionTitle`).
  - `humanTokens`: `<1k` below 1000; `%dk` below 1,000,000 (rounded to nearest); `%.1fM` above.
  - `headerLine`: after the id, `" · " + humanTokens(n.SessionTokens)` when > 0; the existing width truncation keeps the id first because it's written first. `FromRemoved` text stays after it.
  - `Widen` fills `RangeBytes` (sum of `Marshal` sizes + 1 of the kept entries whose turn is in `a..b`) and `LineBytes` (all kept entries).
  - Review (edit.go `reviewLayout`): when the squash's source session has `ContextTokens > 0` (carry it on `squashing` from the confirm, looked up on the node's session), append `"context " + humanTokens(before) + " → ~" + humanTokens(est)` under the `Then:` block, `est = before*(1 - RangeBytes/LineBytes) + len(summary)/4`; count it in the height budget.

- [ ] **Step 4: Run** `go vet ./... && go test -count=1 ./...` → ok.

- [ ] **Step 5: Mutations** — sum all assistant usages on the chain → the split-reply test fails; drop the `> 0` guard → the no-number header test fails. Revert.

- [ ] **Step 6: Commit** — `feat: each header shows the line's context size; the squash review estimates what it saves`.

---

## Self-review

| Spec | Task |
|---|---|
| §2.1 keys, swallowed, footer | 3 |
| §2.2 model, Current, redo, new edit after undo, merge by undone_at | 1, 3 |
| §2.3 moves as one, refusal when edited since | 1 (Group), 3 |
| §2.4 branches off an undone version | 2 |
| §2.5 safety: busy on any version, changed elsewhere, handover, statuses, cursor | 3 |
| §3.1 the number, counted once, absent when none | 4 |
| §3.2 header, formatting, review estimate | 4 |
| §3.3 adapter field, no extra read | 4 |
| §4 context-editing amendments | 1–4 |
| §5 testing, mutations | each task |
