package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"herdr-tree/internal/adapter"
	"herdr-tree/internal/store"
	"herdr-tree/internal/tree"
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

// flat is s with its wrapping undone: every run of spaces and line breaks
// one space.
func flat(s string) string { return strings.Join(strings.Fields(s), " ") }

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
	v := flat(u.View())
	for _, want := range []string{"Squash turns 2–3 — review the summary", "state: the tests pass", "next: ship it",
		flat("Then: turns 2–3 are replaced by the summary.\n" + replacesLine)} {
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

// TestASquashShowsTheTitleAboveTheSummary is §2.10: a titled summary shows
// its title above the box, and the title line does not also appear inside
// the summary body.
func TestASquashShowsTheTitleAboveTheSummary(t *testing.T) {
	fa := &fakeAdapter{summary: "Ship the login screen\n\nstate: the tests pass\nnext: ship it"}
	u, cmd := squashed(t, fa, &herdrLog{})
	u = review(t, u, cmd)
	v := flat(u.View())
	if !strings.Contains(v, "Ship the login screen") {
		t.Fatalf("the review lacks the title:\n%s", v)
	}
	if !strings.Contains(v, "state: the tests pass") {
		t.Fatalf("the review lost the summary body:\n%s", v)
	}
	if strings.Count(v, "Ship the login screen") != 1 {
		t.Fatalf("the title must not also appear in the body: %d occurrences in\n%s",
			strings.Count(v, "Ship the login screen"), v)
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

func TestTheSummarisingViewTicksUntilTheSummaryArrives(t *testing.T) {
	fa := &fakeAdapter{summary: "it went well"}
	u, cmd := squashed(t, fa, &herdrLog{})
	if _, ok := cmd().(tea.BatchMsg); !ok {
		t.Fatal("the summary does not start the clock")
	}
	v := u.View()
	for _, want := range []string{"Summarising turns 2–3 of s", "the model is reading 1 turn · 2 entries · 3 B",
		"⠋ 0s", "ctrl+c leaves (the call is already billed)"} {
		if !strings.Contains(v, want) {
			t.Fatalf("the summarising view lacks %q:\n%s", want, v)
		}
	}
	next, tc := u.Update(tickMsg{u.ticks, u.squash.since.Add(3 * time.Second)})
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
	if _, tc = u.Update(tickMsg{u.ticks, u.squash.since.Add(4 * time.Second)}); tc != nil {
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

// A tick still in flight from one squash must not start a second clock on
// the next.
func TestAStaleTickDoesNotRunASecondClock(t *testing.T) {
	fa := &fakeAdapter{summary: "it went well"}
	u, cmd := squashed(t, fa, &herdrLog{})
	stale := tickMsg{u.ticks, u.squash.since.Add(time.Second)}
	u, _ = press(t, review(t, u, cmd), esc)
	u.m.RangeEnd = u.m.Rows()[2].Node
	u.m.Cursor = 1
	u, _ = press(t, u, enter, enter)
	u, _ = press(t, u, enter)
	if u.squash == nil || u.busy == "" {
		t.Fatalf("setup: the second squash is not summarising: %q", u.status)
	}
	next, tc := u.Update(stale)
	if tc != nil || next.(uiModel).squash.now != u.squash.since {
		t.Fatal("the first squash's tick moved the second one's clock")
	}
	if _, tc = u.Update(tickMsg{u.ticks, u.squash.since.Add(time.Second)}); tc == nil {
		t.Fatal("the second squash's own tick was dropped")
	}
}

// §3.2 of the undo/redo and context meter spec: the review estimates what a
// squash will save, from the source session's context number and the
// range's byte share of the line.
func TestReviewShowsTheContextEstimate(t *testing.T) {
	sess := adapter.Session{ID: "s", Title: "s", CWD: "/repo", Path: "/transcripts/s.jsonl", ContextTokens: 84000}
	for _, id := range []string{"t1", "t2", "t3"} {
		sess.Nodes = append(sess.Nodes, adapter.Node{ID: id, Title: "turn " + id, Kind: adapter.KindHuman})
	}
	roots := tree.Build([]adapter.Session{sess}, &store.Store{Version: 1, Branches: map[string]store.Branch{}})

	fa := &fakeAdapter{summary: strings.Repeat("x", 400)}
	fa.span = adapter.Span{First: 2, Last: 3, RangeBytes: 600, LineBytes: 1000}
	h := &herdrLog{}
	u := uiModel{m: New(roots), a: fa, st: loadedStore(t), repoRoot: "/repo", live: h.live, closePane: h.close}
	u.m.Cursor = 2
	next, _ := u.Update(key('s'))
	u = next.(uiModel)
	u.m.Cursor = 1
	u, _ = press(t, u, enter, enter)
	u, cmd := press(t, u, enter)
	u = review(t, u, cmd)
	if !strings.Contains(flat(u.View()), "context 84k → ~34k") {
		t.Fatalf("review lacks the context estimate:\n%s", u.View())
	}
}

// A line with no known context number gets no estimate line.
func TestReviewOmitsTheContextEstimateWhenUnknown(t *testing.T) {
	fa := &fakeAdapter{summary: "it went well"}
	u, cmd := squashed(t, fa, &herdrLog{})
	u = review(t, u, cmd)
	if strings.Contains(flat(u.View()), "context ") {
		t.Fatalf("review shows an estimate with no known context:\n%s", u.View())
	}
}

func rowsOf(v string) []string { return strings.Split(strings.TrimSuffix(v, "\n"), "\n") }

// The reviewer's case: scrolled to the bottom of a long paragraph in a
// narrow terminal, then widened, which wraps it into far fewer lines.
func TestAResizeWhileScrolledKeepsTheReviewOnTheSummary(t *testing.T) {
	text := strings.Repeat("word ", 399) + "LASTWORD"
	u, cmd := squashed(t, &fakeAdapter{summary: text}, &herdrLog{})
	u.width, u.height = 40, 20
	u = review(t, u, cmd)
	for i := 0; i < 100; i++ {
		u, _ = press(t, u, down)
	}
	if !strings.Contains(u.View(), "LASTWORD") {
		t.Fatalf("setup: not scrolled to the bottom:\n%s", u.View())
	}
	// The view clamps for itself, before Update has seen the new size...
	wide := u
	wide.width = 300
	if v := wide.View(); !strings.Contains(v, "LASTWORD") {
		t.Fatalf("the last line is gone at the new width:\n%s", v)
	}
	// ...and the resize keeps the stored scroll inside the new wrap.
	next, _ := u.Update(tea.WindowSizeMsg{Width: 300, Height: 20})
	u = next.(uiModel)
	if v := u.View(); !strings.Contains(v, "LASTWORD") {
		t.Fatalf("the last line is gone after the resize:\n%s", v)
	}
	if u.squash.scroll != u.reviewLayout().scroll {
		t.Fatalf("stored scroll %d outside the new layout", u.squash.scroll)
	}
}

// However the parts wrap, the review fits the terminal and its header is on
// the first row.
func TestTheReviewFitsTheScreen(t *testing.T) {
	var long []string
	for i := 1; i <= 60; i++ {
		long = append(long, fmt.Sprintf("line %02d of the summary", i))
	}
	for _, size := range [][2]int{{60, 20}, {80, 24}} {
		for _, summary := range []string{"short", strings.Join(long, "\n")} {
			fa := &fakeAdapter{summary: summary}
			u, cmd := squashed(t, fa, &herdrLog{})
			u.width, u.height = size[0], size[1]
			r := review(t, u, cmd)
			rows := rowsOf(r.View())
			if len(rows) > size[1] || !strings.HasPrefix(rows[0], "Squash turns 2–3 — review the summary") {
				t.Fatalf("%dx%d: %d rows, first %q:\n%s", size[0], size[1], len(rows), rows[0], r.View())
			}
			for _, row := range rows {
				if w := lipgloss.Width(row); w > size[0] {
					t.Fatalf("%dx%d: a row %d wide: %q", size[0], size[1], w, row)
				}
			}
		}
	}
}
