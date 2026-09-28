package tui

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"herdr-tree/internal/adapter"
	"herdr-tree/internal/store"
	"herdr-tree/internal/tree"
)

// LiveFunc reports the pane holding a session and herdr's agent_status for
// it; both empty when no pane holds it. ClosePaneFunc closes a pane. Both are
// injected, like SendFunc, so the handover is testable without Herdr.
type (
	LiveFunc      func(sessionID string) (pane, status string, err error)
	ClosePaneFunc func(paneID string) error
)

// busy reports whether an agent_status forbids touching the session under
// it. Only idle is safe: working, blocked, or a status herdr adds later all
// mean a turn may be running. "" is no agent at all.
func busy(status string) bool { return status != "" && status != "idle" }

// editOp is one context edit, captured when its confirmation is raised.
type editOp struct {
	src       adapter.Session
	edit      adapter.Edit
	kind      string     // store.KindCompacted | KindCut | KindInserted
	summarise bool       // squash: the seed is a summary already made and stored
	from, to  *tree.Node // compact: the range to summarise
	title     string     // row title for the new record
	dst       string
	movedTo   string // a cut that is a move's drop: where the turns went
	editID    string // shared by a cross-line move's two records, so they undo together
	ctx       int    // the source line's context number as shown (real or ~), for the estimate
}

// summariseRange makes the billed summary call over op's range and stores
// the result, so p can fold it in later (§2.6). On failure the status says
// why and that nothing was written.
func summariseRange(a adapter.Adapter, st *store.Store, op editOp) (store.Summary, string) {
	text, err := a.Summarise(op.src, op.from.Node.ID, op.to.Node.ID)
	if err != nil {
		return store.Summary{}, "summarise failed: " + err.Error() + " — nothing was written"
	}
	sum := store.Summary{Text: text, SessionID: op.src.ID,
		FromTurn: op.from.Node.ID, ToTurn: op.to.Node.ID, CreatedAt: time.Now().UTC()}
	st.AddSummary(sum)
	if err := st.Save(); err != nil {
		return store.Summary{}, "summarised, but not saved: " + err.Error() + " — nothing was written"
	}
	return sum, ""
}

// squashing is a squash from its confirmation until it lands
// or is cancelled (§2.9). The confirmation's command summarises and stores;
// the summary is read in review, and commit runs everything after it.
type squashing struct {
	first, last int
	src         string
	figures     string // the Preview figures the confirmation showed
	then        string // the confirmation's Then: line, after "Then: "
	commit      func(store.Summary) tea.Cmd
	since, now  time.Time      // summarising: its start and the latest tick
	sum         *store.Summary // set once made: the review
	scroll      int
	// ctxBefore is the source line's context number at confirm time (§3.2),
	// 0 when unknown — the estimate is then omitted. ctxRange and ctxLine
	// are the widened range's and the whole line's marshalled bytes, used to
	// estimate what the squash saves once the summary's length is known.
	ctxBefore         int
	ctxRange, ctxLine int64
}

// summarisedMsg is a squash's summary, made and stored: the review opens.
type summarisedMsg struct{ sum store.Summary }

// tickMsg advances the summarising view's spinner and clock. gen is the
// squash it was asked for, so a tick still in flight from an earlier one is
// dropped rather than starting a second clock.
type tickMsg struct {
	gen int
	at  time.Time
}

func tick(gen int) tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg{gen, t} })
}

// summariseCmd is a squash up to its review: refused says why it may not
// start, before anything is paid, and then the summary is made and stored.
func summariseCmd(a adapter.Adapter, st *store.Store, op editOp, refused func() string) tea.Cmd {
	return func() tea.Msg {
		if why := refused(); why != "" {
			return actionDoneMsg{status: why}
		}
		sum, failed := summariseRange(a, st, op)
		if failed != "" {
			return actionDoneMsg{status: failed}
		}
		return summarisedMsg{sum}
	}
}

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// summarisingView is the overlay while a squash's summary is made (§2.9).
func (u uiModel) summarisingView() string {
	sq := u.squash
	elapsed := sq.now.Sub(sq.since).Truncate(time.Second)
	s := fmt.Sprintf("Summarising turns %d–%d of %s\n\nthe model is reading %s\n\n%s %s\n\nctrl+c leaves (the call is already billed)\n",
		sq.first, sq.last, shortID(sq.src), sq.figures, spinner[int(elapsed.Seconds())%len(spinner)], elapsed)
	if u.abandoning {
		s += "this call is already billed; ctrl+c again to leave it running\n"
	}
	return s
}

// reviewParts is the review laid out for the terminal: every part wrapped to
// its width, and the box given the rows the rest leaves. scroll is the
// stored one, clamped to what this layout can show.
type reviewParts struct {
	header, title, then, context, footer string
	lines                                []string
	show, scroll                         int
}

func (u uiModel) reviewLayout() reviewParts {
	width, height := u.width, u.height
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}
	// lipgloss pads every line to the width; only the box wants that.
	wrap := func(s string, w int) string {
		ls := strings.Split(lipgloss.NewStyle().Width(w).Render(s), "\n")
		for i := range ls {
			ls[i] = strings.TrimRight(ls[i], " ")
		}
		return strings.Join(ls, "\n")
	}
	rows := func(s string) int {
		if s == "" {
			return 0
		}
		return strings.Count(s, "\n") + 1
	}
	sq := u.squash
	text := sq.sum.Text
	var titleLine string
	if t, body, ok := parseTitle(sq.sum.Text); ok {
		titleLine, text = t, body
	}
	p := reviewParts{
		header: wrap(fmt.Sprintf("Squash turns %d–%d — review the summary", sq.first, sq.last), width),
		title:  wrap(titleLine, width),
		then:   wrap("Then: "+sq.then, width),
		// The border and a space of padding on each side.
		lines: strings.Split(wrap(text, max(width-4, 10)), "\n"),
	}
	if sq.ctxBefore > 0 && sq.ctxLine > 0 {
		ratio := float64(sq.ctxRange) / float64(sq.ctxLine)
		est := float64(sq.ctxBefore)*(1-ratio) + float64(len(sq.sum.Text))/4
		p.context = wrap("context "+humanTokens(sq.ctxBefore)+" → ~"+humanTokens(int(math.Round(est))), width)
	}
	footer := "↑↓ scroll  ⏎ commit  esc cancel (summary kept for p)"
	fit := func(f string) {
		p.footer = wrap(f, width)
		// Besides the parts: a blank under the header, the box's two
		// borders, a blank above the footer. The title, when present, adds
		// its own line plus the blank that separates it from the box. The
		// context estimate, when present, adds its own line.
		titleRows := 0
		if p.title != "" {
			titleRows = rows(p.title) + 1
		}
		p.show = max(height-rows(p.header)-titleRows-rows(p.then)-rows(p.context)-rows(p.footer)-4, 3)
	}
	fit(footer)
	if len(p.lines) > p.show {
		// Fitted with the widest the position can be, then written as it is.
		n := len(p.lines)
		fit(footer + fmt.Sprintf(" · lines %d–%d of %d", n, n, n))
		p.scroll = max(0, min(sq.scroll, n-p.show))
		p.footer = wrap(footer+fmt.Sprintf(" · lines %d–%d of %d", p.scroll+1, p.scroll+p.show, n), width)
	}
	return p
}

// reviewView shows the summary just made, in full, before anything is
// written (§2.9). It is the one place a summary is shown.
func (u uiModel) reviewView() string {
	p := u.reviewLayout()
	shown := p.lines[p.scroll:min(p.scroll+p.show, len(p.lines))]
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).Render(strings.Join(shown, "\n"))
	title := ""
	if p.title != "" {
		title = p.title + "\n\n"
	}
	ctx := ""
	if p.context != "" {
		ctx = "\n" + p.context
	}
	return p.header + "\n\n" + title + box + "\n" + p.then + ctx + "\n\n" + p.footer + "\n"
}

// reviewKey is a key in the review: ⏎ runs the rest of the squash, esc
// writes nothing and leaves the summary stored, arrows scroll, and every
// other key is swallowed.
func (u uiModel) reviewKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	sq := *u.squash
	p := u.reviewLayout()
	sq.scroll = p.scroll
	show := p.show
	switch msg.String() {
	case "up", "k":
		sq.scroll--
	case "down", "j":
		sq.scroll++
	case "pgup":
		sq.scroll -= show
	case "pgdown":
		sq.scroll += show
	case "enter":
		u.squash, u.busy = nil, "squashing…"
		return u, sq.commit(*sq.sum)
	case "esc":
		u.squash = nil
		u.status = "cancelled — nothing was written; the summary is stored for p"
		return u, nil
	}
	sq.scroll = max(0, min(sq.scroll, len(p.lines)-show))
	u.squash = &sq
	return u, nil
}

// squashCmd is squash's confirmed edit (§4), split at its review (§2.9):
// summarise makes and stores the summary unless the line was changed
// elsewhere; commit splices it in as the seed, through editCmd's re-checks.
func squashCmd(a adapter.Adapter, st *store.Store, op editOp, live LiveFunc) (summarise tea.Cmd, commit func(store.Summary) tea.Cmd) {
	summarise = summariseCmd(a, st, op, func() string { return changedElsewhere(st, op.src.ID) })
	commit = func(sum store.Summary) tea.Cmd {
		op.edit.Seed = foldBackSeed(op.from, sum, true)
		op.title = "⤶ " + summaryTitle(sum.Text)
		return editCmd(a, st, op, live)
	}
	return summarise, commit
}

// changedElsewhere is §5.1's refusal: "" if every line in sids is still
// current on disk, as st loaded it, else why the edit is refused. It is
// asked right before anything is paid for or written, and again before a
// splice that follows a summary. ponytail: a window is left between the check and the
// splice's Save, which Save's merge settles for the later edit.
func changedElsewhere(st *store.Store, sids ...string) string {
	for _, sid := range sids {
		cur, err := st.CurrentOnDisk(sid)
		if err != nil {
			return "cannot tell whether this line was changed in another overlay: " + err.Error() + " — nothing was written"
		}
		if cur != sid {
			return "this line was changed in another overlay — reopen the tree"
		}
	}
	return ""
}

const continueThere = " — ⏎ on it to continue there"

// verb names op.kind for status text and confirmations, in git vocabulary:
// squash, drop, merge. The store kind itself (store.KindCompacted etc.) is
// unchanged — it is not shown, so only its display name moves.
func verb(kind string) string {
	switch kind {
	case store.KindCompacted:
		return "squashed"
	case store.KindCut:
		return "drop"
	case store.KindInserted:
		return "merged"
	case store.KindMoved:
		return "move"
	}
	return kind
}

// editCmd runs an edit to completion or to its first failure, in the order
// the spec fixes (§6.1). Each step runs only if the one before succeeded, and
// every status says what DID happen. It only writes: moving to the new line
// is the user's own ⏎ (§6.2).
// estimate is the new line's context after an edit (§3.1): the source's
// number as shown (its last reply's, or its own estimate) scaled by the
// share of bytes kept, plus the seed's text at ~4 bytes a token. 0 when the
// source has no number.
func estimate(before int, res adapter.Spliced, seed string) int {
	if before == 0 || res.LineBytes == 0 {
		return 0
	}
	return int(math.Round(float64(before)*float64(res.KeptBytes)/float64(res.LineBytes) + float64(len(seed))/4))
}

func editCmd(a adapter.Adapter, st *store.Store, op editOp, live LiveFunc) tea.Cmd {
	return func() tea.Msg {
		// A squash was asked before its summary, by summariseCmd.
		if !op.summarise {
			if stale := changedElsewhere(st, op.src.ID); stale != "" {
				return actionDoneMsg{status: stale}
			}
		}
		if live != nil {
			// The summary call takes minutes, and a pane may have opened on
			// the session meanwhile. Whatever was true at confirm time is
			// checked again right before the transcript is read.
			_, status, err := live(op.src.ID)
			if err != nil {
				if op.summarise {
					return actionDoneMsg{status: "summary stored — cannot tell whether the session is busy: " + scrubbed(err, op.edit.Seed) + "; nothing was spliced"}
				}
				return actionDoneMsg{status: "cannot tell whether the session is busy: " + scrubbed(err, op.edit.Seed) + " — nothing was written"}
			}
			if busy(status) {
				if op.summarise {
					return actionDoneMsg{status: "summary stored — agent is " + status + "; select again or use p"}
				}
				return actionDoneMsg{status: "agent is " + status + " — wait for it to finish; nothing was written"}
			}
		}
		if op.summarise {
			if stale := changedElsewhere(st, op.src.ID); stale != "" {
				return actionDoneMsg{status: "summary stored — " + stale}
			}
		}
		res, err := a.Splice(op.src, op.edit, op.dst)
		if err != nil {
			return actionDoneMsg{status: verb(op.kind) + " failed: " + scrubbed(err, op.edit.Seed)}
		}
		b := store.Branch{Kind: op.kind, Title: op.title, CreatedAt: time.Now().UTC(), Edit: op.editID}
		b.EstTokens = estimate(op.ctx, res, op.edit.Seed)
		if op.kind == store.KindCut {
			b.Cut = &store.Cut{Turns: res.Removed, At: res.After, To: op.movedTo}
		}
		// A move within the line is a reorder and leaves no marker (§2.8):
		// "moved to" and "moved from" would name the line itself.
		if op.kind == store.KindMoved && op.edit.Carry.ID != op.src.ID {
			b.MovedFrom = &store.Moved{SessionID: op.edit.Carry.ID, At: res.First}
		}
		st.Replace(op.src.ID, res.SessionID, b)
		if err := st.Save(); err != nil {
			return actionDoneMsg{status: verb(op.kind) + " into " + shortID(res.SessionID) + ", but the tree was not saved: " + err.Error(), wrote: true}
		}
		what := verb(op.kind)
		switch {
		case op.kind == store.KindCut:
			what = fmt.Sprintf("dropped %d turns from", res.Removed)
		case op.kind == store.KindMoved && op.edit.Carry.ID == op.src.ID:
			what = "moved 1 turn within"
		case op.kind == store.KindMoved:
			what = "moved 1 turn into"
		}
		return actionDoneMsg{status: what + " " + shortID(op.src.ID) + " → " + shortID(res.SessionID) + continueThere,
			reload: true, tip: res.SessionID, node: res.First}
	}
}

// handoverCmd opens a replacement with focus and only then closes the pane
// still running the line it replaced (§6.2): if the open fails, the old pane
// is all the user has, so it stays. The confirmation may have sat on screen a
// while, so the old pane is checked again before it is closed.
func handoverCmd(a adapter.Adapter, sid, dst, old, oldPane string, live LiveFunc, closePane ClosePaneFunc) tea.Cmd {
	return func() tea.Msg {
		if err := a.Resume(sid, dst, true); err != nil {
			return actionDoneMsg{status: "could not open " + shortID(sid) + " — old pane left running: " + err.Error()}
		}
		pane, status, err := live(old)
		if err != nil {
			return actionDoneMsg{status: "opened " + shortID(sid) + " — old pane left running: " + err.Error()}
		}
		if pane != oldPane {
			return actionDoneMsg{status: "opened " + shortID(sid) + " — the old pane is already gone", quit: true}
		}
		if busy(status) {
			return actionDoneMsg{status: "opened " + shortID(sid) + " — old pane left running: its agent is " + status}
		}
		if err := closePane(oldPane); err != nil {
			return actionDoneMsg{status: "opened " + shortID(sid) + ", but the old pane did not close: " + err.Error()}
		}
		return actionDoneMsg{status: "opened " + shortID(sid) + ", old pane closed", quit: true}
	}
}

// openTip is ⏎ on a line's tip. A tip already open in a pane is left alone:
// a second claude on one transcript would interleave both. If a line it
// replaced is still open in a pane, that pane is handed over after a
// confirmation; otherwise it is a plain resume, with nothing to confirm.
func (u uiModel) openTip(n *tree.Node) (tea.Model, tea.Cmd) {
	if u.live != nil {
		pane, _, err := u.live(n.SessionID)
		if err != nil {
			u.status = "cannot tell whether this session is open: " + err.Error()
			return u, nil
		}
		if pane != "" {
			u.status = "already open in pane " + pane
			return u, nil
		}
	}
	if u.live != nil && u.st != nil {
		// Every other version of the line, an undone one too (§2.5).
		for _, old := range u.st.Lineage(n.SessionID) {
			if old == n.SessionID {
				continue
			}
			pane, status, err := u.live(old)
			if err != nil {
				u.status = "cannot tell whether the old line is still open: " + err.Error()
				return u, nil
			}
			if pane == "" {
				continue
			}
			if busy(status) {
				u.status = "the old line's agent is " + status + " — wait for it to finish"
				return u, nil
			}
			u.confirm = fmt.Sprintf("Check out the new line:  %q\n\nThe pane running the old line is closed; text typed but not sent there is lost.\n\n[enter] continue   [esc] back", n.Node.Title)
			u.pending, u.pendingBusy = handoverCmd(u.a, n.SessionID, u.dstCWD(n), old, pane, u.live, u.closePane), "opening session…"
			return u, nil
		}
	}
	u.busy = "opening session…"
	return u, resumeCmd(u.a, n, u.dstCWD(n))
}

const replacesLine = "A new session replaces this line in the tree (the old one is hidden, kept on disk)."

// editConfirm raises the one confirmation for a range option (§2.3). kind is
// store.KindCompacted (squash) or store.KindCut (drop).
func (u uiModel) editConfirm(kind string) (tea.Model, tea.Cmd) {
	from, to, ok := u.m.RangeSpan()
	if !ok {
		u.m.CancelRange()
		u.status = "the range is no longer on screen"
		return u, nil
	}
	if from.SessionID != to.SessionID {
		u.status = "a range must stay inside one session"
		return u, nil
	}
	src := adapter.Session{ID: to.SessionID, CWD: to.SessionCWD, Path: to.SessionPath}
	if !u.liveCheck(src.ID) {
		return u, nil
	}
	sp, err := u.a.Widen(src, from.Node.ID, to.Node.ID)
	if err != nil {
		u.status = "cannot edit this range: " + err.Error()
		return u, nil
	}
	op := editOp{src: src, edit: adapter.Edit{From: from.Node.ID, To: to.Node.ID}, kind: kind,
		from: from, to: to, dst: u.dstCWD(to), title: "✂ drop", ctx: to.SessionTokens}
	if kind == store.KindCut {
		text := fmt.Sprintf("Drop turns %d–%d:\n\n  from  %q\n  to    %q\n\nRemoves turns %d–%d. Costs nothing. No note is left in the conversation.\n%s",
			sp.First, sp.Last, from.Node.Title, to.Node.Title, sp.First, sp.Last, replacesLine)
		u.confirm = text + "\n\n[enter] go   [esc] back"
		u.pending, u.pendingBusy = editCmd(u.a, u.st, op, u.live), "dropping…"
		return u, nil
	}
	turns, entries, size, err := u.a.Preview(src, sp.End)
	if err != nil {
		u.status = "cannot summarise this range: " + err.Error()
		return u, nil
	}
	cost := fmt.Sprintf("Squash turns %d–%d:\n\n  from  %q\n  to    %q\n\nThe model reads this session up to the end of the range — %d turn(s) · %d entries · %s — and describes only the range. That whole prefix is billed.\n\nThen: ",
		sp.First, sp.Last, from.Node.Title, to.Node.Title, turns, entries, humanBytes(size))
	figures := fmt.Sprintf("%d turns · %d entries · %s", turns, entries, humanBytes(size))
	if turns == 1 {
		figures = fmt.Sprintf("1 turn · %d entries · %s", entries, humanBytes(size))
	}
	then := fmt.Sprintf("turns %d–%d are replaced by the summary.\n%s", sp.First, sp.Last, replacesLine)
	u.confirm = cost + then + "\n\n[enter] go   [esc] back"
	op.summarise = true
	summarise, commit := squashCmd(u.a, u.st, op, u.live)
	u.pending, u.pendingBusy = summarise, "summarising…"
	u.squash = &squashing{first: sp.First, last: sp.Last, src: src.ID, figures: figures, then: then, commit: commit,
		ctxBefore: from.SessionTokens, ctxRange: sp.RangeBytes, ctxLine: sp.LineBytes}
	return u, nil
}

// liveCheck reports whether sessionID may be edited. false means the edit is
// refused and u.status says why: a busy agent, or a herdr that will not say —
// an edit must know whether a turn is running under it, not guess.
func (u *uiModel) liveCheck(sessionID string) bool {
	if u.live == nil {
		return true
	}
	_, status, err := u.live(sessionID)
	if err != nil {
		u.status = "cannot tell whether this session is open: " + err.Error()
		return false
	}
	if busy(status) {
		u.status = "agent is " + status + " — wait for it to finish"
		return false
	}
	return true
}

var rangeMenu = []string{
	"squash — replace these turns with a summary",
	"drop — remove these turns",
}
var placeMenu = []string{"merge here", "branch here"}

func menuView(heading string, options []string, idx int) string {
	s := heading + "\n\n"
	for i, o := range options {
		marker := "  "
		if i == idx {
			marker = "> "
		}
		s += marker + o + "\n"
	}
	return s + "\n↑↓ choose   [enter] select   [esc] back\n"
}

// openRangeMenu raises the range menu over the fixed range: §2.2. It refuses
// the same way editConfirm does when the range no longer applies, since the
// same checks are needed before either option can be offered.
func (u uiModel) openRangeMenu() (tea.Model, tea.Cmd) {
	from, to, ok := u.m.RangeSpan()
	if !ok {
		u.m.CancelRange()
		u.status = "the range is no longer on screen"
		return u, nil
	}
	if from.SessionID != to.SessionID {
		u.status = "a range must stay inside one session"
		return u, nil
	}
	u.menu, u.menuIdx = "range", 0
	return u, nil
}

// placeChosen acts on the placement menu. Merge rewrites the line in place
// and hides the old one; branch is v2's seeded graft and leaves both visible.
func (u uiModel) placeChosen(idx int) (tea.Model, tea.Cmd) {
	at, sum := u.pickAt, u.placing
	u.pickAt = nil
	src := adapter.Session{ID: at.SessionID, CWD: at.SessionCWD, Path: at.SessionPath}
	if idx == 1 {
		// branch here (§2.5b): graft after at's whole turn, not at's own
		// entry. When at is already the turn's last entry (the common case
		// for a live tip), Widen is a no-op.
		sp, err := u.a.WidenBranch(src, at.Node.ID)
		if err != nil {
			u.status = "cannot branch here: " + err.Error()
			return u, nil
		}
		land := func(sum store.Summary) tea.Cmd {
			return foldBackCmd(u.a, u.st, at, sp, u.dstCWD(at), sum, u.agentFor(at), u.send)
		}
		turns, entries, size, err := u.a.Preview(src, sp.End)
		if err != nil {
			u.status = "cannot branch here: " + err.Error()
			return u, nil
		}
		u.confirm = foldBackConfirmText(at, turns, entries, size)
		u.pending, u.pendingBusy = land(sum), "branching…"
		return u, nil
	}
	if !u.liveCheck(at.SessionID) {
		return u, nil
	}
	land := func(sum store.Summary) tea.Cmd {
		// Nothing is removed, so nothing contracted: a merge is always marked
		// as knowledge arriving, whatever session the summary came from.
		op := editOp{src: src, edit: adapter.Edit{After: at.Node.ID, Seed: foldBackSeed(at, sum, false)}, kind: store.KindInserted,
			dst: u.dstCWD(at), title: "⤶ " + summaryTitle(sum.Text), ctx: at.SessionTokens}
		return editCmd(u.a, u.st, op, u.live)
	}
	sp, err := u.a.Widen(src, at.Node.ID, at.Node.ID)
	if err != nil {
		u.status = "cannot merge here: " + err.Error()
		return u, nil
	}
	u.confirm = fmt.Sprintf("Merge the summary after turn %d:  %q\n\nEverything after it is kept. Costs nothing.\n%s\n\n[enter] merge   [esc] back", sp.Last, at.Node.Title, replacesLine)
	u.pending, u.pendingBusy = land(sum), "merging…"
	return u, nil
}

// foldAt is choosing sum for turn at, from p's picker: at the live tip it is
// the next message, anywhere else the place menu (§2.5).
func (u uiModel) foldAt(at *tree.Node, sum store.Summary) (tea.Model, tea.Cmd) {
	if agent := u.agentFor(at); agent != "" && at.IsSessionLeaf && u.send != nil {
		// The live tip: foldBackCmd's send path ignores sp entirely.
		land := func(sum store.Summary) tea.Cmd {
			return foldBackCmd(u.a, u.st, at, entrySpan(at.Node.ID), u.dstCWD(at), sum, agent, u.send)
		}
		// Nothing is copied and nothing is written: the summary is the next
		// message. There is no cost to show.
		u.busy = "sending…"
		return u, land(sum)
	}
	u.placing, u.pickAt = sum, at
	u.menu, u.menuIdx = "place", 0
	return u, nil
}

// carry is move's hand (§2.8): the one section picked up, until ⏎ puts it
// down or esc puts it back. rows are its head and body, for the preview;
// nothing is written until ⏎.
type carry struct {
	src  adapter.Session
	n    *tree.Node // the row m was pressed on
	head *tree.Node
	rows map[*tree.Node]bool
}

// pickUp is m on a row (§2.8): its whole section is in hand and nothing is
// written. A stretch of turns is squashed first, then moved as one section.
func (u uiModel) pickUp(n *tree.Node) (tea.Model, tea.Cmd) {
	src := adapter.Session{ID: n.SessionID, CWD: n.SessionCWD, Path: n.SessionPath}
	sp, err := u.a.Widen(src, n.Node.ID, n.Node.ID)
	if err != nil {
		u.status = "cannot move this: " + err.Error()
		return u, nil
	}
	if sp.First == 0 {
		u.status = "only whole turns move — this is before the first prompt"
		return u, nil
	}
	head := n
	if !n.IsHead && u.m.parent[n] != nil {
		head = u.m.parent[n]
	}
	mv := &carry{src: src, n: n, head: head, rows: map[*tree.Node]bool{head: true}}
	var body func(h *tree.Node)
	body = func(h *tree.Node) {
		for _, c := range h.Children {
			if c.SessionID == head.SessionID && !c.IsHead && !mv.rows[c] {
				mv.rows[c] = true
				body(c)
			}
		}
	}
	body(head)
	u.moving = mv
	u.offHand()
	u.status = "moving 1 turn — ⏎ puts it here · esc puts it back"
	return u, nil
}

// putDown is ⏎ while moving: the section goes after at's whole turn. Within
// the line it is one splice. Into another line it is carryCmd. A refusal — at
// is the section itself, or the turn right before it, or the move would empty
// its line — comes back from the splice with nothing written, and the
// section stays in hand: only a reload lets go of it.
func (u uiModel) putDown(at *tree.Node) (tea.Model, tea.Cmd) {
	mv := u.moving
	dst := adapter.Session{ID: at.SessionID, CWD: at.SessionCWD, Path: at.SessionPath}
	src := mv.src
	ins := editOp{src: dst, edit: adapter.Edit{From: mv.n.Node.ID, After: at.Node.ID, Carry: &src},
		kind: store.KindMoved, dst: u.dstCWD(at), title: "⇢ move", ctx: at.SessionTokens}
	u.busy = "moving…"
	if dst.ID == src.ID {
		return u, editCmd(u.a, u.st, ins, u.live)
	}
	drop := editOp{src: src, edit: adapter.Edit{From: mv.n.Node.ID, To: mv.n.Node.ID}, kind: store.KindCut,
		dst: u.dstCWD(mv.n), title: "✂ drop", ctx: mv.n.SessionTokens}
	return u, carryCmd(u.a, u.st, ins, drop, u.live)
}

// randRead is crypto/rand.Read, indirected so a test can force the failure
// newEditID must refuse the edit for, rather than write with a fabricated id.
var randRead = rand.Read

// newEditID is a fresh id for the records one edit writes.
func newEditID() (string, error) {
	b := make([]byte, 8)
	if _, err := randRead(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// carryCmd is a move into another line (§2.8): the source is asked first,
// then ins writes the turns into the target (asking it), and only if that
// landed does drop take them out of the source, asking it again. Target
// first: if the drop fails, the turns are in two places, never in none.
func carryCmd(a adapter.Adapter, st *store.Store, ins, drop editOp, live LiveFunc) tea.Cmd {
	return func() tea.Msg {
		id, err := newEditID()
		if err != nil {
			return actionDoneMsg{status: "could not make an edit id: " + err.Error() + " — nothing was written"}
		}
		ins.editID, drop.editID = id, id
		if stale := changedElsewhere(st, drop.src.ID); stale != "" {
			return actionDoneMsg{status: stale}
		}
		if live != nil {
			_, status, err := live(drop.src.ID)
			if err != nil {
				return actionDoneMsg{status: "cannot tell whether the source is busy: " + err.Error() + " — nothing was written"}
			}
			if busy(status) {
				return actionDoneMsg{status: "the source's agent is " + status + " — wait for it to finish; nothing was written"}
			}
		}
		msg := editCmd(a, st, ins, live)().(actionDoneMsg)
		if !msg.reload {
			if msg.wrote {
				msg.status += " — the source was not dropped"
			}
			return msg
		}
		drop.movedTo = msg.tip
		cut := editCmd(a, st, drop, live)().(actionDoneMsg)
		into := "moved into " + shortID(ins.src.ID)
		if cut.wrote {
			// The drop's file exists; only its record is missing.
			return actionDoneMsg{status: into + ", and the source's drop was written but not recorded: " + cut.status, reload: true, tip: msg.tip, node: msg.node}
		}
		if !cut.reload {
			return actionDoneMsg{status: into + ", but the source was not dropped: " + cut.status, reload: true, tip: msg.tip, node: msg.node}
		}
		return actionDoneMsg{status: into + " → " + shortID(msg.tip) + ", dropped from " + shortID(drop.src.ID) + " → " + shortID(cut.tip),
			reload: true, tip: msg.tip, node: msg.node}
	}
}
