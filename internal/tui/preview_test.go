package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Four turns sized 5005, 35000, 10000, 40000 (ends 5005, 40005, 50005, 90005).
func heavyWorld(t *testing.T) *world {
	w := newWorld(t)
	w.durations = true
	w.trunk(sidT, "t1", "t2", "t3", "t4")
	w.withUsage(sidT, map[string]int{"t1-r": 5000, "t2-r": 40000, "t3-r": 50000, "t4-r": 90000})
	return w
}

func TestHeavyJumpStepsThroughTheLargestTurnsAndWraps(t *testing.T) {
	u := heavyWorld(t).open(sidT)
	for _, step := range []struct {
		key    rune
		id     string
		status string
	}{
		{']', "t4-p", "heavy 1/4 · 40k"},
		{']', "t2-p", "heavy 2/4 · 35k"},
		{'[', "t4-p", "heavy 1/4 · 40k"},
		{'[', "t1-p", "heavy 4/4 · 5k"},  // wraps
		{']', "t4-p", "heavy 1/4 · 40k"}, // and back round
	} {
		u = drive(t, u, key(step.key))
		if n := u.m.Selected(); n == nil || n.Node.ID != step.id || u.status != step.status {
			got := ""
			if n != nil {
				got = n.Node.ID
			}
			t.Fatalf("after %c: cursor %q status %q, want %q %q", step.key, got, u.status, step.id, step.status)
		}
	}
}

func TestHeavyJumpIsSwallowedMidRange(t *testing.T) {
	u := cursorTo(t, heavyWorld(t).open(sidT), sidT, "t2-p")
	u = drive(t, u, key('s'), key(']'))
	if n := u.m.Selected(); n == nil || n.Node.ID != "t2-p" {
		t.Fatal("] moved the cursor mid-range")
	}
}

func TestTheSidebarPreviewsTheRange(t *testing.T) {
	u := drive(t, heavyWorld(t).open(sidT), tea.WindowSizeMsg{Width: 110, Height: 40})
	u = cursorTo(t, u, sidT, "t3-p")
	u = drive(t, u, key('s'))
	u = cursorTo(t, u, sidT, "t2-p")
	v := u.View()
	for _, want := range []string{"range  2 turns · 45k", "50% of 90k", "drop   → 45k", "squash → ~46k"} {
		if !strings.Contains(v, want) {
			t.Fatalf("no %q in the sidebar:\n%s", want, v)
		}
	}
	if strings.Contains(v, "thinking") {
		t.Fatal("the session breakdown shows mid-range")
	}
	u = drive(t, u, esc)
	if v := u.View(); strings.Contains(v, "range  ") || !strings.Contains(v, "context ") {
		t.Fatalf("the session view is not back after esc:\n%s", v)
	}
}

func TestANarrowPaneShowsTheRangeOnTheStatusLine(t *testing.T) {
	u := drive(t, heavyWorld(t).open(sidT), tea.WindowSizeMsg{Width: 100, Height: 40})
	u = cursorTo(t, u, sidT, "t3-p")
	u = drive(t, u, key('s'))
	u = cursorTo(t, u, sidT, "t2-p")
	if want := "range 2 turns · 45k (50%) · drop → 45k · squash → ~46k"; !strings.Contains(u.View(), want) {
		t.Fatalf("no %q:\n%s", want, u.View())
	}
}

// Turns in the compacted stretch are out of the context: ] skips them.
func TestHeavyJumpSkipsTheCompactedStretch(t *testing.T) {
	w := newWorld(t)
	w.fixture(sidT, "compacted.jsonl")
	u := w.open(sidT)
	for i := 0; i < 3; i++ {
		u = drive(t, u, key(']'))
		if n := u.m.Selected(); n == nil || n.Compacted || !strings.HasPrefix(u.status, "heavy ") || !strings.Contains(u.status, "/2 ") {
			t.Fatalf("jump %d: on %v, status %q — want only u3 and u4", i+1, n, u.status)
		}
	}
}

// ] mid-move would drop the cursor anywhere, even into the section in hand.
func TestHeavyJumpIsSwallowedMidMove(t *testing.T) {
	u := cursorTo(t, heavyWorld(t).open(sidT), sidT, "t1-p")
	u = drive(t, u, key('m'))
	was := u.m.Cursor
	u = drive(t, u, key(']'))
	if u.m.Cursor != was || strings.HasPrefix(u.status, "heavy") {
		t.Fatalf("] moved the cursor mid-move (status %q)", u.status)
	}
}

// A range into the compacted stretch will be refused: the preview says so
// instead of counting the one row that stands for many turns.
func TestTheRangePreviewSaysARangeBeforeACompactCannotBeEdited(t *testing.T) {
	w := newWorld(t)
	w.fixture(sidT, "compacted.jsonl")
	u := drive(t, w.open(sidT), tea.WindowSizeMsg{Width: 110, Height: 40})
	u = drive(t, u, down, key('s'), tea.KeyMsg{Type: tea.KeyUp}) // u3 end, the group row start
	v := u.View()
	if !strings.Contains(v, "before a /compact") || strings.Contains(v, "drop   →") {
		t.Fatalf("the preview counts a range it cannot edit:\n%s", v)
	}
}

// A range must stay in one session; while it strays, no preview adds up two.
func TestTheRangePreviewNeedsOneSession(t *testing.T) {
	w := heavyWorld(t)
	b := w.branch(sidT, sidT, "t2-p")
	u := drive(t, allOf(w.open(sidT)), tea.WindowSizeMsg{Width: 110, Height: 60})
	u = cursorTo(t, u, sidT, "t4-p")
	u = drive(t, u, key('s'))
	u = cursorTo(t, u, b, rowIDOf(t, u, b))
	if v := u.View(); !strings.Contains(v, "one session") || strings.Contains(v, "drop   →") {
		t.Fatalf("a range across two sessions is previewed:\n%s", v)
	}
}

// rowIDOf is the id of sid's first row on screen.
func rowIDOf(t *testing.T, u uiModel, sid string) string {
	t.Helper()
	for _, r := range u.m.Rows() {
		if r.Node.SessionID == sid {
			return r.Node.Node.ID
		}
	}
	t.Fatalf("no row of %s", sid)
	return ""
}
