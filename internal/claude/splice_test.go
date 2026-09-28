package claude

import (
	"encoding/json"
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

// A spliced line shows no context number until its next real reply (§3.1/
// §3.2): the pre-edit usage on file no longer describes what this new line
// reads, so Splice must strip message.usage from every entry it writes.
func TestSpliceStripsUsageFromEveryWrittenEntry(t *testing.T) {
	u := `"usage":{"input_tokens":100,"cache_read_input_tokens":50,"cache_creation_input_tokens":0,"output_tokens":9}`
	lines := []string{
		`{"type":"user","uuid":"u1","parentUuid":null,"sessionId":"S","cwd":"/repo","version":"2.1.278","message":{"role":"user","content":[{"type":"text","text":"one"}]}}`,
		`{"type":"assistant","uuid":"a1","parentUuid":"u1","sessionId":"S","cwd":"/repo","version":"2.1.278","requestId":"r1","message":{"id":"m1","role":"assistant","content":[{"type":"text","text":"reply one"}],` + u + `}}`,
		`{"type":"user","uuid":"u2","parentUuid":"a1","sessionId":"S","cwd":"/repo","version":"2.1.278","message":{"role":"user","content":[{"type":"text","text":"two"}]}}`,
		`{"type":"assistant","uuid":"a2","parentUuid":"u2","sessionId":"S","cwd":"/repo","version":"2.1.278","requestId":"r2","message":{"id":"m2","role":"assistant","content":[{"type":"text","text":"reply two"}],` + u + `}}`,
	}
	p := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, _, _ := spliced(t, p, adapter.Edit{From: "u1", To: "a1"}) // cut turn one, keep turn two
	path, _ := filepath.Glob(filepath.Join(ProjectsDir(), "*", res.SessionID+".jsonl"))
	es, skipped, err := ParseFile(path[0])
	if err != nil || skipped > 0 {
		t.Fatalf("spliced file does not parse: %v skipped=%d", err, skipped)
	}
	if got := contextTokens(es); got != 0 {
		t.Fatalf("contextTokens on the spliced line = %d, want 0", got)
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

// checkValid fails unless path is a transcript Claude Code could have
// written: one root, every parent present, no uuid twice, every tool_use
// answered by a tool_result and the other way round.
func checkValid(t *testing.T, path string) {
	t.Helper()
	es, skipped, err := ParseFile(path)
	if err != nil || skipped > 0 {
		t.Fatalf("does not parse: %v, %d skipped", err, skipped)
	}
	have := map[string]bool{}
	uses, results := map[string]bool{}, map[string]bool{}
	for _, e := range es {
		if u := e.UUID(); u != "" {
			if have[u] {
				t.Fatalf("%s is in the file twice", u)
			}
			have[u] = true
		}
		msg, _ := e.Raw["message"].(map[string]any)
		blocks, _ := msg["content"].([]any)
		for _, b := range blocks {
			bm, _ := b.(map[string]any)
			switch bm["type"] {
			case "tool_use":
				uses[bm["id"].(string)] = true
			case "tool_result":
				results[bm["tool_use_id"].(string)] = true
			}
		}
	}
	roots := 0
	for _, e := range es {
		if e.UUID() == "" {
			continue
		}
		if p := e.ParentUUID(); p == "" {
			roots++
		} else if !have[p] {
			t.Fatalf("%s's parent %s is not in the file", e.UUID(), p)
		}
	}
	if roots != 1 {
		t.Fatalf("%d roots", roots)
	}
	for id := range uses {
		if !results[id] {
			t.Fatalf("tool_use %s has no tool_result", id)
		}
	}
	for id := range results {
		if !uses[id] {
			t.Fatalf("tool_result %s has no tool_use", id)
		}
	}
}

func splicedPath(t *testing.T, sid string) string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(ProjectsDir(), "*", sid+".jsonl"))
	if len(m) != 1 {
		t.Fatalf("transcript of %s: %v", sid, m)
	}
	return m[0]
}

var turn2 = []string{"u2", "a2", "a2b", "tr1", "tr2", "at2", "a2c"}

// Turn 2, with its parallel tool calls, moved after turn 4: 1, 3, 4, 2. A
// move within the line keeps every id, so branches and labels on it hold.
func TestAMoveLaterWithinTheLine(t *testing.T) {
	src := "testdata/splice.jsonl"
	self := &adapter.Session{ID: "S", Path: src}
	res, by, order := spliced(t, src, adapter.Edit{From: "a2b", After: "a4", Carry: self})
	for child, p := range map[string]string{"u3": "a1", "u2": "a4", "a2": "u2", "a2b": "a2", "tr1": "a2", "tr2": "a2b", "at2": "u2", "a2c": "tr1"} {
		if parent(by[child]) != p {
			t.Errorf("%s under %q, want %s", child, parent(by[child]), p)
		}
	}
	if by["a2"].RequestID() != "r2" || by["a2b"].RequestID() != "r2" {
		t.Error("a move within the line changed a requestId")
	}
	if got := leafOf(t, res.SessionID); got != "a2c" {
		t.Errorf("leaf %q, want a2c", got)
	}
	var got []string
	for _, u := range order {
		if u == "u1" || u == "u2" || u == "u3" || u == "u4" {
			got = append(got, u)
		}
	}
	if strings.Join(got, ",") != "u1,u3,u4,u2" {
		t.Errorf("turn order %v, want u1,u3,u4,u2", got)
	}
	if res.Removed != 1 || res.First != "u2" || res.After != "u3" {
		t.Errorf("result %+v, want 1 moved, first u2, after u3", res)
	}
	checkValid(t, splicedPath(t, res.SessionID))
}

// Turn 4 moved after turn 1: 1, 4, 2, 3. The line now ends on turn 3.
func TestAMoveEarlierWithinTheLine(t *testing.T) {
	src := "testdata/splice.jsonl"
	self := &adapter.Session{ID: "S", Path: src}
	res, by, _ := spliced(t, src, adapter.Edit{From: "a4", After: "u1", Carry: self})
	if parent(by["u4"]) != "a1" || parent(by["a4"]) != "u4" || parent(by["u2"]) != "a4" {
		t.Errorf("u4 under %q, a4 under %q, u2 under %q", parent(by["u4"]), parent(by["a4"]), parent(by["u2"]))
	}
	if got := leafOf(t, res.SessionID); got != "a3" {
		t.Errorf("leaf %q, want a3", got)
	}
	if res.After != "" || res.First != "u4" {
		t.Errorf("result %+v", res)
	}
	checkValid(t, splicedPath(t, res.SessionID))
}

// T is a branch of S: it holds S's turns 1 and 2 under the same ids, then one
// of its own. Moving S's turn 2 into T after it renews every id the turn
// carries — uuids, requestIds, message ids, tool_use ids — consistently, so
// T's line still divides into the right turns, and dropping the moved turn
// again leaves a valid transcript.
func TestAMoveIntoALineThatHoldsCopiesOfTheTurns(t *testing.T) {
	dir := t.TempDir()
	b, _ := os.ReadFile("testdata/splice.jsonl")
	// Real assistant entries carry a message id, shared by one request's
	// blocks; a tool result's toolUseResult can name its tool_use.
	var sLines, tLines []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		e, err := lineEntry(l)
		if err != nil {
			t.Fatal(err)
		}
		if msg, ok := e.Raw["message"].(map[string]any); ok && e.Type() == "assistant" {
			msg["id"] = "msg_" + e.RequestID()
		}
		if e.UUID() == "tr1" {
			e.Raw["toolUseResult"] = map[string]any{"tool_use_id": "t1"}
		}
		enc, _ := Marshal(e)
		sLines = append(sLines, string(enc))
		if u := e.UUID(); u == "u3" || u == "a3" || u == "u4" || u == "a4" || u == "sd4" || u == "x1" || u == "xa1" || e.Type() == "last-prompt" {
			continue
		}
		tLines = append(tLines, strings.ReplaceAll(string(enc), `"sessionId":"S"`, `"sessionId":"T"`))
	}
	tLines = append(tLines,
		`{"type":"user","uuid":"b1","parentUuid":"a2c","sessionId":"T","timestamp":"2026-01-02T12:00:00Z","message":{"role":"user","content":[{"type":"text","text":"own"}]}}`,
		`{"type":"assistant","uuid":"ba1","parentUuid":"b1","sessionId":"T","requestId":"rb","timestamp":"2026-01-02T12:00:01Z","message":{"id":"msg_rb","role":"assistant","content":[{"type":"text","text":"own reply"}]}}`)
	srcPath, dst := filepath.Join(dir, "S.jsonl"), filepath.Join(dir, "T.jsonl")
	for p, ls := range map[string][]string{srcPath: sLines, dst: tLines} {
		if err := os.WriteFile(p, []byte(strings.Join(ls, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	srcBefore, _ := os.ReadFile(srcPath)

	res, by, order := spliced(t, dst, adapter.Edit{From: "tr2", After: "ba1",
		Carry: &adapter.Session{ID: "S", Path: srcPath}})
	if after, _ := os.ReadFile(srcPath); string(after) != string(srcBefore) {
		t.Fatal("a move modified the line it carried from")
	}
	if len(order) != len(tLines)+len(turn2) {
		t.Fatalf("%d entries, want T's %d plus 7 moved", len(order), len(tLines))
	}
	for _, id := range turn2 {
		if _, ok := by[id]; !ok {
			t.Errorf("T's own copy of %s was lost", id)
		}
	}
	// The moved entries are the ones T did not have; timestamps say which is which.
	old := map[string]bool{}
	for _, l := range tLines {
		e, _ := lineEntry(l)
		old[e.UUID()] = true
		old[e.RequestID()] = true
		if msg, ok := e.Raw["message"].(map[string]any); ok {
			if id, _ := msg["id"].(string); id != "" {
				old[id] = true
			}
		}
	}
	m := map[string]Entry{}
	for _, e := range by {
		if !old[e.UUID()] {
			switch ts := e.str("timestamp"); ts {
			case "2026-01-01T12:00:05Z":
				m["u2"] = e
			case "2026-01-01T12:00:06Z":
				m["a2"] = e
			case "2026-01-01T12:00:07Z":
				m["a2b"] = e
			case "2026-01-01T12:00:08Z":
				m["tr1"] = e
			case "2026-01-01T12:00:09Z":
				m["tr2"] = e
			case "2026-01-01T12:00:10Z":
				m["at2"] = e
			case "2026-01-01T12:00:11Z":
				m["a2c"] = e
			}
		}
	}
	if len(m) != len(turn2) {
		t.Fatalf("found %d moved entries, want 7", len(m))
	}
	if parent(m["u2"]) != "ba1" || res.First != m["u2"].UUID() || res.Removed != 1 {
		t.Fatalf("moved u2 under %q, result %+v", parent(m["u2"]), res)
	}
	for child, p := range map[string]string{"a2": "u2", "a2b": "a2", "tr1": "a2", "tr2": "a2b", "at2": "u2", "a2c": "tr1"} {
		if parent(m[child]) != m[p].UUID() {
			t.Errorf("moved %s under %q, want moved %s", child, parent(m[child]), p)
		}
	}
	msgID := func(e Entry) string { id, _ := e.Raw["message"].(map[string]any)["id"].(string); return id }
	block := func(e Entry) map[string]any {
		return e.Raw["message"].(map[string]any)["content"].([]any)[0].(map[string]any)
	}
	for _, id := range []string{"a2", "a2b", "a2c"} {
		if r := m[id].RequestID(); r == "" || old[r] {
			t.Errorf("moved %s keeps requestId %q", id, r)
		}
		if mid := msgID(m[id]); mid == "" || old[mid] || !strings.HasPrefix(mid, "msg_") {
			t.Errorf("moved %s keeps message id %q", id, mid)
		}
	}
	if m["a2"].RequestID() != m["a2b"].RequestID() || msgID(m["a2"]) != msgID(m["a2b"]) || m["a2"].RequestID() == m["a2c"].RequestID() {
		t.Error("one request's blocks no longer share their renewed ids")
	}
	t1, t2 := block(m["a2"])["id"], block(m["a2b"])["id"]
	if t1 == "t1" || t2 == "t2" || block(m["tr1"])["tool_use_id"] != t1 || block(m["tr2"])["tool_use_id"] != t2 {
		t.Errorf("tool ids %v %v, results name %v %v", t1, t2, block(m["tr1"])["tool_use_id"], block(m["tr2"])["tool_use_id"])
	}
	if got := m["tr1"].Raw["toolUseResult"].(map[string]any)["tool_use_id"]; got != t1 {
		t.Errorf("toolUseResult names %v, want %v", got, t1)
	}
	if by["a2"].RequestID() != "r2" || block(by["a2"])["id"] != "t1" {
		t.Error("T's own copy lost its ids")
	}
	out := splicedPath(t, res.SessionID)
	checkValid(t, out)

	// T's line divides the moved entries into one turn of their own.
	l, err := parseForEdit(out)
	if err != nil {
		t.Fatal(err)
	}
	for id, e := range m {
		if l.turn[e.UUID()] != l.turn[m["u2"].UUID()] || l.turn[e.UUID()] == l.turn["u2"] {
			t.Errorf("moved %s is in turn %d, the moved turn is %d", id, l.turn[e.UUID()], l.turn[m["u2"].UUID()])
		}
	}
	// ...so a later drop of it takes exactly those entries.
	cut, err := Splice(out, adapter.Edit{From: m["u2"].UUID(), To: m["u2"].UUID()}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	checkValid(t, splicedPath(t, cut.SessionID))
	if es, _, _ := ParseFile(splicedPath(t, cut.SessionID)); len(es) != len(tLines)+1 {
		t.Errorf("after dropping the moved turn %d entries, want T's %d and a leaf pointer", len(es), len(tLines))
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

func lineEntry(l string) (Entry, error) {
	var m map[string]any
	err := json.Unmarshal([]byte(l), &m)
	return Entry{Raw: m}, err
}

// Server-side tools (web search) and MCP connector tools are answered inside
// the assistant's own message: a server_tool_use or mcp_tool_use block, then
// a *_tool_result block naming it. A cross-line move renews their ids like a
// tool_use's, so a target holding a copy of the turn carries no id twice and
// each result still names its call.
func TestAMoveRenewsServerAndMCPToolIDs(t *testing.T) {
	dir := t.TempDir()
	asst := func(sid, uuid, parent, req string, block string) string {
		return `{"type":"assistant","uuid":"` + uuid + `","parentUuid":"` + parent + `","sessionId":"` + sid + `","requestId":"` + req +
			`","timestamp":"2026-01-01T12:00:03Z","message":{"id":"msg_` + req + `","role":"assistant","content":[` + block + `]}}`
	}
	user := func(sid, uuid, parent, text string) string {
		p := `"` + parent + `"`
		if parent == "" {
			p = "null"
		}
		return `{"type":"user","uuid":"` + uuid + `","parentUuid":` + p + `,"sessionId":"` + sid +
			`","timestamp":"2026-01-01T12:00:01Z","message":{"role":"user","content":[{"type":"text","text":"` + text + `"}]}}`
	}
	shared := func(sid string) []string {
		return []string{
			user(sid, "u1", "", "one"),
			asst(sid, "a1", "u1", "r1", `{"type":"text","text":"reply one"}`),
			user(sid, "u2", "a1", "search"),
			asst(sid, "s1", "u2", "r2", `{"type":"server_tool_use","id":"srvtoolu_01","name":"web_search","input":{"query":"q"}}`),
			asst(sid, "s2", "s1", "r2", `{"type":"web_search_tool_result","tool_use_id":"srvtoolu_01","content":[]}`),
			asst(sid, "s3", "s2", "r2", `{"type":"mcp_tool_use","id":"mcptoolu_01","name":"fetch","server_name":"docs","input":{}}`),
			asst(sid, "s4", "s3", "r2", `{"type":"mcp_tool_result","tool_use_id":"mcptoolu_01","content":[]}`),
			asst(sid, "s5", "s4", "r2", `{"type":"text","text":"found it"}`),
		}
	}
	srcPath, dst := filepath.Join(dir, "S.jsonl"), filepath.Join(dir, "T.jsonl")
	tLines := append(shared("T"), user("T", "b1", "s5", "own"), asst("T", "ba1", "b1", "rb", `{"type":"text","text":"own reply"}`))
	for p, ls := range map[string][]string{srcPath: shared("S"), dst: tLines} {
		if err := os.WriteFile(p, []byte(strings.Join(ls, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	res, _, _ := spliced(t, dst, adapter.Edit{From: "u2", After: "ba1", Carry: &adapter.Session{ID: "S", Path: srcPath}})
	es, _, _ := ParseFile(splicedPath(t, res.SessionID))
	calls, answers := map[string]int{}, map[string]int{}
	for _, e := range es {
		msg, _ := e.Raw["message"].(map[string]any)
		blocks, _ := msg["content"].([]any)
		for _, b := range blocks {
			bm, _ := b.(map[string]any)
			typ, _ := bm["type"].(string)
			switch {
			case strings.HasSuffix(typ, "tool_use"):
				calls[bm["id"].(string)]++
			case strings.HasSuffix(typ, "tool_result"):
				answers[bm["tool_use_id"].(string)]++
			}
		}
	}
	if len(calls) != 4 {
		t.Fatalf("tool call ids %v: want T's two and two renewed ones", calls)
	}
	for id, n := range calls {
		if n != 1 || answers[id] != 1 {
			t.Errorf("tool call %s appears %d times, answered %d times", id, n, answers[id])
		}
	}
	for _, id := range []string{"srvtoolu_01", "mcptoolu_01"} {
		if calls[id] != 1 {
			t.Errorf("T's own %s is not kept once", id)
		}
	}
	for id := range calls {
		if !strings.HasPrefix(id, "srvtoolu_") && !strings.HasPrefix(id, "mcptoolu_") {
			t.Errorf("renewed id %s lost its prefix", id)
		}
	}
}

// A turn before a native /compact is not on the session's current line, so
// an edit refuses it, but a branch only reads the file: WidenBranch widens it
// on the line it is on, the pre-compact one (§2.5b).
func TestWidenBranchReachesATurnBeforeANativeCompact(t *testing.T) {
	for _, path := range []string{compacted, "testdata/compacted-preorigin.jsonl"} {
		if _, err := Widen(path, "u1", "u1"); !errors.Is(err, ErrNotOnLine) {
			t.Fatalf("%s: Widen err %v, want ErrNotOnLine", path, err)
		}
		for node, want := range map[string]adapter.Span{
			"u1": {First: 1, Last: 1, End: "a1", EndNode: "a1", Turns: 2},
			"a2": {First: 2, Last: 2, End: "a2", EndNode: "a2", Turns: 2},
			"u3": {First: 1, Last: 1, End: "a3", EndNode: "a3", Turns: 2},
		} {
			got, err := WidenBranch(path, node)
			// RangeBytes/LineBytes are not this test's concern; zero them
			// before comparing the rest of the span.
			got.RangeBytes, got.LineBytes = 0, 0
			if err != nil || got != want {
				t.Errorf("%s: WidenBranch(%s) = %+v, %v; want %+v", path, node, got, err, want)
			}
		}
	}
}

// The same holds on a stretch the session rewound away from: x1 in
// splice.jsonl hangs off a1, and its line ends at xa1.
func TestWidenBranchReachesARewoundStretch(t *testing.T) {
	sp, err := WidenBranch("testdata/splice.jsonl", "x1")
	if err != nil || sp.End != "xa1" {
		t.Fatalf("WidenBranch(x1) = %+v, %v; want it to end at xa1", sp, err)
	}
}
