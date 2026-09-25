package tui

// Move (§2.8), end to end on real files: see scenario_test.go for the world.

import (
	"os"
	"strings"
	"testing"

	"herdr-tree/internal/claude"
	"herdr-tree/internal/store"
)

// prompts is sid's prompt texts in line order, as the agent would read them.
func (w *world) prompts(sid string) []string {
	w.t.Helper()
	es, _, err := claude.ParseFile(w.path(sid))
	if err != nil {
		w.t.Fatal(err)
	}
	by := map[string]claude.Entry{}
	tip := ""
	for _, e := range es {
		if u := e.UUID(); u != "" {
			by[u] = e
			if e.Type() == "user" || e.Type() == "assistant" {
				tip = u
			}
		}
	}
	var out []string
	for u := tip; u != ""; u = by[u].ParentUUID() {
		if claude.IsPrompt(by[u]) {
			out = append([]string{by[u].Text()}, out...)
		}
	}
	return out
}

// snapshot is every transcript and the store, to prove nothing was written.
func (w *world) snapshot() string {
	w.t.Helper()
	var b strings.Builder
	for _, sid := range []string{sidT, sidU} {
		if m, _ := os.ReadFile(w.path(sid)); m != nil {
			b.Write(m)
		}
	}
	st, _ := os.ReadFile(w.storePath())
	b.Write(st)
	return b.String()
}

// view is what the overlay draws, one string per line.
func view(u uiModel) []string {
	u.height = 60
	return strings.Split(u.View(), "\n")
}

// indexOf is the first line of lines containing s, -1 if none.
func indexOf(lines []string, s string) int {
	for i, l := range lines {
		if strings.Contains(l, s) {
			return i
		}
	}
	return -1
}

func moveWorld(t *testing.T) *world {
	w := newWorld(t)
	w.durations = true
	w.trunk(sidT, "t1", "t2", "t3", "t4")
	w.trunk(sidU, "u1", "u2")
	return w
}

func TestPickingUpWithMAndWithTheRangeMenu(t *testing.T) {
	w := moveWorld(t)
	before := w.snapshot()

	u := drive(t, cursorTo(t, w.open(sidT), sidT, "t2-p"), key('m'))
	if u.moving == nil || u.status != "moving 1 turns — ⏎ puts them here · esc puts them back" {
		t.Fatalf("m: moving %v, status %q", u.moving != nil, u.status)
	}

	u = selectRange(t, w.open(sidT), sidT, "t2-r", "t3-c", 2)
	if u.moving == nil || u.status != "moving 2 turns — ⏎ puts them here · esc puts them back" || u.m.RangeEnd != nil {
		t.Fatalf("range menu move: moving %v, status %q", u.moving != nil, u.status)
	}
	if w.snapshot() != before || w.summaries() != 0 {
		t.Fatal("picking up wrote or paid for something")
	}
	u.m.Cursor = 0 // m here would pick up t1
	for _, k := range "spbm" {
		u = drive(t, u, key(k))
		if u.moving == nil || u.moving.turns != 2 || u.m.RangeEnd != nil || u.picking != nil || u.busy != "" {
			t.Fatalf("%c acted while moving: %q", k, u.status)
		}
	}
	if w.snapshot() != before {
		t.Fatal("a swallowed key wrote something")
	}
}

// The ⇢ block follows the cursor, drawn after its turn; the origin shows a
// placeholder; the tree itself is untouched.
func TestTheMovePreviewFollowsTheCursor(t *testing.T) {
	w := moveWorld(t)
	u := w.open(sidT)
	rows := len(u.m.Rows())
	u = drive(t, at(t, u, "t2-p"), key('m')) // folded: one row a turn

	check := func(after string) {
		t.Helper()
		v := view(u)
		at, block, origin := indexOf(v, "user: prompt "+after), indexOf(v, "⇢ prompt t2"), indexOf(v, "⋯ 1 turns moving")
		if at < 0 || block != at+1 || origin < 0 || indexOf(v, "user: prompt t2") >= 0 {
			t.Fatalf("after %s: turn at %d, block at %d, origin at %d:\n%s", after, at, block, origin, strings.Join(v, "\n"))
		}
		if strings.Count(strings.Join(v, "\n"), "⇢ prompt t2") != 1 {
			t.Fatal("the block is drawn twice")
		}
	}
	u = drive(t, u, down) // skips the turns in hand, onto t3
	if n := u.m.Selected(); n.Node.ID != "t3-p" {
		t.Fatalf("down landed on %s, want t3-p past the turn in hand", n.Node.ID)
	}
	check("t3")
	u = drive(t, u, down)
	check("t4")
	u = drive(t, u, key('k'), key('k'))
	check("t1")
	if len(u.m.Rows()) != rows {
		t.Fatalf("%d rows while moving, %d before: the preview changed the tree", len(u.m.Rows()), rows)
	}
}

func TestEscPutsTheTurnsBack(t *testing.T) {
	w := moveWorld(t)
	before := w.snapshot()
	u := drive(t, cursorTo(t, w.open(sidT), sidT, "t2-p"), key('m'), down, down, esc)
	if u.moving != nil || u.status != "move cancelled — nothing was written" || u.quitting {
		t.Fatalf("esc: moving %v, status %q, quitting %v", u.moving != nil, u.status, u.quitting)
	}
	if indexOf(view(u), "⇢") >= 0 || indexOf(view(u), "⋯") >= 0 {
		t.Fatalf("the preview outlived esc:\n%s", strings.Join(view(u), "\n"))
	}
	if w.snapshot() != before || len(w.h.calls) != 0 {
		t.Fatalf("esc wrote or asked herdr something: %v", w.h.calls)
	}
}

// Within the line: one splice, turns in their new order, both markers, no
// pane touched, and the old line hidden.
func TestAMoveWithinTheLine(t *testing.T) {
	w := moveWorld(t)
	u := drive(t, cursorTo(t, w.open(sidT), sidT, "t2-p"), key('m'))
	u = drive(t, cursorTo(t, u, sidT, "t4-r"), enter)
	r := w.replacement(sidT)
	if got := strings.Join(w.prompts(r), ","); got != "prompt t1,prompt t3,prompt t4,prompt t2" {
		t.Fatalf("new line reads %s", got)
	}
	if u.moving != nil || !strings.HasPrefix(u.status, "moved 1 turns within "+shortID(sidT)+" → "+shortID(r)) {
		t.Fatalf("moving %v, status %q", u.moving != nil, u.status)
	}
	for _, c := range w.h.calls {
		if !strings.HasPrefix(c, "live") {
			t.Fatalf("a move touched a pane: %v", w.h.calls)
		}
	}
	u = allOf(u)
	checkLines(t, u, r, sidU)
	if got := rowText(u, r, "t3-p"); !strings.Contains(got, "⇢ 1 turns moved to "+shortID(r)) {
		t.Errorf("where t2 was: %q", got)
	}
	if moved := screen(u)[indexOf(screen(u), "user: prompt t2")]; !strings.Contains(moved, "⇠ moved from "+shortID(r)) {
		t.Errorf("the moved turn: %q", moved)
	}
	checkNoCopies(t, u)
}

// Into another line: the target is written first, then the source drops the
// turns, each asked first; the cursor lands on the target.
func TestAMoveIntoAnotherLine(t *testing.T) {
	w := moveWorld(t)
	u := drive(t, cursorTo(t, allOf(w.open(sidT)), sidT, "t2-p"), key('m'))
	u = drive(t, cursorTo(t, u, sidU, "u1-r"), enter)
	t1, u1 := w.replacement(sidT), w.replacement(sidU)
	if got := strings.Join(w.prompts(u1), ","); got != "prompt u1,prompt t2,prompt u2" {
		t.Errorf("target reads %s", got)
	}
	if got := strings.Join(w.prompts(t1), ","); got != "prompt t1,prompt t3,prompt t4" {
		t.Errorf("source reads %s", got)
	}
	want := "moved into " + shortID(sidU) + " → " + shortID(u1) + ", dropped 1 turns from " + shortID(sidT) + " → " + shortID(t1)
	if u.status != want {
		t.Errorf("status %q, want %q", u.status, want)
	}
	if got := strings.Join(w.h.calls, ", "); got != "live "+sidT+", live "+sidU+", live "+sidT {
		t.Errorf("herdr saw %s, want the source, the target, then the source again", got)
	}
	if n := u.m.Selected(); n == nil || n.SessionID != u1 {
		t.Errorf("cursor not on the target")
	}
	u = allOf(u)
	checkLines(t, u, t1, u1)
	if got := rowText(u, t1, "t3-p"); !strings.Contains(got, "⇢ 1 turns moved to "+shortID(u1)) {
		t.Errorf("where t2 was: %q", got)
	}
	if moved := screen(u)[indexOf(screen(u), "user: prompt t2")]; !strings.Contains(moved, "⇠ moved from "+shortID(t1)) {
		t.Errorf("the moved turn: %q", moved)
	}
	st, _ := store.Load(w.repo)
	if st.Branches[u1].Kind != store.KindMoved || st.Branches[t1].Cut == nil || st.Branches[t1].Cut.To != u1 {
		t.Errorf("records: target %+v, source %+v", st.Branches[u1], st.Branches[t1])
	}
}

// The source's agent starts a turn between the insert and the drop: the
// turns stay in both lines, and the status says so.
func TestAFailedDropAfterAGoodInsertKeepsTheCopy(t *testing.T) {
	w := moveWorld(t)
	u := drive(t, cursorTo(t, allOf(w.open(sidT)), sidT, "t2-p"), key('m'))
	asked := 0
	u.live = func(sid string) (string, string, error) {
		w.h.calls = append(w.h.calls, "live "+sid)
		if sid == sidT {
			if asked++; asked > 1 {
				return "pane-T", "working", nil
			}
		}
		return "", "", nil
	}
	u = drive(t, cursorTo(t, u, sidU, "u1-r"), enter)
	u1 := w.replacement(sidU)
	if st, _ := store.Load(w.repo); st.Resolve(sidT) != sidT {
		t.Fatal("the source was dropped by its busy agent")
	}
	want := "moved into " + shortID(sidU) + ", but the source was not dropped: agent is working — wait for it to finish; nothing was written"
	if u.status != want {
		t.Fatalf("status %q, want %q", u.status, want)
	}
	if got := strings.Join(w.prompts(u1), ","); got != "prompt u1,prompt t2,prompt u2" {
		t.Errorf("target reads %s", got)
	}
	if got := strings.Join(w.prompts(sidT), ","); got != "prompt t1,prompt t2,prompt t3,prompt t4" {
		t.Errorf("source reads %s", got)
	}
	w.checkTranscripts(3)
}

// Busy or replaced elsewhere, on either line: nothing is written and the
// turns stay in hand.
func TestAMoveIsRefusedOnABusyOrChangedLine(t *testing.T) {
	for name, c := range map[string]struct {
		busy, changed string
		want          string
	}{
		"busy source":    {busy: sidT, want: "the source's agent is working — wait for it to finish; nothing was written"},
		"busy target":    {busy: sidU, want: "agent is working — wait for it to finish; nothing was written"},
		"changed source": {changed: sidT, want: staleLine},
		"changed target": {changed: sidU, want: staleLine},
	} {
		t.Run(name, func(t *testing.T) {
			w := moveWorld(t)
			u := drive(t, cursorTo(t, allOf(w.open(sidT)), sidT, "t3-p"), key('m'))
			if c.busy != "" {
				w.h.panes[c.busy] = [2]string{"pane", "working"}
			}
			if c.changed != "" {
				first := map[string]string{sidT: "t1", sidU: "u1"}[c.changed]
				drive(t, selectRange(t, w.open(c.changed), c.changed, first+"-p", first+"-r", 3), enter)
			}
			before := w.snapshot()
			u = drive(t, cursorTo(t, u, sidU, "u1-r"), enter)
			if u.status != c.want || u.moving == nil {
				t.Fatalf("status %q (want %q), still moving %v", u.status, c.want, u.moving != nil)
			}
			if w.snapshot() != before {
				t.Fatal("a refused move wrote something")
			}
		})
	}
}

// Onto itself, onto the turn right before it, or the whole of its line
// elsewhere: refused, nothing written, still moving.
func TestAMoveThatChangesNothingIsRefused(t *testing.T) {
	for name, c := range map[string]struct {
		pick, sid, at, want string
	}{
		"onto itself":           {"t2-p", sidT, "t2-r", "move failed: that puts the turns back where they are"},
		"after the turn before": {"t2-p", sidT, "t1-r", "move failed: that puts the turns back where they are"},
		"a whole line":          {"", sidU, "u1-r", "move failed: a cut must leave at least one turn"},
	} {
		t.Run(name, func(t *testing.T) {
			w := moveWorld(t)
			u := allOf(w.open(sidT))
			if c.pick != "" {
				u = drive(t, cursorTo(t, u, sidT, c.pick), key('m'))
			} else {
				u = selectRange(t, u, sidT, "t1-p", "t4-r", 2)
			}
			before := w.snapshot()
			u = cursorTo(t, u, c.sid, c.at)
			u = drive(t, u, enter)
			if u.status != c.want || u.moving == nil {
				t.Fatalf("status %q (want %q), still moving %v", u.status, c.want, u.moving != nil)
			}
			if w.snapshot() != before {
				t.Fatal("a refused move wrote something")
			}
		})
	}
}

// squash into…'s target mode already has one thing in hand.
func TestMIsSwallowedInTargetMode(t *testing.T) {
	u, _ := press(t, moveUI(t, &fakeAdapter{}, &herdrLog{}), key('m'))
	if u.moving != nil || u.folding == nil {
		t.Fatalf("m in target mode: moving %v, folding %v", u.moving != nil, u.folding != nil)
	}
}
