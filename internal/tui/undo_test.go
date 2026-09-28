package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"herdr-tree/internal/store"
)

func undoWorld(t *testing.T) *world {
	w := newWorld(t)
	w.durations = true
	w.trunk(sidT, "t1", "t2", "t3", "t4")
	return w
}

// storeFile is the store as it is on disk, to prove a refusal wrote nothing.
func (w *world) storeFile() string {
	b, _ := os.ReadFile(w.storePath())
	return string(b)
}

func checkOnTip(t *testing.T, u uiModel, sid string) {
	t.Helper()
	if n := u.m.Selected(); n == nil || n.SessionID != sid || !n.IsSessionLeaf {
		t.Errorf("cursor not on %s's tip", shortID(sid))
	}
}

func shown(u uiModel) string { return strings.Join(screen(u), "\n") }

func TestUndoAndRedoASquash(t *testing.T) {
	w := undoWorld(t)
	u := drive(t, selectRange(t, w.open(sidT), sidT, "t1-p", "t2-r", 0), enter, enter)
	t1 := w.replacement(sidT)

	u = drive(t, u, key('u'))
	if want := "undone squash on " + shortID(sidT) + continueThere; u.status != want {
		t.Fatalf("status %q, want %q", u.status, want)
	}
	checkOnTip(t, u, sidT)
	checkLines(t, u, sidT)
	if rowText(u, sidT, "t1-p") == "" || strings.Contains(shown(u), "⤶ squashed") {
		t.Errorf("the original line is not what is shown:\n%s", shown(u))
	}

	u = drive(t, u, key('U'))
	if want := "redone squash on " + shortID(t1) + continueThere; u.status != want {
		t.Fatalf("status %q, want %q", u.status, want)
	}
	checkOnTip(t, u, t1)
	checkLines(t, u, t1)
	if !strings.Contains(shown(u), "⤶ squashed") {
		t.Errorf("the squashed line is not shown:\n%s", shown(u))
	}
}

func TestUndoWalksBackAndRedoWalksForward(t *testing.T) {
	w := undoWorld(t)
	u := drive(t, selectRange(t, w.open(sidT), sidT, "t2-p", "t2-r", 1), enter)
	v1 := w.replacement(sidT)
	u = drive(t, selectRange(t, u, v1, "t1-p", "t1-r", 0), enter, enter)
	v2 := w.replacement(v1)
	u = drive(t, selectRange(t, u, v2, "t3-p", "t3-r", 1), enter)
	v3 := w.replacement(v2)

	u = drive(t, u, key('u'), key('u'), key('u'))
	checkLines(t, u, sidT)
	for _, id := range []string{"t1-p", "t2-p", "t3-p", "t4-p"} {
		if rowText(u, sidT, id) == "" {
			t.Errorf("%s is not back:\n%s", id, shown(u))
		}
	}
	if s := shown(u); strings.Contains(s, "✂") || strings.Contains(s, "⤶ squashed") {
		t.Errorf("an undone edit's marker shows:\n%s", s)
	}

	u = drive(t, u, key('U'), key('U'))
	checkLines(t, u, v2)
	if rowText(u, v2, "t2-p") != "" || rowText(u, v2, "t3-p") == "" || !strings.Contains(shown(u), "⤶ squashed") {
		t.Errorf("not the second version:\n%s", shown(u))
	}
	if st, _ := store.Load(w.repo); !st.Branches[v3].Undone || st.Current(sidT) != v2 {
		t.Errorf("store: %s undone %v, current %s", shortID(v3), st.Branches[v3].Undone, shortID(st.Current(sidT)))
	}
}

func TestNothingToUndoOrRedo(t *testing.T) {
	w := undoWorld(t)
	u := drive(t, w.open(sidT), key('u'))
	if want := "nothing to undo on " + shortID(sidT); u.status != want {
		t.Errorf("status %q, want %q", u.status, want)
	}
	u = drive(t, u, key('U'))
	if want := "nothing to redo on " + shortID(sidT); u.status != want {
		t.Errorf("status %q, want %q", u.status, want)
	}
}

// A new edit after an undo leaves nothing to redo: the undone version is
// off the path.
func TestANewEditAfterUndoLeavesNothingToRedo(t *testing.T) {
	w := undoWorld(t)
	u := drive(t, selectRange(t, w.open(sidT), sidT, "t1-p", "t1-r", 0), enter, enter)
	u = drive(t, u, key('u'))
	u = drive(t, selectRange(t, u, sidT, "t3-p", "t3-r", 1), enter)
	if !strings.HasPrefix(u.status, "dropped") {
		t.Fatalf("the drop after undo: %q", u.status)
	}
	d := w.replacement(sidT)
	u = drive(t, u, key('U'))
	if want := "nothing to redo on " + shortID(d); u.status != want {
		t.Errorf("status %q, want %q", u.status, want)
	}
	checkLines(t, u, d)
	if rowText(u, d, "t3-p") != "" || strings.Contains(shown(u), "⤶ squashed") {
		t.Errorf("not the drop:\n%s", shown(u))
	}
}

func moveTtoU(t *testing.T, w *world) (uiModel, string, string) {
	t.Helper()
	w.trunk(sidU, "u1", "u2")
	u := drive(t, cursorTo(t, allOf(w.open(sidT)), sidT, "t2-p"), key('m'))
	u = drive(t, cursorTo(t, u, sidU, "u1-r"), enter)
	return u, w.replacement(sidT), w.replacement(sidU)
}

// A cross-line move undoes and redoes as one edit, from either line.
func TestUndoAMoveUndoesBothLines(t *testing.T) {
	w := undoWorld(t)
	u, t1, u1 := moveTtoU(t, w)

	u = drive(t, cursorTo(t, u, u1, "u2-p"), key('u'))
	if want := "undone move on " + shortID(sidU) + continueThere; u.status != want {
		t.Fatalf("status %q, want %q", u.status, want)
	}
	checkLines(t, u, sidT, sidU)
	if rowText(u, sidT, "t2-p") == "" || strings.Count(shown(u), "prompt t2") != 1 {
		t.Errorf("t2 is not back where it was, once:\n%s", shown(u))
	}

	u = drive(t, cursorTo(t, u, sidT, "t1-p"), key('U'))
	if !strings.HasPrefix(u.status, "redone move on ") {
		t.Fatalf("status %q", u.status)
	}
	checkLines(t, u, t1, u1)
	if !strings.Contains(shown(u), "⇠ moved from "+shortID(t1)) || strings.Count(shown(u), "prompt t2") != 1 {
		t.Errorf("t2 is not moved again, once:\n%s", shown(u))
	}
}

// A cross-line move's undo restores both lines whichever line the cursor is
// on: mirrors TestUndoAMoveUndoesBothLines, undoing from the SOURCE line
// (sidT, unmoved) instead of the target.
func TestUndoAMoveFromTheSourceLineUndoesBothLines(t *testing.T) {
	w := undoWorld(t)
	u, t1, u1 := moveTtoU(t, w)

	u = drive(t, cursorTo(t, u, t1, "t1-p"), key('u'))
	if want := "undone move on " + shortID(sidT) + continueThere; u.status != want {
		t.Fatalf("status %q, want %q", u.status, want)
	}
	checkLines(t, u, sidT, sidU)
	if rowText(u, sidT, "t2-p") == "" || strings.Count(shown(u), "prompt t2") != 1 {
		t.Errorf("t2 is not back where it was, once:\n%s", shown(u))
	}

	u = drive(t, cursorTo(t, u, sidU, "u2-p"), key('U'))
	if !strings.HasPrefix(u.status, "redone move on ") {
		t.Fatalf("status %q", u.status)
	}
	checkLines(t, u, t1, u1)
	if !strings.Contains(shown(u), "⇠ moved from "+shortID(t1)) || strings.Count(shown(u), "prompt t2") != 1 {
		t.Errorf("t2 is not moved again, once:\n%s", shown(u))
	}
}

func TestUndoAMoveIsRefusedIfTheOtherLineWasEditedSince(t *testing.T) {
	w := undoWorld(t)
	u, t1, u1 := moveTtoU(t, w)
	u = drive(t, selectRange(t, u, u1, "u2-p", "u2-r", 1), enter)
	u2 := w.replacement(u1)
	before := w.storeFile()

	u = drive(t, cursorTo(t, u, t1, "t1-p"), key('u'))
	if want := shortID(u2) + " was edited since — undo that first"; u.status != want {
		t.Fatalf("status %q, want %q", u.status, want)
	}
	if w.storeFile() != before {
		t.Error("a refused undo wrote the store")
	}
	checkLines(t, u, t1, u2)
}

// Any version of the line open in a busy pane refuses the undo.
func TestUndoIsRefusedWhileAnyVersionIsBusy(t *testing.T) {
	w := undoWorld(t)
	u := drive(t, selectRange(t, w.open(sidT), sidT, "t1-p", "t1-r", 0), enter, enter)
	t1 := w.replacement(sidT)
	w.h.panes[sidT] = [2]string{"pane-T", "working"}
	before := w.storeFile()

	u = drive(t, u, key('u'))
	if want := "agent is working — wait for it to finish"; u.status != want {
		t.Fatalf("status %q, want %q", u.status, want)
	}
	if w.storeFile() != before {
		t.Error("a refused undo wrote the store")
	}
	checkLines(t, u, t1)
}

func TestUndoIsRefusedIfAnotherOverlayChangedTheLine(t *testing.T) {
	w := undoWorld(t)
	u := drive(t, selectRange(t, w.open(sidT), sidT, "t1-p", "t1-r", 0), enter, enter)
	t1 := w.replacement(sidT)

	other, err := store.Load(w.repo)
	if err != nil {
		t.Fatal(err)
	}
	b := other.Branches[t1]
	b.Undone, b.UndoneAt = true, time.Now().UTC()
	other.Branches[t1] = b
	if err := other.Save(); err != nil {
		t.Fatal(err)
	}
	before := w.storeFile()

	u = drive(t, u, key('u'))
	if u.status != staleLine {
		t.Fatalf("status %q, want %q", u.status, staleLine)
	}
	if w.storeFile() != before {
		t.Error("a refused undo wrote the store")
	}
}

// ⏎ on the restored tip hands over from a pane on the undone version.
func TestEnterAfterUndoHandsOverFromTheUndoneVersion(t *testing.T) {
	w := undoWorld(t)
	u := drive(t, selectRange(t, w.open(sidT), sidT, "t1-p", "t1-r", 0), enter, enter)
	t1 := w.replacement(sidT)
	w.h.panes[t1] = [2]string{"pane-T1", "idle"}

	u = drive(t, u, key('u'))
	checkOnTip(t, u, sidT)
	w.h.calls = nil
	u = drive(t, u, enter)
	if !strings.Contains(u.confirm, "The pane running the old line is closed") {
		t.Fatalf("no handover: confirm %q, status %q, herdr %v", u.confirm, u.status, w.h.calls)
	}
	for _, c := range w.h.calls {
		if !strings.HasPrefix(c, "live") {
			t.Fatalf("something was resumed or closed: %v", w.h.calls)
		}
	}
}

// A move's redo needs both lines where the move left them: T, edited and
// that edit undone, no longer leads to the move, so U on U redoes nothing.
func TestRedoAMoveIsRefusedIfTheOtherLineMovedOn(t *testing.T) {
	w := undoWorld(t)
	u, _, u1 := moveTtoU(t, w)
	u = drive(t, cursorTo(t, u, u1, "u2-p"), key('u'))
	u = drive(t, selectRange(t, u, sidT, "t3-p", "t3-r", 1), enter)
	t2 := w.replacement(sidT)
	u = drive(t, cursorTo(t, u, t2, "t1-p"), key('u'))
	checkLines(t, u, sidT, sidU)
	before := w.storeFile()

	u = drive(t, cursorTo(t, u, sidU, "u1-p"), key('U'))
	if want := "nothing to redo on " + shortID(sidU); u.status != want {
		t.Fatalf("status %q, want %q", u.status, want)
	}
	if w.storeFile() != before {
		t.Error("a refused redo wrote the store")
	}
	checkLines(t, u, sidT, sidU)
	if n := strings.Count(shown(u), "prompt t2"); n != 1 {
		t.Errorf("prompt t2 shows %d times:\n%s", n, shown(u))
	}
}

// A failed save puts the undo back, so a later save cannot write it.
func TestAnUndoThatIsNotSavedIsPutBack(t *testing.T) {
	w := undoWorld(t)
	u := drive(t, selectRange(t, w.open(sidT), sidT, "t1-p", "t1-r", 0), enter, enter)
	t1 := w.replacement(sidT)
	dir := filepath.Dir(w.storePath())
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	u = drive(t, u, key('u'))
	if !strings.HasPrefix(u.status, "not saved: ") {
		t.Fatalf("status %q", u.status)
	}
	if b := u.st.Branches[t1]; b.Undone || !b.UndoneAt.IsZero() {
		t.Fatalf("in memory %s is still undone: %v at %v", shortID(t1), b.Undone, b.UndoneAt)
	}
	os.Chmod(dir, 0o700)
	u = drive(t, cursorTo(t, u, t1, "t3-p"), key('L'))
	u = drive(t, u, append(typed("keep"), enter)...)
	if st, _ := store.Load(w.repo); st.Labels[store.LabelKey(t1, "t3-p")] != "keep" || st.Branches[t1].Undone || st.Current(sidT) != t1 {
		t.Errorf("a later save wrote the undo: current %s", shortID(st.Current(sidT)))
	}
}
