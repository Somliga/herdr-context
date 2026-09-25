package claude

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"herdr-tree/internal/adapter"
)

const seedText = CompactionPrefix + " u2..u3\n\nturns two and three, briefly"

// spliced runs Splice into a fresh projects dir and returns the new
// transcript's entries keyed by uuid, plus its uuids in file order.
func spliced(t *testing.T, src string, e adapter.Edit) (adapter.Spliced, map[string]Entry, []string) {
	t.Helper()
	t.Setenv("CLAUDE_PROJECTS_DIR", t.TempDir())
	before, _ := os.ReadFile(src)
	res, err := Splice(src, e, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(src)
	if string(before) != string(after) {
		t.Fatal("splicing modified the source transcript")
	}
	path := filepath.Join(ProjectsDir(), "*", res.SessionID+".jsonl")
	m, _ := filepath.Glob(path)
	if len(m) != 1 {
		t.Fatalf("want one spliced file, found %v", m)
	}
	fi, _ := os.Stat(m[0])
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("spliced file mode %v, want 0600", fi.Mode().Perm())
	}
	es, skipped, err := ParseFile(m[0])
	if err != nil || skipped > 0 {
		t.Fatalf("spliced file does not parse: %v skipped=%d", err, skipped)
	}
	by := map[string]Entry{}
	var order []string
	for _, e := range es {
		if u := e.UUID(); u != "" {
			if e.SessionID() != res.SessionID {
				t.Fatalf("%s carries session %q, want %q", u, e.SessionID(), res.SessionID)
			}
			by[u] = e
			order = append(order, u)
		}
	}
	return res, by, order
}

func seedOf(t *testing.T, by map[string]Entry) Entry {
	t.Helper()
	for _, e := range by {
		if strings.HasPrefix(e.Text(), "⤶") {
			return e
		}
	}
	t.Fatal("no seed entry in the spliced file")
	return Entry{}
}

func parent(e Entry) string { return e.ParentUUID() }

func TestCutRemovesWholeTurnsAndRejoinsTheRest(t *testing.T) {
	res, by, order := spliced(t, "testdata/splice.jsonl", adapter.Edit{From: "a2b", To: "a3"})
	want := []string{"pre1", "u1", "a1", "u4", "a4"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("kept %v, want %v", order, want)
	}
	if parent(by["u4"]) != "a1" {
		t.Fatalf("u4 rejoined to %q, want a1", parent(by["u4"]))
	}
	if res.Removed != 2 || res.After != "u4" {
		t.Fatalf("result %+v, want Removed 2, After u4", res)
	}
}

func TestCompactPutsTheSeedWhereTheRangeWas(t *testing.T) {
	_, by, order := spliced(t, "testdata/splice.jsonl", adapter.Edit{From: "u2", To: "u3", Seed: seedText})
	seed := seedOf(t, by)
	if parent(seed) != "a1" {
		t.Fatalf("seed parented to %q, want a1", parent(seed))
	}
	if parent(by["u4"]) != seed.UUID() {
		t.Fatalf("u4 rejoined to %q, want the seed", parent(by["u4"]))
	}
	for _, gone := range []string{"u2", "a2", "a2b", "tr1", "tr2", "at2", "a2c", "u3", "a3"} {
		if _, ok := by[gone]; ok {
			t.Fatalf("%s survived the compaction", gone)
		}
	}
	// File order puts the seed immediately before what follows it.
	for i, u := range order {
		if u == "u4" && order[i-1] != seed.UUID() {
			t.Fatalf("seed not written just before u4: %v", order)
		}
	}
}

func TestInsertKeepsEverythingAfter(t *testing.T) {
	seed := SummaryPrefix + " other\n\nwhat the branch found"
	res, by, _ := spliced(t, "testdata/splice.jsonl", adapter.Edit{After: "a1", Seed: seed})
	s := seedOf(t, by)
	if parent(s) != "a1" || parent(by["u2"]) != s.UUID() {
		t.Fatalf("seed under %q, u2 under %q; want a1 and the seed", parent(s), parent(by["u2"]))
	}
	for _, kept := range []string{"u2", "a2", "a2b", "tr1", "tr2", "at2", "a2c", "u3", "a3", "u4", "a4"} {
		if _, ok := by[kept]; !ok {
			t.Fatalf("%s was lost by an insert", kept)
		}
	}
	if res.Removed != 0 {
		t.Fatalf("an insert removed %d turns", res.Removed)
	}
}

// Inserting "after" a mid-turn entry lands after the WHOLE turn: anything
// else would separate a tool call from its result.
func TestInsertAfterAToolCallLandsAfterItsTurn(t *testing.T) {
	_, by, _ := spliced(t, "testdata/splice.jsonl", adapter.Edit{After: "a2b", Seed: seedText})
	s := seedOf(t, by)
	if parent(s) != "a2c" || parent(by["u3"]) != s.UUID() {
		t.Fatalf("seed under %q, u3 under %q; want a2c and the seed", parent(s), parent(by["u3"]))
	}
}

func TestCompactingTheFirstTurnKeepsThePreamble(t *testing.T) {
	_, by, _ := spliced(t, "testdata/splice.jsonl", adapter.Edit{From: "u1", To: "a1", Seed: seedText})
	if parent(seedOf(t, by)) != "pre1" {
		t.Fatalf("seed parented to %q, want the preamble's pre1", parent(seedOf(t, by)))
	}
}

func TestWithNoPreambleTheSeedBecomesTheRoot(t *testing.T) {
	_, by, _ := spliced(t, "testdata/parallel.jsonl", adapter.Edit{From: "u1", To: "a3", Seed: seedText})
	s := seedOf(t, by)
	if s.Raw["parentUuid"] != nil {
		t.Fatalf("seed parent %v, want null", s.Raw["parentUuid"])
	}
	if parent(by["u2"]) != s.UUID() {
		t.Fatal("u2 not rejoined to the root seed")
	}
}

func TestACutFromTheFirstTurnMakesTheNextTurnTheRoot(t *testing.T) {
	_, by, _ := spliced(t, "testdata/parallel.jsonl", adapter.Edit{From: "u1", To: "a1"})
	if by["u2"].Raw["parentUuid"] != nil {
		t.Fatalf("u2 parent %v, want null", by["u2"].Raw["parentUuid"])
	}
}

func TestCompactingEverythingLeavesOnlyTheSeed(t *testing.T) {
	_, by, _ := spliced(t, "testdata/parallel.jsonl", adapter.Edit{From: "u1", To: "u2", Seed: seedText})
	if len(by) != 1 {
		t.Fatalf("kept %d entries, want only the seed", len(by))
	}
}

// The leaf pointer names the tip when something follows the edit, and the
// seed when the edit reached the end.
func TestTheLeafPointerFollowsTheEdit(t *testing.T) {
	for _, c := range []struct {
		e    adapter.Edit
		want func(map[string]Entry) string
	}{
		{adapter.Edit{From: "u2", To: "u3", Seed: seedText}, func(map[string]Entry) string { return "a4" }},
		{adapter.Edit{From: "u3", To: "a4", Seed: seedText}, func(by map[string]Entry) string { return seedOf(t, by).UUID() }},
	} {
		res, by, _ := spliced(t, "testdata/splice.jsonl", c.e)
		path, _ := filepath.Glob(filepath.Join(ProjectsDir(), "*", res.SessionID+".jsonl"))
		es, _, _ := ParseFile(path[0])
		leaf := ""
		for _, e := range es {
			if e.Type() == "last-prompt" {
				leaf, _ = e.Raw["leafUuid"].(string)
			}
		}
		if leaf != c.want(by) {
			t.Fatalf("%+v: leaf %q, want %q", c.e, leaf, c.want(by))
		}
	}
}

func TestSpliceRefuses(t *testing.T) {
	t.Setenv("CLAUDE_PROJECTS_DIR", t.TempDir())
	for name, c := range map[string]struct {
		e    adapter.Edit
		want error
	}{
		"a cut of every turn": {adapter.Edit{From: "u1", To: "a4"}, ErrNothingLeft},
		"a rewound stretch":   {adapter.Edit{From: "x1", To: "xa1"}, ErrNotOnLine},
		"an unmarked seed":    {adapter.Edit{From: "u2", To: "u3", Seed: "no prefix"}, ErrUnmarkedSeed},
	} {
		if _, err := Splice("testdata/splice.jsonl", c.e, t.TempDir()); !errors.Is(err, c.want) {
			t.Fatalf("%s: err = %v, want %v", name, err, c.want)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(ProjectsDir(), "*", "*.jsonl")); len(left) != 0 {
		t.Fatalf("a refused splice wrote %v", left)
	}
}

// Splice reads the source when it runs, not when the range was chosen: turns
// the agent added in between are kept, after the edit.
func TestTurnsAddedSinceTheRangeWasChosenAreKept(t *testing.T) {
	src := filepath.Join(t.TempDir(), "s.jsonl")
	b, _ := os.ReadFile("testdata/splice.jsonl")
	b = append(b, []byte(`{"type":"user","uuid":"u5","parentUuid":"a4","sessionId":"S","timestamp":"2026-01-01T12:01:00Z","message":{"role":"user","content":[{"type":"text","text":"five"}]}}`+"\n"+
		`{"type":"assistant","uuid":"a5","parentUuid":"u5","sessionId":"S","requestId":"r6","timestamp":"2026-01-01T12:01:01Z","message":{"role":"assistant","content":[{"type":"text","text":"reply five"}]}}`+"\n")...)
	if err := os.WriteFile(src, b, 0o600); err != nil {
		t.Fatal(err)
	}
	_, by, _ := spliced(t, src, adapter.Edit{From: "u2", To: "u3"})
	if parent(by["u5"]) != "a4" || parent(by["a5"]) != "u5" {
		t.Fatal("turns appended after the range was chosen were not kept in place")
	}
}

func TestWidenReportsTurnNumbersAndTheReadingEnd(t *testing.T) {
	s, err := Widen("testdata/splice.jsonl", "a3", "a2b")
	if err != nil {
		t.Fatal(err)
	}
	if s.First != 2 || s.Last != 3 || s.End != "a3" || s.EndNode != "a3" || s.Turns != 4 {
		t.Fatalf("span %+v, want 2..3 ending a3, of 4 turns", s)
	}
}

// compacted.jsonl is a native /compact: the parent chain restarts at a
// compact_boundary (parentUuid null), followed by the isCompactSummary entry,
// which no one typed and so is preamble (§3.4).
const compacted = "testdata/compacted.jsonl"

func TestARangeBeforeANativeCompactIsNotOnTheLine(t *testing.T) {
	t.Setenv("CLAUDE_PROJECTS_DIR", t.TempDir())
	if _, err := Splice(compacted, adapter.Edit{From: "u1", To: "u3"}, t.TempDir()); !errors.Is(err, ErrNotOnLine) {
		t.Fatalf("err %v, want ErrNotOnLine", err)
	}
}

func TestCompactingEveryTurnAfterANativeCompactKeepsTheBoundary(t *testing.T) {
	_, by, order := spliced(t, compacted, adapter.Edit{From: "u3", To: "a4", Seed: seedText})
	seed := seedOf(t, by)
	if want := "cb,cs," + seed.UUID(); strings.Join(order, ",") != want {
		t.Fatalf("kept %v, want %s", order, want)
	}
	if by["cb"].ParentUUID() != "" || parent(by["cs"]) != "cb" || parent(seed) != "cs" {
		t.Fatalf("boundary under %q, summary under %q, seed under %q", parent(by["cb"]), parent(by["cs"]), parent(seed))
	}
}

func TestCuttingATurnAfterANativeCompactKeepsTheBoundaryAsRoot(t *testing.T) {
	_, by, order := spliced(t, compacted, adapter.Edit{From: "u3", To: "a3"})
	if want := "cb,cs,u4,a4"; strings.Join(order, ",") != want {
		t.Fatalf("kept %v, want %s", order, want)
	}
	if by["cb"].ParentUUID() != "" || parent(by["u4"]) != "cs" {
		t.Fatalf("boundary under %q, u4 under %q", parent(by["cb"]), parent(by["u4"]))
	}
}

// moved maps each source uuid to the entry of the spliced file that carries
// the same line: every splice.jsonl entry has its own timestamp, which a move
// copies verbatim.
func moved(t *testing.T, src string, by map[string]Entry) map[string]Entry {
	t.Helper()
	es, _, err := ParseFile(src)
	if err != nil {
		t.Fatal(err)
	}
	at := map[string]Entry{}
	for _, e := range by {
		at[e.str("timestamp")] = e
	}
	out := map[string]Entry{}
	for _, e := range es {
		if n, ok := at[e.str("timestamp")]; ok && e.UUID() != "" {
			out[e.UUID()] = n
		}
	}
	return out
}

// checkMoved asserts each of ids is in the output under a fresh uuid, never
// its old one, with its parent mapped inside the moved set.
func checkMoved(t *testing.T, by map[string]Entry, m map[string]Entry, ids ...string) {
	t.Helper()
	set := map[string]bool{}
	for _, id := range ids {
		set[id] = true
	}
	for _, id := range ids {
		n, ok := m[id]
		if !ok {
			t.Fatalf("%s did not move", id)
		}
		if n.UUID() == id {
			t.Errorf("%s moved under its old uuid", id)
		}
		if _, ok := by[id]; ok {
			t.Errorf("%s is still in the file under its old uuid", id)
		}
	}
}

func leafOf(t *testing.T, sid string) string {
	t.Helper()
	path, _ := filepath.Glob(filepath.Join(ProjectsDir(), "*", sid+".jsonl"))
	es, _, _ := ParseFile(path[0])
	leaf := ""
	for _, e := range es {
		if e.Type() == "last-prompt" {
			leaf, _ = e.Raw["leafUuid"].(string)
		}
	}
	return leaf
}

var turn2 = []string{"u2", "a2", "a2b", "tr1", "tr2", "at2", "a2c"}

// Turn 2, with its parallel tool calls, moved after turn 4: 1, 3, 4, 2.
func TestAMoveLaterWithinTheLine(t *testing.T) {
	src := "testdata/splice.jsonl"
	self := &adapter.Session{ID: "S", Path: src}
	res, by, order := spliced(t, src, adapter.Edit{From: "a2b", After: "a4", Carry: self})
	m := moved(t, src, by)
	checkMoved(t, by, m, turn2...)
	if parent(by["u3"]) != "a1" {
		t.Errorf("u3 under %q, want a1: the gap closes", parent(by["u3"]))
	}
	if parent(m["u2"]) != "a4" {
		t.Errorf("moved u2 under %q, want a4", parent(m["u2"]))
	}
	for child, p := range map[string]string{"a2": "u2", "a2b": "a2", "tr1": "a2", "tr2": "a2b", "at2": "u2", "a2c": "tr1"} {
		if parent(m[child]) != m[p].UUID() {
			t.Errorf("moved %s under %q, want moved %s", child, parent(m[child]), p)
		}
	}
	if got := leafOf(t, res.SessionID); got != m["a2c"].UUID() {
		t.Errorf("leaf %q, want the moved a2c", got)
	}
	// File order follows the new line.
	var got []string
	for _, u := range order {
		if u == m["u2"].UUID() {
			u = "u2*"
		}
		if u == "u1" || u == "u3" || u == "u4" || u == "u2*" {
			got = append(got, u)
		}
	}
	if strings.Join(got, ",") != "u1,u3,u4,u2*" {
		t.Errorf("turn order %v, want u1,u3,u4,u2*", got)
	}
	if res.Removed != 1 || res.First != m["u2"].UUID() || res.After != "u3" {
		t.Errorf("result %+v, want 1 moved, first the moved u2, after u3", res)
	}
}

// Turn 4 moved after turn 1: 1, 4, 2, 3. The line now ends on turn 3.
func TestAMoveEarlierWithinTheLine(t *testing.T) {
	src := "testdata/splice.jsonl"
	self := &adapter.Session{ID: "S", Path: src}
	res, by, _ := spliced(t, src, adapter.Edit{From: "a4", After: "u1", Carry: self})
	m := moved(t, src, by)
	checkMoved(t, by, m, "u4", "a4")
	if parent(m["u4"]) != "a1" || parent(m["a4"]) != m["u4"].UUID() {
		t.Errorf("moved u4 under %q, a4 under %q", parent(m["u4"]), parent(m["a4"]))
	}
	if parent(by["u2"]) != m["a4"].UUID() {
		t.Errorf("u2 under %q, want the moved a4", parent(by["u2"]))
	}
	if got := leafOf(t, res.SessionID); got != "a3" {
		t.Errorf("leaf %q, want a3", got)
	}
	if res.After != "" || res.First != m["u4"].UUID() {
		t.Errorf("result %+v", res)
	}
}

// T is a branch of S: it holds S's turns 1 and 2 under the same uuids, then
// one of its own. Moving S's turn 2 into T after b1 copies it under fresh
// uuids, so T never holds one uuid twice.
func TestAMoveIntoALineThatHoldsCopiesOfTheTurns(t *testing.T) {
	dir := t.TempDir()
	b, _ := os.ReadFile("testdata/splice.jsonl")
	var lines []string
	for _, l := range strings.Split(string(b), "\n") {
		if strings.Contains(l, `"uuid":"u3"`) {
			break
		}
		if !strings.Contains(l, `"x1"`) && !strings.Contains(l, `"xa1"`) {
			lines = append(lines, strings.ReplaceAll(l, `"sessionId":"S"`, `"sessionId":"T"`))
		}
	}
	lines = append(lines,
		`{"type":"user","uuid":"b1","parentUuid":"a2c","sessionId":"T","timestamp":"2026-01-02T12:00:00Z","message":{"role":"user","content":[{"type":"text","text":"own"}]}}`,
		`{"type":"assistant","uuid":"ba1","parentUuid":"b1","sessionId":"T","requestId":"rb","timestamp":"2026-01-02T12:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"own reply"}]}}`)
	dst := filepath.Join(dir, "T.jsonl")
	if err := os.WriteFile(dst, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srcBefore, _ := os.ReadFile("testdata/splice.jsonl")

	res, by, order := spliced(t, dst, adapter.Edit{From: "tr2", After: "ba1",
		Carry: &adapter.Session{ID: "S", Path: "testdata/splice.jsonl"}})
	if after, _ := os.ReadFile("testdata/splice.jsonl"); string(after) != string(srcBefore) {
		t.Fatal("a move modified the line it carried from")
	}
	seen := map[string]bool{}
	for _, u := range order {
		if seen[u] {
			t.Fatalf("%s is in the file twice", u)
		}
		seen[u] = true
	}
	for _, id := range turn2 {
		if _, ok := by[id]; !ok {
			t.Errorf("T's own copy of %s was lost", id)
		}
	}
	if len(order) != len(lines)+len(turn2) {
		t.Fatalf("%d entries, want T's %d plus 7 moved", len(order), len(lines))
	}
	var first Entry
	for _, e := range by {
		if parent(e) == "ba1" {
			first = e
		}
	}
	if first.Text() != "two" || res.First != first.UUID() || res.Removed != 1 {
		t.Fatalf("after ba1 comes %q (%s), result %+v; want the moved prompt two", first.Text(), first.UUID(), res)
	}
	if got := leafOf(t, res.SessionID); got == "ba1" || by[got].Text() != "reply two" {
		t.Errorf("leaf %q, want the moved a2c", got)
	}
}

func TestMoveRefuses(t *testing.T) {
	t.Setenv("CLAUDE_PROJECTS_DIR", t.TempDir())
	src := "testdata/splice.jsonl"
	self := &adapter.Session{ID: "S", Path: src}
	// one is a line of one turn: the preamble, u1 and a1.
	one := filepath.Join(t.TempDir(), "one.jsonl")
	b, _ := os.ReadFile(src)
	if err := os.WriteFile(one, []byte(strings.Join(strings.SplitAfter(string(b), "\n")[:3], "")), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		dst  string
		e    adapter.Edit
		want error
	}{
		"into itself":           {src, adapter.Edit{From: "u2", After: "a2b", Carry: self}, ErrMoveNowhere},
		"after the turn before": {src, adapter.Edit{From: "u2", After: "a1", Carry: self}, ErrMoveNowhere},
		"a line's only turn":    {src, adapter.Edit{From: "a1", After: "u2", Carry: &adapter.Session{ID: "O", Path: one}}, ErrNothingLeft},
		"the preamble":          {src, adapter.Edit{From: "pre1", After: "a4", Carry: self}, ErrMovePreamble},
		"a target off the line": {src, adapter.Edit{From: "u2", After: "x1", Carry: self}, ErrNotOnLine},
		"a turn off the line":   {src, adapter.Edit{From: "x1", After: "a4", Carry: self}, ErrNotOnLine},
		"a seed":                {src, adapter.Edit{From: "u2", After: "a4", Carry: self, Seed: seedText}, ErrMoveSeed},
	} {
		if _, err := Splice(c.dst, c.e, t.TempDir()); !errors.Is(err, c.want) {
			t.Fatalf("%s: err = %v, want %v", name, err, c.want)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(ProjectsDir(), "*", "*.jsonl")); len(left) != 0 {
		t.Fatalf("a refused move wrote %v", left)
	}
}
