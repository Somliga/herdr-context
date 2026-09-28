package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"herdr-tree/internal/adapter"
)

// run is what cmd sends back. A confirmed squash batches its work with the
// summarising view's tick, work first; only the work is run.
func run(cmd tea.Cmd) tea.Msg {
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		return b[0]()
	}
	return msg
}

// review runs a confirmed squash's summary and hands it back to Update,
// which must open the review.
func review(t *testing.T, u uiModel, cmd tea.Cmd) uiModel {
	t.Helper()
	next, _ := u.Update(run(cmd))
	u = next.(uiModel)
	if u.squash == nil || u.squash.sum == nil {
		t.Fatalf("the summary did not open the review; status %q", u.status)
	}
	return u
}

// commit is review, then ⏎ on it: the rest of the squash, run.
func commit(t *testing.T, u uiModel, cmd tea.Cmd) (uiModel, actionDoneMsg) {
	t.Helper()
	u, cmd = press(t, review(t, u, cmd), enter)
	return u, cmd().(actionDoneMsg)
}

// squashed is rangeUI's t2..t3 squash, confirmed: the summary is on its way.
func squashed(t *testing.T, fa *fakeAdapter, h *herdrLog) (uiModel, tea.Cmd) {
	t.Helper()
	fa.span = adapter.Span{First: 2, Last: 3}
	u, _ := press(t, rangeUI(t, fa, h), enter, enter)
	return press(t, u, enter)
}

func TestASquashShowsTheSummaryBeforeItLands(t *testing.T) {
	fa := &fakeAdapter{summary: "state: the tests pass\nnext: ship it"}
	u, cmd := squashed(t, fa, &herdrLog{})
	u = review(t, u, cmd)
	v := u.View()
	for _, want := range []string{"Squash turns 2–3 — review the summary", "state: the tests pass", "next: ship it",
		"Then: turns 2–3 are replaced by the summary.\n" + replacesLine} {
		if !strings.Contains(v, want) {
			t.Fatalf("the review lacks %q:\n%s", want, v)
		}
	}
	if len(fa.spliced) != 0 || len(u.st.AllSummaries()) != 1 {
		t.Fatalf("spliced %+v before ⏎; %d summaries stored", fa.spliced, len(u.st.AllSummaries()))
	}
	// Keys other than the review's own do nothing.
	u, cmd = press(t, u, key('s'), key('q'), key('p'))
	if cmd != nil || u.squash == nil || u.quitting || len(fa.spliced) != 0 {
		t.Fatal("a stray key left the review or wrote")
	}
}

func TestEnterInTheReviewSplicesThatSummaryAndReloads(t *testing.T) {
	fa := &fakeAdapter{summary: "it went well", sessions: []adapter.Session{sessionOf("spliced-sid", "t1", "x")}}
	u, cmd := squashed(t, fa, &herdrLog{})
	u, msg := commit(t, u, cmd)
	if len(fa.spliced) != 1 || !strings.Contains(fa.spliced[0].Seed, "it went well") {
		t.Fatalf("spliced %+v", fa.spliced)
	}
	if !msg.reload || msg.tip != "spliced-sid" {
		t.Fatalf("%+v", msg)
	}
	next, _ := u.Update(msg)
	if u = next.(uiModel); u.busy != "" || u.squash != nil || rowOf(u, "spliced-sid", "x") < 0 {
		t.Fatalf("not reloaded onto the new line: busy %q status %q", u.busy, u.status)
	}
}

func TestEscInTheReviewWritesNothingAndKeepsTheSummary(t *testing.T) {
	fa := &fakeAdapter{summary: "it went well"}
	u, cmd := squashed(t, fa, &herdrLog{})
	u, cmd = press(t, review(t, u, cmd), esc)
	if cmd != nil {
		cmd()
	}
	if got := strings.Join(fa.writes, ","); got != "summarise s" || len(fa.spliced) != 0 {
		t.Fatalf("writes %s after esc, want only the summary", got)
	}
	if len(u.st.AllSummaries()) != 1 || u.squash != nil || u.quitting {
		t.Fatalf("esc must leave the review with the summary stored: status %q", u.status)
	}
	if u.status != "cancelled — nothing was written; the summary is stored for p" {
		t.Fatalf("status %q", u.status)
	}
}

// Each landing of squash into…, up to its confirmation.
var landings = []struct {
	name string
	to   func(*testing.T, uiModel, *fakeAdapter) uiModel
	then string
	want string
}{
	{"merge", func(t *testing.T, u uiModel, _ *fakeAdapter) uiModel {
		u, _ = press(t, at(t, u, "o1"), enter, enter)
		return u
	}, "Then: the summary is merged into o · turns 2–3 are dropped from s", "summarise s,splice o,splice s"},
	{"branch", func(t *testing.T, u uiModel, _ *fakeAdapter) uiModel {
		u, _ = press(t, at(t, u, "o1"), enter, down, enter)
		return u
	}, "Then: a new line branches at o, carrying the summary · turns 2–3 are dropped from s", "summarise s,graft o,splice s"},
	{"live tip", func(t *testing.T, u uiModel, fa *fakeAdapter) uiModel {
		u.current, u.liveAgent = "o", "agent-1"
		u.send = func(agent, _ string) error {
			fa.writes = append(fa.writes, "send "+agent)
			return nil
		}
		u, _ = press(t, at(t, u, "o2"), enter)
		return u
	}, "Then: the summary is sent to agent-1 as your next message · turns 2–3 are dropped from s", "summarise s,send agent-1,splice s"},
}

func TestSquashIntoIsReviewedBeforeItLandsAndDrops(t *testing.T) {
	for _, l := range landings {
		fa := &fakeAdapter{}
		u := l.to(t, moveUI(t, fa, &herdrLog{}), fa)
		u, cmd := press(t, u, enter)
		u = review(t, u, cmd)
		if v := u.View(); !strings.Contains(v, "it went well") || !strings.Contains(v, l.then) {
			t.Fatalf("%s: the review lacks the summary or %q:\n%s", l.name, l.then, v)
		}
		if got := strings.Join(fa.writes, ","); got != "summarise s" {
			t.Fatalf("%s: writes %s before ⏎", l.name, got)
		}
		u, cmd = press(t, u, enter)
		cmd()
		if got := strings.Join(fa.writes, ","); got != l.want {
			t.Fatalf("%s: writes %s, want %s", l.name, got, l.want)
		}

		fa = &fakeAdapter{}
		u = l.to(t, moveUI(t, fa, &herdrLog{}), fa)
		u, cmd = press(t, u, enter)
		u, cmd = press(t, review(t, u, cmd), esc)
		if cmd != nil {
			cmd()
		}
		if got := strings.Join(fa.writes, ","); got != "summarise s" || len(u.st.AllSummaries()) != 1 || u.folding != nil {
			t.Fatalf("%s: esc wrote %s or lost the summary", l.name, got)
		}
	}
}

func TestTheSummarisingViewTicksUntilTheSummaryArrives(t *testing.T) {
	fa := &fakeAdapter{summary: "it went well"}
	u, cmd := squashed(t, fa, &herdrLog{})
	if _, ok := cmd().(tea.BatchMsg); !ok {
		t.Fatal("the summary does not start the clock")
	}
	v := u.View()
	for _, want := range []string{"Summarising turns 2–3 of s", "the model is reading 1 turns · 2 entries · 3 B",
		"⠋ 0s", "ctrl+c leaves (the call is already billed)"} {
		if !strings.Contains(v, want) {
			t.Fatalf("the summarising view lacks %q:\n%s", want, v)
		}
	}
	next, tc := u.Update(tickMsg(u.squash.since.Add(3 * time.Second)))
	u = next.(uiModel)
	if v := u.View(); !strings.Contains(v, "⠸ 3s") || tc == nil {
		t.Fatalf("a tick did not advance the clock or ask for the next one:\n%s", v)
	}
	// The first ctrl+c warns and stays.
	u, _ = press(t, u, tea.KeyMsg{Type: tea.KeyCtrlC})
	if u.quitting || !strings.Contains(u.View(), "ctrl+c again to leave it running") {
		t.Fatalf("the first ctrl+c:\n%s", u.View())
	}
	next, _ = u.Update(run(cmd))
	u = next.(uiModel)
	if _, tc = u.Update(tickMsg(u.squash.since.Add(4 * time.Second))); tc != nil {
		t.Fatal("the clock ticks on after the summary")
	}
}

func TestASourceBusyByCommitIsRefusedWithTheSummaryStored(t *testing.T) {
	fa := &fakeAdapter{summary: "it went well"}
	h := &herdrLog{status: []string{"idle"}}
	u, cmd := squashed(t, fa, h)
	u = review(t, u, cmd)
	h.status = []string{"working"}
	u, cmd = press(t, u, enter)
	msg := cmd().(actionDoneMsg)
	if msg.status != "summary stored — agent is working; select again or use p" || msg.reload {
		t.Fatalf("%+v", msg)
	}
	if got := strings.Join(fa.writes, ","); got != "summarise s" || len(u.st.AllSummaries()) != 1 {
		t.Fatalf("writes %s", got)
	}
}

func TestALongSummaryScrolls(t *testing.T) {
	var lines []string
	for i := 1; i <= 40; i++ {
		lines = append(lines, fmt.Sprintf("line %02d of the summary", i))
	}
	fa := &fakeAdapter{summary: strings.Join(lines, "\n")}
	u, cmd := squashed(t, fa, &herdrLog{})
	u.width, u.height = 60, 20
	u = review(t, u, cmd)
	if v := u.View(); !strings.Contains(v, "line 01") || strings.Contains(v, "line 40") {
		t.Fatalf("the box does not start at the top:\n%s", v)
	}
	u, _ = press(t, u, down)
	if v := u.View(); strings.Contains(v, "line 01") || !strings.Contains(v, "line 02") {
		t.Fatalf("↓ did not move the window:\n%s", v)
	}
	for i := 0; i < 50; i++ {
		u, _ = press(t, u, down)
	}
	if v := u.View(); !strings.Contains(v, "line 40") {
		t.Fatalf("the last line is out of reach:\n%s", v)
	}
	u, _ = press(t, u, tea.KeyMsg{Type: tea.KeyPgUp}, tea.KeyMsg{Type: tea.KeyPgUp}, tea.KeyMsg{Type: tea.KeyPgUp})
	if v := u.View(); !strings.Contains(v, "line 01") {
		t.Fatalf("pgup did not reach the top:\n%s", v)
	}
}

// The summary is shown in the review and nowhere else: not in any status
// line on the way there, on esc, or on a commit that fails or lands.
func TestNoStatusLineCarriesTheReviewedSummary(t *testing.T) {
	check := func(u uiModel, when string) {
		t.Helper()
		if strings.Contains(u.status, sentinel) {
			t.Fatalf("%s: status %q", when, u.status)
		}
	}
	for _, spliceErr := range []error{nil, errors.New("write failed: " + sentinel)} {
		fa := &fakeAdapter{summary: sentinel, spliceErr: spliceErr}
		u, cmd := squashed(t, fa, &herdrLog{})
		check(u, "summarising")
		u = review(t, u, cmd)
		check(u, "review")
		if !strings.Contains(u.View(), sentinel) {
			t.Fatal("the review does not show the summary")
		}
		u, cmd = press(t, u, enter)
		next, _ := u.Update(cmd())
		check(next.(uiModel), "commit")
	}
	u, cmd := squashed(t, &fakeAdapter{summary: sentinel}, &herdrLog{})
	u, _ = press(t, review(t, u, cmd), esc)
	check(u, "esc")
}
