package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"herdr-tree/internal/adapter"
	"herdr-tree/internal/store"
	"herdr-tree/internal/tree"
)

// herdrLog records what the handover asked herdr for, in order.
type herdrLog struct {
	status   []string // successive answers to live(); the last repeats; "" is no pane
	liveErr  error
	closeErr error
	calls    []string
}

func (h *herdrLog) live(sid string) (string, string, error) {
	h.calls = append(h.calls, "live "+sid)
	if h.liveErr != nil {
		return "", "", h.liveErr
	}
	if len(h.status) == 0 {
		return "", "", nil
	}
	s := h.status[0]
	if len(h.status) > 1 {
		h.status = h.status[1:]
	}
	if s == "" {
		return "", "", nil
	}
	return "pane-1", s, nil
}

func (h *herdrLog) close(p string) error {
	h.calls = append(h.calls, "close "+p)
	return h.closeErr
}

func rangeUI(t *testing.T, fa *fakeAdapter, h *herdrLog) uiModel {
	t.Helper()
	u := uiModel{m: New(session("s", "t1", "t2", "t3")), a: fa, st: loadedStore(t), repoRoot: "/repo",
		live: h.live, closePane: h.close}
	u.m.Cursor = 2
	next, _ := u.Update(key('s'))
	u = next.(uiModel)
	u.m.Cursor = 1
	return u
}

func press(t *testing.T, u uiModel, msgs ...tea.KeyMsg) (uiModel, tea.Cmd) {
	t.Helper()
	var cmd tea.Cmd
	for _, m := range msgs {
		var next tea.Model
		next, cmd = u.Update(m)
		u = next.(uiModel)
	}
	return u, cmd
}

var (
	enter = tea.KeyMsg{Type: tea.KeyEnter}
	down  = tea.KeyMsg{Type: tea.KeyDown}
	esc   = tea.KeyMsg{Type: tea.KeyEsc}
)

func TestARangeOpensAMenuNotACall(t *testing.T) {
	fa := &fakeAdapter{}
	u, cmd := press(t, rangeUI(t, fa, &herdrLog{}), enter)
	if cmd != nil || u.menu != "range" {
		t.Fatalf("want the range menu and no command; menu=%q", u.menu)
	}
	v, last := u.View(), -1
	for _, want := range []string{
		"squash — replace these turns with a summary",
		"drop — remove these turns",
	} {
		i := strings.Index(v, want)
		if i <= last {
			t.Fatalf("menu does not offer %q after the one before it:\n%s", want, v)
		}
		last = i
	}
	u, _ = press(t, u, esc)
	if u.menu != "" || u.m.RangeEnd == nil {
		t.Fatal("esc on the menu must close it and keep the range")
	}
}

func TestCutConfirmsOnceThenSplicesAndReplaces(t *testing.T) {
	fa := &fakeAdapter{span: adapter.Span{First: 2, Last: 3}}
	u, cmd := press(t, rangeUI(t, fa, &herdrLog{}), enter, down, enter)
	if cmd != nil {
		t.Fatal("cut ran before its confirmation")
	}
	for _, want := range []string{"turns 2–3", "Costs nothing", "hidden"} {
		if !strings.Contains(u.confirm, want) {
			t.Fatalf("cut confirmation lacks %q:\n%s", want, u.confirm)
		}
	}
	u, cmd = press(t, u, enter)
	msg := cmd().(actionDoneMsg)
	if len(fa.spliced) != 1 || fa.spliced[0].Seed != "" {
		t.Fatalf("want one cut, got %+v", fa.spliced)
	}
	if fa.resumed != "" {
		t.Fatal("a session no pane holds must not be opened")
	}
	if u.st.Branches["s"].ReplacedBy != "spliced-sid" {
		t.Fatal("the old line was not marked replaced")
	}
	nb := u.st.Branches["spliced-sid"]
	if nb.Kind != store.KindCut || nb.Cut == nil || nb.Cut.Turns != 2 || nb.Cut.At != "t3" || nb.Title != "✂ drop" {
		t.Fatalf("new record %+v", nb)
	}
	if !msg.reload || msg.quit {
		t.Fatalf("a not-live edit reloads the tree and stays open: %+v", msg)
	}
}

func TestCompactSummarisesThenSplicesWithTheSeed(t *testing.T) {
	fa := &fakeAdapter{summary: "it went well", span: adapter.Span{First: 2, Last: 3}}
	u, _ := press(t, rangeUI(t, fa, &herdrLog{}), enter, enter)
	if !strings.Contains(u.confirm, "billed") || !strings.Contains(u.confirm, "replaced by the summary") {
		t.Fatalf("compact confirmation:\n%s", u.confirm)
	}
	u, cmd := press(t, u, enter)
	commit(t, u, cmd)
	if fa.summarisedFrom != "t2" || fa.summarisedTo != "t3" {
		t.Fatalf("summarised %q..%q", fa.summarisedFrom, fa.summarisedTo)
	}
	if len(fa.spliced) != 1 || !strings.HasPrefix(fa.spliced[0].Seed, claudeCompactionPrefix) ||
		!strings.Contains(fa.spliced[0].Seed, "it went well") {
		t.Fatalf("splice seed %+v", fa.spliced)
	}
	if len(u.st.AllSummaries()) != 1 {
		t.Fatal("the summary must still be stored for p")
	}
}

func TestAFailedSummaryWritesAndClosesNothing(t *testing.T) {
	fa := &fakeAdapter{summariseErr: errors.New("claude: limit reached"), span: adapter.Span{First: 2, Last: 3}}
	h := &herdrLog{status: []string{"idle"}}
	u, _ := press(t, rangeUI(t, fa, h), enter, enter)
	_, cmd := press(t, u, enter)
	msg := run(cmd).(actionDoneMsg)
	if len(fa.spliced) != 0 || fa.resumed != "" {
		t.Fatal("something was written or opened after the summary failed")
	}
	if msg.quit {
		t.Fatal("a failure must stay on screen")
	}
}

func TestAWorkingAgentIsRefusedAtConfirm(t *testing.T) {
	fa := &fakeAdapter{span: adapter.Span{First: 2, Last: 3}}
	u, cmd := press(t, rangeUI(t, fa, &herdrLog{status: []string{"working"}}), enter, down, enter)
	if cmd != nil || u.confirm != "" {
		t.Fatal("a working agent's session reached the confirmation")
	}
	if !strings.Contains(u.status, "working") || u.m.RangeEnd == nil {
		t.Fatalf("status %q; the range must survive a refusal", u.status)
	}
}

// The summary call takes minutes; the user may start a turn in the old pane
// meanwhile. The summary is kept, nothing is spliced.
func TestAnAgentThatStartsWorkingDuringTheSummaryStopsTheSplice(t *testing.T) {
	fa := &fakeAdapter{summary: "s", span: adapter.Span{First: 2, Last: 3}}
	h := &herdrLog{status: []string{"idle", "working"}}
	u, _ := press(t, rangeUI(t, fa, h), enter, enter)
	u, cmd := press(t, u, enter)
	_, msg := commit(t, u, cmd)
	if len(fa.spliced) != 0 {
		t.Fatal("spliced while the agent was working")
	}
	if !strings.Contains(msg.status, "summary stored") {
		t.Fatalf("status %q", msg.status)
	}
	if len(u.st.AllSummaries()) != 1 {
		t.Fatal("the paid-for summary was lost")
	}
}

// A cross-line move needs one id shared by its insert and its drop. If
// crypto/rand fails, carryCmd must refuse the whole move rather than write
// with a fabricated id.
func TestCarryRefusesWhenRandFails(t *testing.T) {
	old := randRead
	randRead = func([]byte) (int, error) { return 0, errors.New("no entropy") }
	defer func() { randRead = old }()

	msg := carryCmd(nil, nil, editOp{}, editOp{}, nil)().(actionDoneMsg)
	if msg.reload || msg.quit {
		t.Fatalf("a failed id generation still went ahead: %+v", msg)
	}
	if !strings.Contains(msg.status, "could not make an edit id") {
		t.Fatalf("status %q, want it to mention the id failure", msg.status)
	}
}

func TestHerdrNotAnsweringRefusesTheEdit(t *testing.T) {
	fa := &fakeAdapter{span: adapter.Span{First: 2, Last: 3}}
	u, cmd := press(t, rangeUI(t, fa, &herdrLog{liveErr: errors.New("herdr agent list timed out")}), enter, down, enter)
	if cmd != nil || u.confirm != "" {
		t.Fatal("an edit went ahead without knowing whether a pane holds the session")
	}
}

func TestNoEditStatusCarriesTheSeed(t *testing.T) {
	fa := &fakeAdapter{summary: sentinel, span: adapter.Span{First: 2, Last: 3},
		spliceErr: errors.New("write failed: " + sentinel)}
	u, _ := press(t, rangeUI(t, fa, &herdrLog{}), enter, enter)
	u, cmd := press(t, u, enter)
	if _, msg := commit(t, u, cmd); strings.Contains(msg.status, sentinel) {
		t.Fatalf("status leaked the summary: %q", msg.status)
	}
}

func pickUI(t *testing.T, fa *fakeAdapter, h *herdrLog) uiModel {
	t.Helper()
	st := loadedStore(t)
	st.AddSummary(store.Summary{Text: "what the branch found", SessionID: "other", FromTurn: "a", ToTurn: "b"})
	u := uiModel{m: New(session("s", "t1", "t2", "t3")), a: fa, st: st, repoRoot: "/repo",
		live: h.live, closePane: h.close}
	u.m.Cursor = 0
	u, _ = press(t, u, key('p'), enter)
	return u
}

func TestPickingASummaryAwayFromTheLiveTipOffersInsertOrBranch(t *testing.T) {
	u := pickUI(t, &fakeAdapter{}, &herdrLog{})
	if u.menu != "place" {
		t.Fatalf("menu %q, want place", u.menu)
	}
	for _, want := range []string{"merge here", "branch here"} {
		if !strings.Contains(u.View(), want) {
			t.Fatalf("placement menu lacks %q:\n%s", want, u.View())
		}
	}
}

func TestInsertSplicesTheSummaryInAndKeepsWhatFollows(t *testing.T) {
	fa := &fakeAdapter{span: adapter.Span{First: 1, Last: 1}}
	u, cmd := press(t, pickUI(t, fa, &herdrLog{}), enter)
	if cmd != nil || !strings.Contains(u.confirm, "Everything after it is kept") {
		t.Fatalf("insert must confirm first:\n%s", u.confirm)
	}
	_, cmd = press(t, u, enter)
	cmd()
	if len(fa.spliced) != 1 {
		t.Fatalf("spliced %+v", fa.spliced)
	}
	e := fa.spliced[0]
	if e.After != "t1" || e.From != "" || !strings.HasPrefix(e.Seed, claudeSummaryPrefix) {
		t.Fatalf("edit %+v, want an insert after t1 seeded with the summary", e)
	}
	if u.st.Branches["spliced-sid"].Kind != store.KindInserted {
		t.Fatal("the new record is not marked inserted")
	}
}

// TestInsertCarriesTheSummarysTitle is §2.10 crossed with p's "merge here":
// a titled summary's seed reads "⤶ merged from <src8>: <title>", and the new
// record's store title is the summary's title, not its first raw line.
func TestInsertCarriesTheSummarysTitle(t *testing.T) {
	st := loadedStore(t)
	st.AddSummary(store.Summary{Text: "Title: Add login screen\n\nstate: the login screen now exists",
		SessionID: "other", FromTurn: "a", ToTurn: "b"})
	fa := &fakeAdapter{span: adapter.Span{First: 1, Last: 1}}
	h := &herdrLog{}
	u := uiModel{m: New(session("s", "t1", "t2", "t3")), a: fa, st: st, repoRoot: "/repo",
		live: h.live, closePane: h.close}
	u.m.Cursor = 0
	u, _ = press(t, u, key('p'), enter)
	u, _ = press(t, u, enter)
	u, cmd := press(t, u, enter)
	cmd()

	if len(fa.spliced) != 1 {
		t.Fatalf("spliced %+v", fa.spliced)
	}
	e := fa.spliced[0]
	want := claudeSummaryPrefix + " " + shortID("other") + ": Add login screen"
	if firstLineOf(e.Seed) != want {
		t.Fatalf("seed first line %q, want %q", firstLineOf(e.Seed), want)
	}
	if got := u.st.Branches["spliced-sid"].Title; got != "⤶ Add login screen" {
		t.Fatalf("branch title %q, want %q", got, "⤶ Add login screen")
	}
}

// p places a summary that already exists: its failure says nothing about one
// being kept.
func TestAFailedPlacementByPSaysNothingAboutAStoredSummary(t *testing.T) {
	fa := &fakeAdapter{seedErr: errors.New("graft refused")}
	u, _ := press(t, pickUI(t, fa, &herdrLog{}), down, enter)
	_, cmd := press(t, u, enter)
	if msg := cmd().(actionDoneMsg); msg.status != "branch failed: graft refused" {
		t.Fatalf("status %q", msg.status)
	}
}

func TestBranchHereIsTodaysFoldBack(t *testing.T) {
	fa := &fakeAdapter{}
	u, _ := press(t, pickUI(t, fa, &herdrLog{}), down, enter)
	if !strings.Contains(u.confirm, "NEW session") {
		t.Fatalf("branch here should raise the v2 fold-back confirmation:\n%s", u.confirm)
	}
	_, cmd := press(t, u, enter)
	cmd()
	if fa.seededWith == "" || len(fa.spliced) != 0 {
		t.Fatal("branch here must graft, not splice")
	}
}

// at puts the cursor on the row of node id.
func at(t *testing.T, u uiModel, id string) uiModel {
	t.Helper()
	for i, r := range u.m.Rows() {
		if r.Node.Node.ID == id {
			u.m.Cursor = i
			return u
		}
	}
	t.Fatalf("no row %q", id)
	return u
}

func TestContinueOnALiveSessionOpensAndClosesNothing(t *testing.T) {
	fa := &fakeAdapter{summary: "x", span: adapter.Span{First: 2, Last: 3}}
	h := &herdrLog{status: []string{"idle"}}
	u, _ := press(t, rangeUI(t, fa, h), enter, enter)
	if strings.Contains(u.confirm, "Text typed but not sent") {
		t.Fatalf("an edit closes no pane, so it must not warn of one:\n%s", u.confirm)
	}
	u, cmd := press(t, u, enter)
	_, msg := commit(t, u, cmd)
	if fa.resumed != "" {
		t.Fatalf("an edit opened %q", fa.resumed)
	}
	for _, c := range h.calls {
		if strings.HasPrefix(c, "close") {
			t.Fatalf("an edit closed a pane: %v", h.calls)
		}
	}
	if !msg.reload || msg.quit || msg.status != "squashed s → spliced- — ⏎ on it to continue there" {
		t.Fatalf("%+v", msg)
	}
}

func TestCutSaysHowManyTurnsWent(t *testing.T) {
	fa := &fakeAdapter{span: adapter.Span{First: 2, Last: 3}}
	u, _ := press(t, rangeUI(t, fa, &herdrLog{}), enter, down, enter)
	_, cmd := press(t, u, enter)
	if msg := cmd().(actionDoneMsg); msg.status != "dropped 2 turns from s → spliced- — ⏎ on it to continue there" {
		t.Fatalf("status %q", msg.status)
	}
}

func sessionOf(id string, turns ...string) adapter.Session {
	s := adapter.Session{ID: id, Title: id, CWD: "/repo", Path: "/transcripts/" + id + ".jsonl"}
	for _, t := range turns {
		s.Nodes = append(s.Nodes, adapter.Node{ID: t, Title: "turn " + t, Kind: adapter.KindHuman})
	}
	return s
}

// cutAndReload cuts t2..t3 of s and feeds the result back, with Discover
// finding the replacement (which gained a turn) and an unrelated session.
func cutAndReload(t *testing.T, current string) uiModel {
	t.Helper()
	fa := &fakeAdapter{span: adapter.Span{First: 2, Last: 3}, sessions: []adapter.Session{
		sessionOf("s", "t1", "t2", "t3"), sessionOf("spliced-sid", "t1", "t3", "t4"), sessionOf("other", "o1")}}
	u := rangeUI(t, fa, &herdrLog{})
	u.current = current
	u, _ = press(t, u, enter, down, enter)
	u, cmd := press(t, u, enter)
	next, _ := u.Update(cmd())
	return next.(uiModel)
}

// A reload that fails after the edit wrote must say both: what was written,
// and that the tree on screen is the old one.
func TestAFailedReloadSaysTheTreeWasNotRefreshed(t *testing.T) {
	fa := &fakeAdapter{span: adapter.Span{First: 2, Last: 3}, discoverErr: errors.New("disk on fire")}
	u, _ := press(t, rangeUI(t, fa, &herdrLog{}), enter, down, enter)
	u, cmd := press(t, u, enter)
	next, _ := u.Update(cmd())
	want := "dropped 2 turns from s → spliced- — ⏎ on it to continue there (tree not refreshed: disk on fire)"
	if got := next.(uiModel).status; got != want {
		t.Fatalf("status %q, want %q", got, want)
	}
}

func TestAReloadPutsTheCursorOnTheNewLinesTip(t *testing.T) {
	u := cutAndReload(t, "")
	if n := u.m.Selected(); n == nil || n.SessionID != "spliced-sid" || !n.IsSessionLeaf {
		t.Fatalf("cursor on %+v, want the replacement's tip", n)
	}
}

func TestScopeFollowsTheReplacementOfTheCurrentSession(t *testing.T) {
	u := cutAndReload(t, "s")
	rows := u.m.Rows()
	if len(rows) == 0 {
		t.Fatal("nothing on screen")
	}
	for _, r := range rows {
		if r.Node.SessionID != "spliced-sid" {
			t.Fatalf("scope fell back to all sessions: row of %q", r.Node.SessionID)
		}
	}
}

func TestBranchHereWritesAndOpensNothing(t *testing.T) {
	fa := &fakeAdapter{}
	u, _ := press(t, pickUI(t, fa, &herdrLog{}), down, enter)
	_, cmd := press(t, u, enter)
	msg := cmd().(actionDoneMsg)
	if fa.seededWith == "" || fa.resumed != "" {
		t.Fatalf("branch here must write and open nothing: resumed %q", fa.resumed)
	}
	if !msg.reload || msg.quit || msg.status != "branched new-sid — ⏎ on it to open it" {
		t.Fatalf("%+v", msg)
	}
}

// replacedUI shows spliced-sid, which replaced s, with the cursor on its tip.
func replacedUI(t *testing.T, fa *fakeAdapter, h *herdrLog) uiModel {
	t.Helper()
	st := loadedStore(t)
	st.Replace("s", "spliced-sid", store.Branch{Kind: store.KindCut})
	roots := tree.Build([]adapter.Session{sessionOf("s", "t1", "t2", "t3"), sessionOf("spliced-sid", "t1", "t3")}, st)
	u := uiModel{m: New(roots), a: fa, st: st, repoRoot: "/repo", live: h.live,
		closePane: func(p string) error {
			if fa.resumed == "" {
				h.calls = append(h.calls, "close-before-resume")
			}
			return h.close(p)
		}}
	u.m.Cursor = len(u.m.Rows()) - 1
	if n := u.m.Selected(); n.SessionID != "spliced-sid" || !n.IsSessionLeaf {
		t.Fatalf("setup: cursor on %+v", n)
	}
	return u
}

func TestEnterOnAReplacementHandsTheOldPaneOver(t *testing.T) {
	fa := &fakeAdapter{}
	h := &herdrLog{status: []string{"", "idle"}}
	u, cmd := press(t, replacedUI(t, fa, h), enter)
	if cmd != nil || !strings.Contains(u.confirm, "text typed but not sent there is lost") {
		t.Fatalf("want a confirmation first:\n%s", u.confirm)
	}
	_, cmd = press(t, u, enter)
	msg := cmd().(actionDoneMsg)
	if fa.resumed != "spliced-sid" || !fa.focused {
		t.Fatalf("resumed %q focused=%v", fa.resumed, fa.focused)
	}
	if got := strings.Join(h.calls, ","); got != "live spliced-sid,live s,live s,close pane-1" {
		t.Fatalf("herdr calls %s, want the old pane closed after the open", got)
	}
	if !msg.quit || msg.status != "opened spliced-, old pane closed" {
		t.Fatalf("%+v", msg)
	}
}

func TestEnterOnAReplacementWhoseOldAgentWorksIsRefused(t *testing.T) {
	fa := &fakeAdapter{}
	h := &herdrLog{status: []string{"", "working"}}
	u, cmd := press(t, replacedUI(t, fa, h), enter)
	if cmd != nil || u.confirm != "" || !strings.Contains(u.status, "working") {
		t.Fatalf("status %q confirm %q", u.status, u.confirm)
	}
	if fa.resumed != "" || strings.Contains(strings.Join(h.calls, ","), "close") {
		t.Fatal("something was opened or closed")
	}
}

func TestIfTheReplacementDoesNotOpenTheOldPaneStays(t *testing.T) {
	fa := &fakeAdapter{resumeErr: errors.New("split refused")}
	h := &herdrLog{status: []string{"", "idle"}}
	u, _ := press(t, replacedUI(t, fa, h), enter)
	_, cmd := press(t, u, enter)
	msg := cmd().(actionDoneMsg)
	if strings.Contains(strings.Join(h.calls, ","), "close") {
		t.Fatalf("the old pane was closed though the new one never opened: %v", h.calls)
	}
	if !strings.Contains(msg.status, "old pane left running") || msg.quit {
		t.Fatalf("%+v", msg)
	}
}

func TestEnterOnAReplacementWithNoOldPaneJustResumes(t *testing.T) {
	fa := &fakeAdapter{}
	u, cmd := press(t, replacedUI(t, fa, &herdrLog{}), enter)
	if cmd == nil || u.confirm != "" {
		t.Fatalf("want a plain resume, got confirm %q", u.confirm)
	}
	cmd()
	if fa.resumed != "spliced-sid" || fa.focused {
		t.Fatalf("resumed %q focused=%v, want unfocused", fa.resumed, fa.focused)
	}
}

func TestAFailedCloseAfterTheHandoverIsReported(t *testing.T) {
	fa := &fakeAdapter{}
	h := &herdrLog{status: []string{"", "idle"}, closeErr: errors.New("no such pane")}
	u, _ := press(t, replacedUI(t, fa, h), enter)
	_, cmd := press(t, u, enter)
	msg := cmd().(actionDoneMsg)
	if fa.resumed != "spliced-sid" || !fa.focused {
		t.Fatalf("resumed %q focused=%v", fa.resumed, fa.focused)
	}
	if msg.quit || msg.status != "opened spliced-, but the old pane did not close: no such pane" {
		t.Fatalf("%+v", msg)
	}
}

func TestEnterOnAReplacementWhenHerdrWillNotSayIsRefused(t *testing.T) {
	fa := &fakeAdapter{}
	u, cmd := press(t, replacedUI(t, fa, &herdrLog{liveErr: errors.New("herdr agent list timed out")}), enter)
	if cmd != nil || u.confirm != "" || fa.resumed != "" {
		t.Fatalf("went ahead without knowing: confirm %q resumed %q", u.confirm, fa.resumed)
	}
	if !strings.HasPrefix(u.status, "cannot tell whether") {
		t.Fatalf("status %q", u.status)
	}
}

func TestABlockedAgentRefusesAnEdit(t *testing.T) {
	fa := &fakeAdapter{span: adapter.Span{First: 2, Last: 3}}
	u, cmd := press(t, rangeUI(t, fa, &herdrLog{status: []string{"blocked"}}), enter, down, enter)
	if cmd != nil || u.confirm != "" || u.status != "s is open in pane pane-1 and herdr says its agent is blocked — nothing was written; try again when its turn ends" {
		t.Fatalf("status %q confirm %q", u.status, u.confirm)
	}
}

func TestABlockedOldAgentRefusesTheHandover(t *testing.T) {
	fa := &fakeAdapter{}
	u, cmd := press(t, replacedUI(t, fa, &herdrLog{status: []string{"", "blocked"}}), enter)
	if cmd != nil || u.confirm != "" || u.status != "the old line: s is open in pane pane-1 and herdr says its agent is blocked — nothing was written; try again when its turn ends" {
		t.Fatalf("status %q confirm %q", u.status, u.confirm)
	}
}

// The confirmation can sit on screen for as long as the user likes; the old
// agent may start a turn meanwhile, and closing its pane would kill it.
func TestTheHandoverChecksTheOldPaneAgainBeforeClosingIt(t *testing.T) {
	fa := &fakeAdapter{}
	h := &herdrLog{status: []string{"", "idle", "working"}}
	u, _ := press(t, replacedUI(t, fa, h), enter)
	_, cmd := press(t, u, enter)
	msg := cmd().(actionDoneMsg)
	if fa.resumed != "spliced-sid" {
		t.Fatalf("resumed %q", fa.resumed)
	}
	if strings.Contains(strings.Join(h.calls, ","), "close") {
		t.Fatalf("closed a pane whose agent was working: %v", h.calls)
	}
	if msg.quit || msg.status != "opened spliced- — old pane left running: its agent is working" {
		t.Fatalf("%+v", msg)
	}
}

// If the old pane closed on its own between the confirmation and the
// handover's recheck, live() reports no pane (or a different one) rather
// than "idle" — the handover is still done, since a new pane is already
// open, but there is no old pane left to close.
func TestTheHandoverFindsTheOldPaneAlreadyGone(t *testing.T) {
	fa := &fakeAdapter{}
	h := &herdrLog{status: []string{"", "idle", ""}}
	u, _ := press(t, replacedUI(t, fa, h), enter)
	_, cmd := press(t, u, enter)
	msg := cmd().(actionDoneMsg)
	if fa.resumed != "spliced-sid" {
		t.Fatalf("resumed %q", fa.resumed)
	}
	if strings.Contains(strings.Join(h.calls, ","), "close") {
		t.Fatalf("closed a pane that was already gone: %v", h.calls)
	}
	if !msg.quit || msg.status != "opened spliced- — the old pane is already gone" {
		t.Fatalf("%+v", msg)
	}
}

func TestAPaneOpenedDuringTheSummaryStopsTheSplice(t *testing.T) {
	fa := &fakeAdapter{summary: "s", span: adapter.Span{First: 2, Last: 3}}
	u, _ := press(t, rangeUI(t, fa, &herdrLog{status: []string{"", "working"}}), enter, enter)
	u, cmd := press(t, u, enter)
	_, msg := commit(t, u, cmd)
	if len(fa.spliced) != 0 || !strings.HasPrefix(msg.status, "summary stored") {
		t.Fatalf("spliced %+v status %q", fa.spliced, msg.status)
	}
}

func TestHerdrFailingAfterTheSummarySaysTheSummaryWasStored(t *testing.T) {
	roots := session("s", "t1", "t2")
	rows := New(roots).Rows()
	op := editOp{src: adapter.Session{ID: "s"}, kind: store.KindCompacted, summarise: true, from: rows[0].Node, to: rows[1].Node}
	fa := &fakeAdapter{summary: "x"}
	live := func(string) (string, string, error) { return "", "", errors.New("timed out") }
	msg := editCmd(fa, loadedStore(t), op, live)().(actionDoneMsg)
	if len(fa.spliced) != 0 || msg.status != "summary stored — cannot tell whether the session is busy: timed out; nothing was spliced" {
		t.Fatalf("spliced %+v status %q", fa.spliced, msg.status)
	}
}

func TestAMissingReplacementKeepsTheCurrentScope(t *testing.T) {
	st := loadedStore(t)
	st.Replace("s", "gone", store.Branch{Kind: store.KindCut})
	u := uiModel{m: New(nil), st: st, current: "s",
		roots: tree.Build([]adapter.Session{sessionOf("s", "t1"), sessionOf("other", "o1")}, st)}
	u.rebuild()
	rows := u.m.Rows()
	if len(rows) == 0 {
		t.Fatal("nothing on screen")
	}
	for _, r := range rows {
		if r.Node.SessionID != "s" {
			t.Fatalf("scope fell back to all sessions: row of %q", r.Node.SessionID)
		}
	}
}

func TestEnterOnATipAlreadyOpenInAPaneOpensNoSecondAgent(t *testing.T) {
	fa := &fakeAdapter{}
	u := uiModel{m: New(session("s", "t1", "t2")), a: fa, st: loadedStore(t), repoRoot: "/repo",
		live: (&herdrLog{status: []string{"idle"}}).live}
	u.m.Cursor = 1
	u, cmd := press(t, u, enter)
	if cmd != nil || fa.resumed != "" || u.status != "already open in pane pane-1" {
		t.Fatalf("status %q resumed %q", u.status, fa.resumed)
	}
}

// TestBBranchesHereWithoutOpeningOrConfirming is §2.5c: b on a mid-line
// prompt row grafts at the turn's last entry, straight off — no confirmation
// — and reloads with the status naming the new branch.
func TestBBranchesHereWithoutOpeningOrConfirming(t *testing.T) {
	fa := &fakeAdapter{span: adapter.Span{End: "t2"}}
	u := uiModel{m: New(session("s", "t1", "t2", "t3")), a: fa, st: loadedStore(t), repoRoot: "/repo"}
	u.m.Cursor = 1 // t2, a mid-line prompt row, not the tip
	u, cmd := press(t, u, key('b'))
	if u.confirm != "" {
		t.Fatalf("b must not confirm, got %q", u.confirm)
	}
	if cmd == nil {
		t.Fatal("b must branch straight off")
	}
	msg := cmd().(actionDoneMsg)
	if fa.branchedAt != "t2" {
		t.Fatalf("want the graft at the turn's last entry %q, got %q", "t2", fa.branchedAt)
	}
	if fa.resumed != "" {
		t.Fatalf("b must open nothing, resumed %q", fa.resumed)
	}
	if !msg.reload || msg.quit || msg.status != "branched new-sid — ⏎ on it to open it" {
		t.Fatalf("%+v", msg)
	}
}

// TestBOnATipMakesAOneRowBranch is §5.3b via the real files harness: a branch
// grafted at a tip carries no turn of its own yet, so it renders as its one
// copied row — the graft point — and nothing opens.
func TestBOnATipMakesAOneRowBranch(t *testing.T) {
	w := newWorld(t)
	w.trunk(sidT, "t1", "t2")
	u := allOf(w.open(sidT))
	u = cursorTo(t, u, sidT, "t2-r") // the tip
	u = drive(t, u, key('b'))
	if u.confirm != "" || u.quitting {
		t.Fatalf("b must ask nothing and open nothing: confirm %q quitting %v", u.confirm, u.quitting)
	}
	if len(w.h.calls) != 0 {
		t.Fatalf("b opened something: %v", w.h.calls)
	}
	st, err := store.Load(w.repo)
	if err != nil {
		t.Fatal(err)
	}
	var branch string
	for sid, b := range st.Branches {
		if b.GraftedFrom.SessionID == sidT {
			branch = sid
		}
	}
	if branch == "" {
		t.Fatalf("no branch recorded off %s: %+v", shortID(sidT), st.Branches)
	}
	if !strings.Contains(u.status, "branched "+shortID(branch)+" — ⏎ on it to open it") {
		t.Fatalf("status %q", u.status)
	}
	n := u.m.Selected()
	if n == nil || n.SessionID != branch {
		t.Fatalf("cursor on %+v, want the new branch", n)
	}
	unfold(u)
	rows := 0
	for _, r := range u.m.Rows() {
		if r.Node.SessionID == branch {
			rows++
		}
	}
	if rows != 1 {
		t.Fatalf("a branch with nothing of its own must keep exactly one row, got %d", rows)
	}
}

// TestBIsSwallowedDuringARangeAMenuAConfirmationAndTargetMode is §2.5c: b
// writes nothing while a range is being fixed, while a menu or confirmation
// is on screen.
func TestBIsSwallowedDuringARangeAMenuAndAConfirmation(t *testing.T) {
	fa := &fakeAdapter{span: adapter.Span{First: 2, Last: 3, Turns: 3}}

	// Mid-range (s fixed the end, cursor not yet moved to a start).
	u := rangeUI(t, fa, &herdrLog{})
	u, cmd := press(t, u, key('b'))
	if cmd != nil || len(fa.writes) != 0 {
		t.Fatalf("b acted mid-range: writes %v", fa.writes)
	}
	for _, k := range "uU" {
		if _, cmd := press(t, u, key(k)); cmd != nil {
			t.Fatalf("%c acted mid-range", k)
		}
	}

	// The range menu.
	u, cmd = press(t, u, enter)
	if u.menu != "range" {
		t.Fatalf("setup: no range menu: %q", u.status)
	}
	u, cmd = press(t, u, key('b'))
	if cmd != nil || len(fa.writes) != 0 {
		t.Fatalf("b acted on the range menu: writes %v", fa.writes)
	}

	// A confirmation (⏎ on a mid-line row raises one).
	fa2 := &fakeAdapter{span: adapter.Span{End: "t2"}}
	u2 := uiModel{m: New(session("s", "t1", "t2", "t3")), a: fa2, st: loadedStore(t), repoRoot: "/repo"}
	u2.m.Cursor = 0
	u2, _ = press(t, u2, enter)
	if u2.confirm == "" {
		t.Fatal("setup: no confirmation")
	}
	u2, cmd = press(t, u2, key('b'))
	if cmd != nil || u2.confirm == "" {
		t.Fatal("b acted on a confirmation")
	}
}

func TestFooterNamesContinueAndBranch(t *testing.T) {
	u := uiModel{m: New(session("s", "t1")), st: loadedStore(t)}
	v := u.View()
	if !strings.Contains(v, "⏎ continue here") || !strings.Contains(v, "b branch") {
		t.Fatalf("footer missing ⏎/b wording:\n%s", v)
	}
}

// Every footer fits an 80-column terminal, and the rows leave it room: the
// whole view fits the height, so no key's name is cut off.
func TestEveryFooterFitsIn80Columns(t *testing.T) {
	var ids []string
	for i := 0; i < 40; i++ {
		ids = append(ids, fmt.Sprintf("t%d", i))
	}
	for _, state := range []string{"normal", "all, human", "range", "moving"} {
		u := uiModel{m: New(session("s", ids...)), st: loadedStore(t), current: "s", width: 80, height: 24,
			status: "branched 1a2b3c4d — ⏎ on it to open it"}
		u.m.SetTrunk(map[string]bool{"s": true})
		want := []string{"↑↓ move", "←→ fold", "⏎ continue here", "b branch", "s select", "m move", "p place", "L label", "a scope:this session", "f filter:all", "esc close", "u undo", "U redo"}
		switch state {
		case "all, human":
			u.scopeAll = true
			u.m.CycleFilter()
			want = []string{"a scope:all sessions", "f filter:human", "esc close"}
		case "range":
			u.m.RangeEnd = u.m.Selected()
			want = []string{"esc cancel range"}
		case "moving":
			n := u.m.Rows()[3].Node
			u.moving = &carry{n: n, head: n, rows: map[*tree.Node]bool{n: true}}
			want = []string{"esc put it back"}
		}
		lines := strings.Split(strings.TrimSuffix(u.View(), "\n"), "\n")
		if len(lines) > u.height {
			t.Errorf("%s: %d lines on a %d-line terminal", state, len(lines), u.height)
		}
		for _, l := range lines {
			if w := lipgloss.Width(l); w > u.width {
				t.Errorf("%s: a %d-column line: %q", state, w, l)
			}
		}
		v := u.View()
		for _, k := range want {
			if !strings.Contains(v, k) {
				t.Errorf("%s: footer lacks %q:\n%s", state, k, v)
			}
		}
	}
}
