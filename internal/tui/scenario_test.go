package tui

// This file imports internal/claude. The "internal/tui must not import
// internal/claude" rule protects the production binary's layering; a
// test-only import creates no cycle, and it is the only way to drive the
// real overlay against the real adapter (controller ruling, Task 16).
//
// Every scenario runs on real files: transcripts in a temp CLAUDE_PROJECTS_DIR
// whose cwd is a temp git repo, a real store under a temp
// HERDR_PLUGIN_CONFIG_DIR, the real claude adapter (Discover, Widen, Preview,
// Splice, Graft, Summarise), tree.Build and this package's Update. Only three
// things are stood in for: `claude` is a stub on PATH that prints a fixed
// summary (so the real Summarise path runs and spends nothing), `herdr` is a
// stub on PATH that fails (so nothing can reach a live Herdr), and Resume,
// which would ask herdr for a pane, is recorded instead.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"herdr-tree/internal/adapter"
	"herdr-tree/internal/claude"
	"herdr-tree/internal/store"
	"herdr-tree/internal/tree"
)

// world is one scenario's filesystem, adapter and panes.
type world struct {
	t     *testing.T
	repo  string // the sessions' cwd, a git repo
	proj  string // CLAUDE_PROJECTS_DIR
	stubs string // where the stub claude and herdr live
	a     *recAdapter
	h     *paneLog
	clock time.Time
	// durations ends every turn with the system turn_duration entry Claude
	// Code writes after a reply: a real turn's last entry is not a row.
	durations bool
}

// recAdapter is the real claude adapter with Resume recorded, since Resume
// is the one method that goes to herdr.
type recAdapter struct {
	adapter.Adapter
	h *paneLog
}

func (r *recAdapter) Resume(sid, _ string, focus bool) error {
	r.h.calls = append(r.h.calls, fmt.Sprintf("resume %s focus=%v", sid, focus))
	return nil
}

// paneLog is herdr as the injected live/closePane funcs see it: which pane
// holds each session and its agent_status. Every call is logged in order,
// Resume's too, so a handover's step order can be asserted.
type paneLog struct {
	panes map[string][2]string // session id -> {pane, status}
	calls []string
}

func (p *paneLog) live(sid string) (string, string, error) {
	p.calls = append(p.calls, "live "+sid)
	v := p.panes[sid]
	return v[0], v[1], nil
}

func (p *paneLog) close(pane string) error {
	p.calls = append(p.calls, "close "+pane)
	return nil
}

func newWorld(t *testing.T) *world {
	t.Helper()
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	w := &world{t: t, repo: repo, proj: t.TempDir(), stubs: t.TempDir(),
		h: &paneLog{panes: map[string][2]string{}}, clock: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)}
	w.a = &recAdapter{Adapter: claude.New(), h: w.h}
	t.Setenv("CLAUDE_PROJECTS_DIR", w.proj)
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", t.TempDir())
	// The summary is fixed and says nothing about the prompt, so a status
	// line carrying it would be caught by the scenarios' text assertions.
	stub := map[string]string{
		"claude": "#!/bin/sh\necho call >> " + filepath.Join(w.stubs, "claude-calls") + "\nprintf 'state: the fixed summary\\nnext: carry on\\n'\n",
		"herdr":  "#!/bin/sh\necho \"stub herdr refused: $*\" >&2\nexit 1\n",
	}
	for name, body := range stub {
		if err := os.WriteFile(filepath.Join(w.stubs, name), []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", w.stubs+string(os.PathListSeparator)+os.Getenv("PATH"))
	return w
}

// summaries is how many times the stub claude was called.
func (w *world) summaries() int {
	b, _ := os.ReadFile(filepath.Join(w.stubs, "claude-calls"))
	return strings.Count(string(b), "call")
}

func (w *world) tick() string {
	w.clock = w.clock.Add(time.Second)
	return w.clock.Format(time.RFC3339)
}

// turnLines is one turn as Claude Code writes it: the prompt, a Bash call,
// its result, and the reply. uuids are <id>-p, -c, -x and -r, so a test can
// name any row; parent is the entry the prompt follows ("" for a root).
func (w *world) turnLines(sid, id, parent string) []map[string]any {
	base := func(typ, uuid, parent string) map[string]any {
		m := map[string]any{"type": typ, "uuid": uuid, "sessionId": sid, "cwd": w.repo,
			"version": "2.1.278", "timestamp": w.tick(), "isSidechain": false, "userType": "external"}
		if parent == "" {
			m["parentUuid"] = nil
		} else {
			m["parentUuid"] = parent
		}
		return m
	}
	p := base("user", id+"-p", parent)
	p["origin"] = map[string]any{"kind": "human"}
	p["message"] = map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "prompt " + id}}}
	c := base("assistant", id+"-c", id+"-p")
	c["requestId"] = "req-" + id + "-1"
	c["message"] = map[string]any{"role": "assistant", "content": []any{map[string]any{
		"type": "tool_use", "id": "toolu_" + id, "name": "Bash", "input": map[string]any{"command": "echo " + id}}}}
	x := base("user", id+"-x", id+"-c")
	x["toolUseResult"] = map[string]any{"stdout": id}
	x["message"] = map[string]any{"role": "user", "content": []any{map[string]any{
		"type": "tool_result", "tool_use_id": "toolu_" + id, "content": id}}}
	r := base("assistant", id+"-r", id+"-x")
	r["requestId"] = "req-" + id + "-2"
	r["message"] = map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "reply " + id}}}
	if !w.durations {
		return []map[string]any{p, c, x, r}
	}
	d := base("system", id+"-d", id+"-r")
	d["subtype"] = "turn_duration"
	d["durationMs"] = 1200
	return []map[string]any{p, c, x, r, d}
}

func (w *world) appendLines(path string, lines []map[string]any) {
	w.t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		w.t.Fatal(err)
	}
	defer f.Close()
	for _, l := range lines {
		b, err := json.Marshal(l)
		if err != nil {
			w.t.Fatal(err)
		}
		f.Write(append(b, '\n'))
	}
}

// trunk writes a new session sid with a preamble and one turn per id.
func (w *world) trunk(sid string, ids ...string) {
	w.t.Helper()
	dir := filepath.Join(w.proj, claude.SlugFor(w.repo))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		w.t.Fatal(err)
	}
	pre := map[string]any{"type": "attachment", "uuid": sid[:4] + "-pre", "parentUuid": nil, "sessionId": sid,
		"cwd": w.repo, "version": "2.1.278", "timestamp": w.tick(), "attachment": map[string]any{"kind": "reminder"}}
	w.appendLines(filepath.Join(dir, sid+".jsonl"), []map[string]any{pre})
	w.typeInto(sid, ids...)
}

// typeInto appends turns to sid's transcript, after its tip, as Claude Code
// does when the user types into it.
func (w *world) typeInto(sid string, ids ...string) {
	w.t.Helper()
	path := w.path(sid)
	es, _, err := claude.ParseFile(path)
	if err != nil {
		w.t.Fatal(err)
	}
	tip := ""
	for _, e := range es {
		if u := e.UUID(); u != "" && (e.Type() == "user" || e.Type() == "assistant" || e.Type() == "attachment") {
			tip = u
		}
	}
	for _, id := range ids {
		lines := w.turnLines(sid, id, tip)
		w.appendLines(path, lines)
		tip = lines[len(lines)-1]["uuid"].(string)
	}
}

func (w *world) path(sid string) string {
	w.t.Helper()
	m, _ := filepath.Glob(filepath.Join(w.proj, "*", sid+".jsonl"))
	if len(m) != 1 {
		w.t.Fatalf("transcript of %s: %v", sid, m)
	}
	return m[0]
}

// open is Run without the terminal: Discover, Load, Build, scoped to current.
func (w *world) open(current string) uiModel {
	w.t.Helper()
	sessions, err := w.a.Discover(w.repo)
	if err != nil {
		w.t.Fatal(err)
	}
	st, err := store.Load(w.repo)
	if err != nil {
		w.t.Fatal(err)
	}
	roots := tree.Build(sessions, st)
	u := uiModel{m: New(roots), a: w.a, st: st, repoRoot: w.repo, current: current, roots: roots,
		live: w.h.live, closePane: w.h.close}
	u.rebuild()
	return u
}

// drive feeds msgs to Update and runs every command it returns on the spot,
// feeding its message back in, until the loop settles or quits. A squash's
// clock is not run (see run), so a squash settles in its review.
func drive(t *testing.T, u uiModel, msgs ...tea.Msg) uiModel {
	t.Helper()
	for _, m := range msgs {
		next, cmd := u.Update(m)
		u = next.(uiModel)
		for cmd != nil {
			out := run(cmd)
			if _, ok := out.(tea.QuitMsg); ok {
				break
			}
			next, cmd = u.Update(out)
			u = next.(uiModel)
		}
		if strings.Contains(u.status, "fixed summary") {
			t.Errorf("a status line carries the summary: %q", u.status)
		}
	}
	return u
}

func typed(s string) []tea.Msg {
	var out []tea.Msg
	for _, r := range s {
		out = append(out, key(r))
	}
	return out
}

// unfold opens every section and every compacted stretch, as → on each would.
func unfold(u uiModel) {
	u.m.Folded = map[*tree.Node]bool{}
	for _, r := range u.m.Rows() {
		if r.Group {
			u.m.open(r.Node.SessionID)
		}
	}
}

// rowOf is the index of sid's row for entry id, -1 if it has none.
func rowOf(u uiModel, sid, id string) int {
	unfold(u)
	for i, r := range u.m.Rows() {
		if r.Node.SessionID == sid && r.Node.Node.ID == id {
			return i
		}
	}
	return -1
}

func cursorTo(t *testing.T, u uiModel, sid, id string) uiModel {
	t.Helper()
	i := rowOf(u, sid, id)
	if i < 0 {
		t.Fatalf("no row %s of %s on screen:\n%s", id, shortID(sid), strings.Join(screen(u), "\n"))
	}
	u.m.Cursor = i
	return u
}

// screen is every row as the user sees it, unfolded, with its cut marker.
// A row with a header line (§5.3e) is both lines, header first, so an index
// is still a row and a join is still the screen.
func screen(u uiModel) []string {
	unfold(u)
	return asShown(u)
}

// asShown is screen without unfolding anything: the rows as they stand.
func asShown(u uiModel) []string {
	var out []string
	for _, r := range u.m.Rows() {
		line, _ := renderRow(r, false, u.current, 0)
		if g := groupLine(r, 0); g != "" {
			line = g + "\n" + line
		}
		if h := headerLine(r, 0); h != "" {
			line = h + "\n" + line
		}
		out = append(out, line+cutNote(r.Node)+compactNote(r))
	}
	return out
}

// allOf is u with every session in view.
func allOf(u uiModel) uiModel {
	u.scopeAll = true
	u.rebuild()
	return u
}

// branch is ⏎ then confirm on sid's entry id, in an overlay opened for
// current. It returns the new session, which the overlay opened (unfocused).
func (w *world) branch(current, sid, id string) string {
	w.t.Helper()
	u := cursorTo(w.t, allOf(w.open(current)), sid, id)
	u = drive(w.t, u, enter)
	if !strings.Contains(u.confirm, "Continue from") {
		w.t.Fatalf("no branch confirmation: %q / %q", u.confirm, u.status)
	}
	before := len(w.h.calls)
	u = drive(w.t, u, enter)
	if !u.quitting || len(w.h.calls) != before+1 || !strings.HasSuffix(w.h.calls[before], "focus=false") {
		w.t.Fatalf("branch did not open unfocused and quit: %q %v", u.status, w.h.calls[before:])
	}
	return strings.Fields(w.h.calls[before])[1]
}

// selectRange fixes a range from sid's entry from to its entry to and
// chooses option opt of the range menu (0 squash, 1 drop).
func selectRange(t *testing.T, u uiModel, sid, from, to string, opt int) uiModel {
	t.Helper()
	u = cursorTo(t, u, sid, to)
	u = drive(t, u, key('s'))
	u = cursorTo(t, u, sid, from)
	u = drive(t, u, enter)
	if u.menu != "range" {
		t.Fatalf("no range menu: %q", u.status)
	}
	for i := 0; i < opt; i++ {
		u = drive(t, u, down)
	}
	return drive(t, u, enter)
}

// replacement is the session that now stands in sid's place.
func (w *world) replacement(sid string) string {
	w.t.Helper()
	st, err := store.Load(w.repo)
	if err != nil {
		w.t.Fatal(err)
	}
	r := st.Current(sid)
	if r == sid {
		w.t.Fatalf("%s was not replaced", shortID(sid))
	}
	return r
}

// checkNoCopies fails if any entry renders twice: a branch's copies of the
// line it left share that line's uuids, so a repeated id is a repeated turn.
func checkNoCopies(t *testing.T, u uiModel) {
	t.Helper()
	unfold(u)
	seen := map[string]string{}
	for _, r := range u.m.Rows() {
		id := r.Node.Node.ID
		if id == "" {
			continue
		}
		if s, dup := seen[id]; dup {
			t.Errorf("entry %s renders in %s and again in %s:\n%s", id, shortID(s), shortID(r.Node.SessionID), strings.Join(screen(u), "\n"))
			return
		}
		seen[id] = r.Node.SessionID
	}
}

// checkHangsUnder asserts child's first row is its first own entry, marked
// ↳ <child>, drawn right after parent's entry at and indented under it — a
// branch is always indented under the turn it left (§5.3d), on the trunk or
// not.
func checkHangsUnder(t *testing.T, u uiModel, child, first, parent, at string) {
	t.Helper()
	unfold(u)
	rows := u.m.Rows()
	p, c := rowOf(u, parent, at), -1
	for i, r := range rows {
		if r.Node.SessionID == child {
			c = i
			break
		}
	}
	if p < 0 || c < 0 {
		t.Fatalf("%s (row %d) or %s's %s (row %d) not on screen:\n%s", shortID(child), c, shortID(parent), at, p, strings.Join(screen(u), "\n"))
	}
	text := screen(u)[c]
	if rows[c].Node.Node.ID != first || !strings.Contains(text, "↳ "+shortID(child)) {
		t.Errorf("%s starts at %q, want %s marked ↳:\n%s", shortID(child), text, first, strings.Join(screen(u), "\n"))
	}
	// A graft is lifted to render as if it hung directly off its turn's own
	// HEAD (§5.3d, and orderedChildren's bodyGrafts lift): body rows first
	// (in Build's own order — the entry named "at" plus any added since,
	// such as a later squash's seed), then every graft, so the child's row
	// comes right after the LAST body row of that turn, not necessarily
	// right after "at" itself, and exactly one level under the head.
	head := p
	for head > 0 && !rows[head].Node.IsHead {
		head--
	}
	after := head + 1
	for after < len(rows) && rows[after].Node.SessionID == rows[head].Node.SessionID && !rows[after].Node.IsHead {
		after++
	}
	if c != after || headerDepth(rows[c]) != rows[head].Depth+1 {
		t.Errorf("%s at row %d header depth %d, want right after %s's turn's body (row %d, head %s at row %d depth %d), its header one level under it:\n%s",
			shortID(child), c, headerDepth(rows[c]), shortID(parent), after, rows[head].Node.Node.ID, head, rows[head].Depth, strings.Join(screen(u), "\n"))
	}
}

// checkVisibleFolded asserts sid has at least one row in the model's CURRENT
// fold state — deliberately never calling unfold first. checkHangsUnder,
// rowOf and checkLines all unfold before looking, so none of them can catch
// a branch that a folded head is hiding (the common case since Task 20's
// whole-turn rule almost always grafts on a body row, not the head).
func checkVisibleFolded(t *testing.T, u uiModel, sid string) {
	t.Helper()
	for _, r := range u.m.Rows() {
		if r.Node.SessionID == sid {
			return
		}
	}
	t.Errorf("%s has no visible row in the default fold state:\n%s", shortID(sid), strings.Join(screen(u), "\n"))
}

// checkLines asserts exactly the named sessions have rows.
func checkLines(t *testing.T, u uiModel, want ...string) {
	t.Helper()
	unfold(u)
	got := map[string]bool{}
	for _, r := range u.m.Rows() {
		got[r.Node.SessionID] = true
	}
	wantSet := map[string]bool{}
	for _, s := range want {
		wantSet[s] = true
		if !got[s] {
			t.Errorf("%s is not on screen", shortID(s))
		}
	}
	for s := range got {
		if !wantSet[s] {
			t.Errorf("%s is on screen and should not be:\n%s", shortID(s), strings.Join(screen(u), "\n"))
		}
	}
}

func checkScopes(t *testing.T, u uiModel, sids ...string) {
	t.Helper()
	for _, s := range sids {
		if ScopeTo(u.roots, s) == nil {
			t.Errorf("ScopeTo(%s) finds nothing", shortID(s))
		}
	}
}

func rowText(u uiModel, sid, id string) string {
	if i := rowOf(u, sid, id); i >= 0 {
		return screen(u)[i]
	}
	return ""
}

const (
	sidT = "a1a1a1a1-0000-4000-8000-000000000001"
	sidU = "b2b2b2b2-0000-4000-8000-000000000002"
)

// branchesOfBranches builds scenario A's forest: T (t1..t5), B off T's turn
// 2 with b1..b3 typed into it, C off B's own turn b1 with c1.
func branchesOfBranches(w *world) (b, c string) {
	w.trunk(sidT, "t1", "t2", "t3", "t4", "t5")
	b = w.branch(sidT, sidT, "t2-r")
	w.typeInto(b, "b1", "b2", "b3")
	c = w.branch(b, b, "b1-r")
	w.typeInto(c, "c1")
	return b, c
}

// A. A branch of a branch renders each line from where it diverges.
func TestScenarioBranchOfABranch(t *testing.T) {
	w := newWorld(t)
	b, c := branchesOfBranches(w)

	for _, current := range []string{sidT, b, c} {
		u := allOf(w.open(current))
		checkVisibleFolded(t, u, b) // before checkLines/checkHangsUnder unfold everything
		checkVisibleFolded(t, u, c)
		checkLines(t, u, sidT, b, c)
		checkHangsUnder(t, u, b, "b1-p", sidT, "t2-r")
		checkHangsUnder(t, u, c, "c1-p", b, "b1-r")
		checkNoCopies(t, u)
		checkScopes(t, u, sidT, b, c)
	}
	// Scoped to T, the whole family is T's tree.
	u := w.open(sidT)
	checkLines(t, u, sidT, b, c)
}

// §3.1/§3.2 end-to-end: a real transcript whose last reply carries
// real-shaped usage produces a header context number through Discover ->
// Build -> headerLine, not by setting Node fields directly (that unit test
// is TestHeaderShowsTheContextNumber in view_test.go).
func TestScenarioHeaderShowsTheLinesRealContextSize(t *testing.T) {
	w := newWorld(t)
	dir := filepath.Join(w.proj, claude.SlugFor(w.repo))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	pre := map[string]any{"type": "attachment", "uuid": sidT[:4] + "-pre", "parentUuid": nil, "sessionId": sidT,
		"cwd": w.repo, "version": "2.1.278", "timestamp": w.tick(), "attachment": map[string]any{"kind": "reminder"}}
	lines := w.turnLines(sidT, "t1", sidT[:4]+"-pre")
	reply := lines[len(lines)-1]["message"].(map[string]any)
	reply["usage"] = map[string]any{
		"input_tokens": 210, "cache_read_input_tokens": 84000, "cache_creation_input_tokens": 0, "output_tokens": 12,
	}
	w.appendLines(filepath.Join(dir, sidT+".jsonl"), append([]map[string]any{pre}, lines...))

	u := allOf(w.open(sidT))
	if !strings.Contains(shown(u), shortID(sidT)+" · 84k") {
		t.Fatalf("header does not carry the context number:\n%s", shown(u))
	}
}

// A2. ⏎ on a folded head row grafts after the WHOLE turn it belongs to
// (§2.5b), not at the prompt itself: branching on t2's PROMPT row must land
// exactly where branching on its REPLY row would — right under t2-r, with
// t2-r itself not duplicated into the branch as a visible copy.
func TestScenarioBranchStartsAfterTheWholeTurn(t *testing.T) {
	w := newWorld(t)
	w.trunk(sidT, "t1", "t2", "t3")

	b := w.branch(sidT, sidT, "t2-p") // the folded head row, not its reply

	es, _, err := claude.ParseFile(w.path(b))
	if err != nil {
		t.Fatal(err)
	}
	last := ""
	for _, e := range es {
		if u := e.UUID(); u != "" {
			last = u
		}
	}
	if last != "t2-r" {
		t.Fatalf("branch file's last copied entry is %q, want t2-r (t2's whole turn, not just its prompt)", last)
	}

	w.typeInto(b, "b1")
	u := allOf(w.open(b))
	checkLines(t, u, sidT, b)
	checkHangsUnder(t, u, b, "b1-p", sidT, "t2-r")
	checkNoCopies(t, u)
}

// A3. §5.3d: the branch off BULLDOG (t2) is always indented under it, bar
// and all, and TRIPPLEDIP (t3) — the trunk's own tail once the user is on
// the branch — stays at the root's depth with no bar. Deliberately NOT
// unfolded: BULLDOG's whole-turn graft (Task 20, §2.5b) lands on its REPLY,
// a body row, and New() folds BULLDOG-p by default. A graft attached under a
// folded head's body must still render — Rows()'s fold only hides a folded
// head's own SAME-SESSION body rows, never a graft, wherever in that turn it
// physically attached — so this exercises the default (folded) state on
// purpose, not through checkHangsUnder/rowOf/checkLines, which all unfold
// first and would never catch this.
func TestScenarioBranchAtBulldogIndentsAndTheTailDoesNot(t *testing.T) {
	w := newWorld(t)
	w.trunk(sidT, "APPLE", "BULLDOG", "TRIPPLEDIP")

	b := w.branch(sidT, sidT, "BULLDOG-p")
	w.typeInto(b, "BRANCH1")

	u := allOf(w.open(b))
	rows := u.m.Rows() // no unfold: the default (folded) state

	find := func(sid, id string) (Row, bool) {
		for _, r := range rows {
			if r.Node.SessionID == sid && r.Node.Node.ID == id {
				return r, true
			}
		}
		return Row{}, false
	}
	bulldogRow, ok := find(sidT, "BULLDOG-p")
	if !ok {
		t.Fatalf("BULLDOG-p not on screen folded:\n%s", strings.Join(screen(u), "\n"))
	}
	branchRow, ok := find(b, "BRANCH1-p")
	if !ok {
		t.Fatalf("the branch is invisible while BULLDOG is folded (its graft point, BULLDOG's reply, is a body row):\n%s", strings.Join(screen(u), "\n"))
	}
	tailRow, ok := find(sidT, "TRIPPLEDIP-p")
	if !ok {
		t.Fatalf("TRIPPLEDIP-p not on screen folded:\n%s", strings.Join(screen(u), "\n"))
	}

	if headerDepth(branchRow) != bulldogRow.Depth+1 || !branchRow.OnTrunk {
		t.Fatalf("the branch at BULLDOG must be indented exactly one level under it with the bar: %+v vs BULLDOG's %+v", branchRow, bulldogRow)
	}
	if tailRow.Depth != bulldogRow.Depth || tailRow.OnTrunk {
		t.Fatalf("TRIPPLEDIP is the abandoned tail: root depth, no bar: %+v", tailRow)
	}
}

// A4. Round-2 review finding: a lifted graft's m.parent still names the
// body row it physically attached to (BULLDOG's reply), not the head it now
// renders under (BULLDOG-p) — orderedChildren lifts it for RENDERING, but
// Build's own Children graph, which m.parent walks, is unchanged. Fold() on
// the branch's own first row must still land on BULLDOG-p, folded or not.
//
// No typeInto after w.branch: nothing new was written into the branch, so
// its own kept row (attachPoint's "nothing new" fallback) is the copy of
// BULLDOG's reply itself — a LEAF, so Fold() always jumps rather than
// folding it first, in every fold state.
func TestFoldOnABranchLandsOnItsHead(t *testing.T) {
	w := newWorld(t)
	w.trunk(sidT, "APPLE", "BULLDOG", "TRIPPLEDIP")
	b := w.branch(sidT, sidT, "BULLDOG-p")

	check := func(t *testing.T, u uiModel) {
		t.Helper()
		i := -1
		for j, r := range u.m.Rows() {
			if r.Node.SessionID == b {
				i = j
				break
			}
		}
		if i < 0 {
			t.Fatalf("the branch's own row is not on screen:\n%s", strings.Join(screen(u), "\n"))
		}
		u.m.Cursor = i
		u.m.Fold()
		cur := u.m.Rows()[u.m.Cursor]
		if cur.Node.SessionID != sidT || cur.Node.Node.ID != "BULLDOG-p" {
			t.Fatalf("Fold() on the branch landed on %s %q, want BULLDOG-p:\n%s",
				shortID(cur.Node.SessionID), cur.Node.Node.ID, strings.Join(screen(u), "\n"))
		}
	}

	t.Run("folded", func(t *testing.T) {
		check(t, allOf(w.open(b))) // the default state
	})
	t.Run("unfolded", func(t *testing.T) {
		u := allOf(w.open(b))
		unfold(u)
		check(t, u)
	})
}

// A branch of a branch of a branch with nothing new of its own: C's one
// kept row (its copy of b1-r) sits under C's Superseded copy of b1-p, and D
// hangs on that kept row. The UI never offers to branch from a line's tip, so
// D's graft is written by hand, as a hand-edited store could. D is drawn one
// level under C's kept b1-r row, so stepping up lands there, the row drawn
// directly above it one level out, not on some ancestor in the graph.
func TestFoldOnABranchOfABranchLandsOnARowYouCanSee(t *testing.T) {
	w := newWorld(t)
	w.trunk(sidT, "APPLE", "BULLDOG", "TRIPPLEDIP")
	b := w.branch(sidT, sidT, "BULLDOG-p")
	w.typeInto(b, "b1", "b2")
	c := w.branch(b, b, "b1-p")
	d := "d4d4d4d4-0000-4000-8000-000000000004"
	body, err := os.ReadFile(w.path(c))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(w.path(c)), d+".jsonl"),
		[]byte(strings.ReplaceAll(string(body), c, d)), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Load(w.repo)
	if err != nil {
		t.Fatal(err)
	}
	st.Add(d, store.Branch{GraftedFrom: store.From{SessionID: c, Node: "b1-r"}})
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}

	check := func(t *testing.T, u uiModel) {
		t.Helper()
		i := -1
		for j, r := range u.m.Rows() {
			if r.Node.SessionID == d {
				i = j
				break
			}
		}
		if i < 0 {
			t.Fatalf("D's own row is not on screen:\n%s", strings.Join(screen(u), "\n"))
		}
		u.m.Cursor = i
		u.m.Fold()
		cur := u.m.Rows()[u.m.Cursor]
		if cur.Node.SessionID != c || cur.Node.Node.ID != "b1-r" {
			t.Fatalf("Fold() on D landed on %s %q, want C's b1-r:\n%s",
				shortID(cur.Node.SessionID), cur.Node.Node.ID, strings.Join(screen(u), "\n"))
		}
	}

	t.Run("folded", func(t *testing.T) {
		check(t, allOf(w.open(d)))
	})
	t.Run("unfolded", func(t *testing.T) {
		u := allOf(w.open(d))
		unfold(u)
		check(t, u)
	})
}

// C. Three replacements of one line in place, with branches off turns 2
// and 4.
func TestScenarioAChainOfReplacements(t *testing.T) {
	w := newWorld(t)
	w.trunk(sidT, "t1", "t2", "t3", "t4", "t5")
	b := w.branch(sidT, sidT, "t2-r")
	w.typeInto(b, "b1")
	d := w.branch(sidT, sidT, "t4-r")
	w.typeInto(d, "d1")
	checkVisibleFolded(t, allOf(w.open(sidT)), b)
	checkVisibleFolded(t, allOf(w.open(sidT)), d)

	// B and D each still hang under their turn of the newest T line,
	// starting at their own first turn.
	step := func(t *testing.T, u uiModel, line string) {
		t.Helper()
		u = allOf(u)
		checkHangsUnder(t, u, b, "b1-p", line, "t2-r")
		checkHangsUnder(t, u, d, "d1-p", line, "t4-r")
		checkNoCopies(t, u)
	}

	// 1. squash T's turn 1.
	u := selectRange(t, w.open(sidT), sidT, "t1-p", "t1-r", 0)
	u = drive(t, u, enter, enter) // confirm, then commit the review
	t1 := w.replacement(sidT)
	if first := u.m.Rows()[0]; first.Node.SessionID != t1 {
		t.Fatalf("scope did not follow the replacement: %q", screen(u)[0])
	}
	if got := screen(u); !strings.Contains(strings.Join(got, "\n"), "⤶ squashed t1-p..t1-r") {
		t.Errorf("no squash seed:\n%s", strings.Join(got, "\n"))
	}
	checkLines(t, allOf(u), t1, b, d)
	t.Run("after squash", func(t *testing.T) {
		step(t, u, t1)
	})

	// 2. drop T's turn 3.
	u = selectRange(t, u, t1, "t3-p", "t3-r", 1)
	u = drive(t, u, enter)
	t2 := w.replacement(t1)
	if got := rowText(u, t2, "t4-p"); !strings.Contains(got, "✂ 1 turns dropped before this") {
		t.Errorf("row after the drop %q, want the ✂ marker", got)
	}
	checkLines(t, allOf(u), t2, b, d)
	t.Run("after drop", func(t *testing.T) {
		step(t, u, t2)
	})

	// 3. squash T's turn 5: the drop's marker is still owed at t4.
	u = selectRange(t, u, t2, "t5-p", "t5-r", 0)
	u = drive(t, u, enter, enter) // confirm, then commit the review
	t3 := w.replacement(t2)
	if w.summaries() != 2 {
		t.Errorf("%d summary calls, want 2", w.summaries())
	}
	t.Run("drop marker survives a later edit", func(t *testing.T) {
		if got := rowText(u, t3, "t4-p"); !strings.Contains(got, "✂ 1 turns dropped before this") {
			t.Errorf("row after the earlier drop %q, want the ✂ marker still there", got)
		}
	})
	checkLines(t, allOf(u), t3, b, d)
	t.Run("after second squash", func(t *testing.T) {
		step(t, u, t3)
	})

	// 4. drop T's turn 4, which D left: D becomes a root from a removed
	// stretch, B stays.
	u = selectRange(t, u, t3, "t4-p", "t4-r", 1)
	u = drive(t, u, enter)
	t4 := w.replacement(t3)
	u = allOf(u)
	checkLines(t, u, t4, b, d)
	root := u.m.Rows()
	var dRow string
	for i, r := range root {
		if r.Node.SessionID == d {
			dRow = screen(u)[i]
			if r.Depth != 0 {
				t.Errorf("D renders at depth %d, want a root", r.Depth)
			}
			break
		}
	}
	if !strings.Contains(dRow, "from a removed stretch") {
		t.Errorf("D's first row %q, want it marked from a removed stretch", dRow)
	}
}

// D. ⏎ on the newest of two in-place replacements hands T's pane over.
func TestScenarioHandoverAfterSeveralEdits(t *testing.T) {
	for _, status := range []string{"idle", "working"} {
		t.Run(status, func(t *testing.T) {
			w := newWorld(t)
			w.trunk(sidT, "t1", "t2", "t3", "t4")
			w.h.panes[sidT] = [2]string{"pane-T", "idle"}

			u := selectRange(t, w.open(sidT), sidT, "t2-p", "t2-r", 0)
			u = drive(t, u, enter, enter) // confirm, then commit the review
			t1 := w.replacement(sidT)
			if !strings.HasSuffix(u.status, "⏎ on it to continue there") {
				t.Fatalf("squash status %q", u.status)
			}
			u = selectRange(t, u, t1, "t4-p", "t4-r", 1)
			u = drive(t, u, enter)
			t2 := w.replacement(t1)
			for _, c := range w.h.calls {
				if strings.HasPrefix(c, "close") || strings.HasPrefix(c, "resume") {
					t.Fatalf("an edit opened or closed a pane: %v", w.h.calls)
				}
			}

			t.Run("the cursor lands on the new tip", func(t *testing.T) {
				if n := u.m.Selected(); n == nil || n.SessionID != t2 || !n.IsSessionLeaf {
					t.Errorf("cursor on %s's %s after the edit, want %s's tip", shortID(n.SessionID), n.Node.ID, shortID(t2))
				}
			})

			w.h.panes[sidT] = [2]string{"pane-T", status}
			w.h.calls = nil
			// T was t1..t4: squashing t2 and dropping t4 leaves t3 as the tip.
			u = drive(t, cursorTo(t, u, t2, "t3-r"), enter)
			if status == "working" {
				if u.confirm != "" || !strings.Contains(u.status, "working") {
					t.Fatalf("a working old pane was not refused: status %q confirm %q", u.status, u.confirm)
				}
				for _, c := range w.h.calls {
					if !strings.HasPrefix(c, "live") {
						t.Fatalf("something was resumed or closed: %v", w.h.calls)
					}
				}
				return
			}
			if !strings.Contains(u.confirm, "The pane running the old line is closed; text typed but not sent there is lost.") {
				t.Fatalf("confirmation:\n%s\nstatus %q", u.confirm, u.status)
			}
			u = drive(t, u, enter)
			var acts []string
			for _, c := range w.h.calls {
				if !strings.HasPrefix(c, "live") {
					acts = append(acts, c)
				}
			}
			if got := strings.Join(acts, ", "); got != "resume "+t2+" focus=true, close pane-T" {
				t.Fatalf("herdr saw %q, want the newest line resumed with focus, then T's pane closed", got)
			}
			if u.status != "opened "+shortID(t2)+", old pane closed" {
				t.Fatalf("status %q", u.status)
			}
		})
	}
}

// E. A second overlay, loaded before the first squashed T, saves a label:
// T stays hidden behind its replacement and the label is kept. T is a
// branch, so both overlays hold a record for it — the one the second save
// must not write back without its replaced_by.
func TestScenarioTwoOverlays(t *testing.T) {
	w := newWorld(t)
	w.trunk(sidU, "u1", "u2")
	tb := w.branch(sidU, sidU, "u1-r")
	w.typeInto(tb, "t1", "t2", "t3", "t4")
	u1, u2 := w.open(tb), w.open(tb)

	u1 = selectRange(t, u1, tb, "t2-p", "t3-r", 0)
	u1 = drive(t, u1, enter, enter) // confirm, then commit the review
	t1 := w.replacement(tb)

	u2 = cursorTo(t, u2, tb, "t4-p")
	u2 = drive(t, u2, key('L'))
	u2 = drive(t, u2, append(typed("keep"), enter)...)
	if u2.status != "" {
		t.Fatalf("label save: %q", u2.status)
	}

	u := allOf(w.open(tb))
	checkVisibleFolded(t, u, t1)
	checkLines(t, u, sidU, t1)
	checkHangsUnder(t, u, t1, "t1-p", sidU, "u1-r")
	if st, _ := store.Load(w.repo); st.Branches[tb].ReplacedBy != t1 || st.Labels[store.LabelKey(tb, "t4-p")] != "keep" {
		t.Fatalf("store after both saves: T %+v, labels %v", st.Branches[tb], st.Labels)
	}
	t.Run("the label shows on the replacement", func(t *testing.T) {
		if got := rowText(u, t1, "t4-p"); !strings.Contains(got, "★ keep") {
			t.Errorf("t4 in T's replacement renders %q, want the label", got)
		}
	})
}

// §5.3c: a label carried into a replacement is the one last set, so
// clearing it on the replacement clears it, and re-labelling replaces it.
func TestScenarioALabelSetOnAReplacementReplacesTheOlderOne(t *testing.T) {
	w := newWorld(t)
	w.trunk(sidT, "t1", "t2", "t3")
	u := drive(t, cursorTo(t, w.open(sidT), sidT, "t3-p"), key('L'))
	u = drive(t, u, append(typed("keep"), enter)...)
	u = drive(t, selectRange(t, u, sidT, "t1-p", "t1-r", 0), enter, enter) // confirm, then commit the review
	t1 := w.replacement(sidT)
	if got := rowText(u, t1, "t3-p"); !strings.Contains(got, "★ keep") {
		t.Fatalf("t3 in the replacement renders %q, want the label carried over", got)
	}

	// Re-labelling first: both steps share the one store, so the order
	// matters, and each needs the label it replaces.
	u = drive(t, cursorTo(t, u, t1, "t3-p"), key('L'))
	u = drive(t, u, append(typed("!"), enter)...)
	if got := rowText(w.open(t1), t1, "t3-p"); !strings.Contains(got, "★ keep!") {
		t.Errorf("after a reload t3 renders %q, want the new label", got)
	}
	st, _ := store.Load(w.repo)
	if _, ok := st.Labels[store.LabelKey(sidT, "t3-p")]; ok || st.Labels[store.LabelKey(t1, "t3-p")] != "keep!" {
		t.Errorf("labels on disk %v, want only the replacement's", st.Labels)
	}

	// Clearing the label on the replacement clears it: T's older copy
	// must not show through.
	u = drive(t, cursorTo(t, u, t1, "t3-p"), key('L'))
	for range "keep!" {
		u = drive(t, u, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	u = drive(t, u, enter)
	if got := rowText(u, t1, "t3-p"); strings.Contains(got, "★") {
		t.Errorf("t3 still renders %q after clearing its label", got)
	}
	if got := rowText(w.open(t1), t1, "t3-p"); strings.Contains(got, "★") {
		t.Errorf("after a reload t3 renders %q, want no label", got)
	}
}

// Two overlays loaded from the same store both show T. The first drops t2;
// the second, still showing the old tree, squashes t3..t4 of T. The second
// is refused before anything is paid for (§5.1): T was replaced since it
// loaded, so it would splice a stale line and orphan the first edit's.
func TestScenarioTwoOverlaysEditTheSameLine(t *testing.T) {
	w := newWorld(t)
	w.trunk(sidT, "t1", "t2", "t3", "t4")
	u1, u2 := w.open(sidT), w.open(sidT)

	u1 = drive(t, selectRange(t, u1, sidT, "t2-p", "t2-r", 1), enter)
	r1 := w.replacement(sidT)
	u2 = drive(t, selectRange(t, u2, sidT, "t3-p", "t4-r", 0), enter)
	if u2.status != staleLine || w.summaries() != 0 {
		t.Fatalf("second overlay's squash: %q, %d summary calls", u2.status, w.summaries())
	}

	for _, sid := range []string{sidT, r1} {
		if _, err := os.Stat(w.path(sid)); err != nil {
			t.Fatalf("%s is gone: %v", shortID(sid), err)
		}
	}
	w.checkTranscripts(2)
	st, _ := store.Load(w.repo)
	if st.Current(sidT) != r1 || st.Branches[r1].Replaces != sidT {
		t.Fatalf("store: T→%s, %s replaces %q", shortID(st.Current(sidT)), shortID(r1), st.Branches[r1].Replaces)
	}
	if got := rowText(w.open(sidT), r1, "t3-p"); got == "" {
		t.Fatalf("scoped to T, the first overlay's line does not show")
	}
	u := allOf(w.open(sidT))
	checkLines(t, u, r1)
	if got := rowText(u, r1, "t2-p"); got != "" {
		t.Fatalf("the dropped t2 shows again: %q", got)
	}
	checkNoCopies(t, u)
}

// staleLine is §5.1's refusal of an edit whose line another overlay replaced.
const staleLine = "this line was changed in another overlay — reopen the tree"

// checkTranscripts fails unless exactly n transcripts are on disk.
func (w *world) checkTranscripts(n int) {
	w.t.Helper()
	m, _ := filepath.Glob(filepath.Join(w.proj, "*", "*.jsonl"))
	if len(m) != n {
		w.t.Fatalf("%d transcripts on disk, want %d: %v", len(m), n, m)
	}
}

// storePath is where store.Load reads w's store.
func (w *world) storePath() string {
	sum := sha256.Sum256([]byte(w.repo))
	return filepath.Join(os.Getenv("HERDR_PLUGIN_CONFIG_DIR"), hex.EncodeToString(sum[:])[:12], "tree.json")
}

// The line is replaced while the summary is being made: the summary is kept,
// the splice is refused, and nothing else is written.
func TestAnEditIsRefusedIfItsLineIsReplacedDuringTheSummary(t *testing.T) {
	w := newWorld(t)
	w.trunk(sidT, "t1", "t2", "t3", "t4")
	u1, u2 := w.open(sidT), w.open(sidT)
	u1 = drive(t, selectRange(t, u1, sidT, "t2-p", "t2-r", 1), enter)
	r1 := w.replacement(sidT)

	// Hide the drop until the summary call, which puts it back.
	aside := filepath.Join(w.stubs, "tree-after.json")
	if err := os.Rename(w.storePath(), aside); err != nil {
		t.Fatal(err)
	}
	body := "#!/bin/sh\ncp " + aside + " " + w.storePath() + "\nprintf 'state: the fixed summary\\nnext: carry on\\n'\n"
	if err := os.WriteFile(filepath.Join(w.stubs, "claude"), []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}

	u2 = drive(t, selectRange(t, u2, sidT, "t3-p", "t4-r", 0), enter, enter) // confirm, then commit the review
	if u2.status != "summary stored — "+staleLine {
		t.Fatalf("status %q", u2.status)
	}
	w.checkTranscripts(2)
	st, _ := store.Load(w.repo)
	if st.Current(sidT) != r1 || len(st.Summaries) != 1 {
		t.Fatalf("store: T→%s, %d summaries", shortID(st.Current(sidT)), len(st.Summaries))
	}
}

// A store that cannot be read cannot say whether the line was replaced, so
// the edit is refused rather than guessed at.
func TestAnEditIsRefusedIfTheStoreCannotBeRead(t *testing.T) {
	w := newWorld(t)
	w.trunk(sidT, "t1", "t2", "t3")
	u := w.open(sidT)
	if err := os.MkdirAll(w.storePath(), 0o700); err != nil { // a directory: ReadFile fails
		t.Fatal(err)
	}
	u = drive(t, selectRange(t, u, sidT, "t2-p", "t3-r", 0), enter)
	if !strings.HasPrefix(u.status, "cannot tell whether this line was changed in another overlay: ") || w.summaries() != 0 {
		t.Fatalf("status %q, %d summary calls", u.status, w.summaries())
	}
	w.checkTranscripts(1)
}

// TestBMidLineShowsTheBranchAndGoesToIt is the reported bug: b on a mid-line
// prompt, in a real-shaped transcript whose turns end with a turn_duration
// entry, made a branch that was not on screen and left the cursor where it
// was. The widened turn ends at that system entry, which is no row, so the
// edge named an id tree.Build could not attach anything to.
func TestBMidLineShowsTheBranchAndGoesToIt(t *testing.T) {
	w := newWorld(t)
	w.durations = true
	w.trunk(sidT, "NUGGET", "TRIPPLEDIP", "BULLDOG", "HORSE")
	u := w.open(sidT)
	for i, r := range u.m.Rows() {
		if r.Node.Node.ID == "TRIPPLEDIP-p" {
			u.m.Cursor = i
		}
	}
	u = drive(t, u, key('b'))
	checkOneRowBranchUnder(t, w, u, "TRIPPLEDIP-p")
}

// checkOneRowBranchUnder: the one branch off sidT has exactly one row, its
// copy of the graft point, one level under the head at, folded and
// unfolded, and the cursor is on it.
func checkOneRowBranchUnder(t *testing.T, w *world, u uiModel, at string) {
	t.Helper()
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
		t.Fatalf("no branch recorded: %q", u.status)
	}
	if n := u.m.Selected(); n == nil || n.SessionID != branch {
		t.Fatalf("cursor on %+v, want the new branch %s:\n%s", n, shortID(branch), strings.Join(screen(u), "\n"))
	}
	for _, folded := range []bool{true, false} {
		if !folded {
			unfold(u)
		} else {
			u.m = New(u.m.Roots)
			u.m.SetTrunk(map[string]bool{sidT: true})
		}
		var head, mine []Row
		for _, r := range u.m.Rows() {
			if r.Node.SessionID == sidT && r.Node.Node.ID == at {
				head = append(head, r)
			}
			if r.Node.SessionID == branch {
				mine = append(mine, r)
			}
		}
		if len(head) != 1 || len(mine) != 1 || headerDepth(mine[0]) != head[0].Depth+1 {
			t.Fatalf("folded=%v: want one branch row one level under %s, got %+v under %+v", folded, at, mine, head)
		}
	}
}

// TestContinueMidLineShowsTheBranch is ⏎'s share of the same edge: continue
// here on a mid-line prompt of real-shaped turns opens a branch that the next
// overlay shows under the turn it came from.
func TestContinueMidLineShowsTheBranch(t *testing.T) {
	w := newWorld(t)
	w.durations = true
	w.trunk(sidT, "NUGGET", "TRIPPLEDIP", "BULLDOG", "HORSE")
	b := w.branch(sidT, sidT, "TRIPPLEDIP-p")
	u := w.open(sidT)
	u.m.RevealTip(b)
	checkOneRowBranchUnder(t, w, u, "TRIPPLEDIP-p")
}

// TestBranchHereMidLineShowsTheBranch is the third writer of the same edge:
// p places a summary at a mid-line prompt of real-shaped turns, branch here.
func TestBranchHereMidLineShowsTheBranch(t *testing.T) {
	w := newWorld(t)
	w.durations = true
	w.trunk(sidT, "NUGGET", "TRIPPLEDIP", "BULLDOG")
	w.trunk(sidU, "u1", "u2")
	// Squash U's u1, storing a summary p can place.
	u := selectRange(t, allOf(w.open(sidT)), sidU, "u1-p", "u1-r", 0)
	u = drive(t, u, enter, enter) // confirm, then commit the review
	u = drive(t, cursorTo(t, u, sidT, "TRIPPLEDIP-p"), key('p'), enter)
	if u.menu != "place" {
		t.Fatalf("no place menu: %q", u.status)
	}
	u = drive(t, u, down, enter, enter) // branch here, confirm
	st, _ := store.Load(w.repo)
	var d string
	for id, br := range st.Branches {
		if br.GraftedFrom.SessionID == sidT {
			d = id
		}
	}
	if d == "" {
		t.Fatalf("no branch recorded off T: %q", u.status)
	}
	rows := w.open(sidT).m.Rows() // folded, scoped to T
	var head, mine []Row
	for _, r := range rows {
		if r.Node.SessionID == sidT && r.Node.Node.ID == "TRIPPLEDIP-p" {
			head = append(head, r)
		}
		if r.Node.SessionID == d {
			mine = append(mine, r)
		}
	}
	if len(head) != 1 || len(mine) != 1 || headerDepth(mine[0]) != head[0].Depth+1 {
		t.Fatalf("want the branch's seed one level under TRIPPLEDIP, got %+v under %+v", mine, head)
	}
}

// oldEdge makes a b on at and then saves its edge the way b did before
// edges named a node: at the turn's closing turn_duration entry, id-d.
func oldEdge(t *testing.T, w *world, at, id string) string {
	t.Helper()
	u := w.open(sidT)
	u = drive(t, cursorTo(t, u, sidT, at), key('b'))
	st, err := store.Load(w.repo)
	if err != nil {
		t.Fatal(err)
	}
	for sid, b := range st.Branches {
		if b.GraftedFrom.SessionID == sidT {
			b.GraftedFrom.Node = id + "-d"
			st.Branches[sid] = b
			if err := st.Save(); err != nil {
				t.Fatal(err)
			}
			return sid
		}
	}
	t.Fatalf("no branch recorded: %q", u.status)
	return ""
}

// TestAnEdgeSavedAtAnInvisibleForkPointStillAttaches heals the branches b,
// ⏎ and branch here saved before the fix: the edge names a turn_duration
// entry, which no line has a node for.
func TestAnEdgeSavedAtAnInvisibleForkPointStillAttaches(t *testing.T) {
	w := newWorld(t)
	w.durations = true
	w.trunk(sidT, "NUGGET", "TRIPPLEDIP", "BULLDOG", "HORSE")
	b := oldEdge(t, w, "TRIPPLEDIP-p", "TRIPPLEDIP")
	u := w.open(sidT)
	u.m.RevealTip(b)
	checkOneRowBranchUnder(t, w, u, "TRIPPLEDIP-p")

	// With a turn of its own, none of its copies render.
	w.typeInto(b, "OWN")
	u = w.open(sidT)
	checkHangsUnder(t, u, b, "OWN-p", sidT, "TRIPPLEDIP-r")
	checkNoCopies(t, u)
}

// The healing must not hide a removed fork point: a turn dropped since still
// leaves its branch a marked root, and a turn dropped before the fork point
// still leaves the branch under the right turn of the new line.
func TestAnInvisibleForkPointAfterADrop(t *testing.T) {
	for _, tc := range []struct {
		drop    string
		removed bool
	}{{"TRIPPLEDIP", true}, {"NUGGET", false}} {
		t.Run(tc.drop, func(t *testing.T) {
			w := newWorld(t)
			w.durations = true
			w.trunk(sidT, "NUGGET", "TRIPPLEDIP", "BULLDOG", "HORSE")
			b := oldEdge(t, w, "TRIPPLEDIP-p", "TRIPPLEDIP")
			u := selectRange(t, allOf(w.open(sidT)), sidT, tc.drop+"-p", tc.drop+"-r", 1)
			u = drive(t, u, enter)
			r := w.replacement(sidT)
			if r == sidT {
				t.Fatalf("no drop: %q", u.status)
			}
			u = allOf(w.open(r))
			var root *tree.Node
			for _, n := range u.m.Roots {
				if n.SessionID == b {
					root = n
				}
			}
			if tc.removed {
				if root == nil || !root.FromRemoved {
					t.Fatalf("a branch off a dropped turn must be a marked root, got %+v:\n%s", root, strings.Join(screen(u), "\n"))
				}
				return
			}
			if root != nil {
				t.Fatalf("the branch fell off the new line:\n%s", strings.Join(screen(u), "\n"))
			}
			checkHangsUnder(t, u, b, "TRIPPLEDIP-r", r, "TRIPPLEDIP-r")
		})
	}
}

// Spliced.After is the next turn's opening entry, always a node, so a drop
// in real-shaped turns marks the row after it, not the line's last row.
func TestADropInRealShapedTurnsMarksTheNextPrompt(t *testing.T) {
	w := newWorld(t)
	w.durations = true
	w.trunk(sidT, "NUGGET", "TRIPPLEDIP", "BULLDOG")
	u := selectRange(t, w.open(sidT), sidT, "TRIPPLEDIP-p", "TRIPPLEDIP-r", 1)
	u = drive(t, u, enter)
	r := w.replacement(sidT)
	if got := rowText(u, r, "BULLDOG-p"); !strings.Contains(got, "✂ 1 turns dropped before this") {
		t.Fatalf("row after the drop %q, want the ✂ marker:\n%s", got, strings.Join(screen(u), "\n"))
	}
}

// headersFamily is the family the user drew for §5.3e: a trunk NUGGET,
// TRIPPLEDIP; a branch off TRIPPLEDIP with BEATS, BEATS22 (current); and a
// sibling off TRIPPLEDIP with HORSE, HORSE22. It returns the overlay opened
// on the first branch, folded, and the two branch ids.
func headersFamily(t *testing.T) (uiModel, string, string) {
	w := newWorld(t)
	w.trunk(sidT, "NUGGET", "TRIPPLEDIP")
	b := w.branch(sidT, sidT, "TRIPPLEDIP-p")
	w.typeInto(b, "BEATS", "BEATS22")
	c := w.branch(sidT, sidT, "TRIPPLEDIP-p")
	w.typeInto(c, "HORSE", "HORSE22")
	u := w.open(b)
	u.width, u.height = 120, 40
	return u, b, c
}

// rowLines is View's row area: every line before the blank line above the
// counter, the cursor column dropped.
func rowLines(t *testing.T, u uiModel) (lines []string, cursor int) {
	t.Helper()
	cursor = -1
	for i, l := range strings.Split(u.View(), "\n") {
		if l == "" {
			break
		}
		if strings.HasPrefix(l, "> ") {
			if cursor >= 0 {
				t.Fatalf("two cursor lines:\n%s", u.View())
			}
			cursor = i
		}
		lines = append(lines, l[2:])
	}
	return lines, cursor
}

// TestEachSessionStartsWithItsOwnHeaderLine is the picture the user approved
// for §5.3e, drawn by View from real files.
func TestEachSessionStartsWithItsOwnHeaderLine(t *testing.T) {
	u, b, c := headersFamily(t)
	got, _ := rowLines(t, u)
	want := []string{
		"▎ " + shortID(sidT),
		"▎ ▸ user: prompt NUGGET  (2)  ~<1k",
		"▎ ▸ user: prompt TRIPPLEDIP  (2)  ~<1k",
		"▎   ↳ " + shortID(b),
		"▎     ▸ user: prompt BEATS  (2)  ~<1k",
		"▎     ▸ user: prompt BEATS22  (2)  ~<1k",
		"    ↳ " + shortID(c),
		"      ▸ user: prompt HORSE  (2)  ~<1k",
		"      ▸ user: prompt HORSE22  (2)  ~<1k",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// The header is part of its turn's row: ↑↓ step over it, the cursor is never
// on it, and the counter counts rows.
func TestTheCursorNeverSitsOnAHeader(t *testing.T) {
	u, _, _ := headersFamily(t)
	u.m.Cursor = 0
	total := len(u.m.Rows())
	if total != 6 {
		t.Fatalf("%d rows, want 6 (headers are not rows)", total)
	}
	for i := 0; i < total; i++ {
		lines, cur := rowLines(t, u)
		if cur < 0 || !strings.Contains(lines[cur], "user: prompt") {
			t.Fatalf("row %d: the cursor is on %q, not a turn:\n%s", i, lines[max(cur, 0)], u.View())
		}
		if want := fmt.Sprintf("(%d/%d)", i+1, total); !strings.Contains(u.View(), want) {
			t.Fatalf("counter is not %s:\n%s", want, u.View())
		}
		u = drive(t, u, down)
	}
	if u.m.Cursor != total-1 {
		t.Fatalf("↓ past the end moved the cursor to %d", u.m.Cursor)
	}
}

// The viewport fits lines, not rows: a header never pushes the cursor's row
// off screen, and the header of the cursor's row is drawn with it.
func TestTheWindowFitsLinesAndKeepsTheCursorsHeader(t *testing.T) {
	u, _, _ := headersFamily(t)
	u.height = 9 // five lines of rows
	for i := range u.m.Rows() {
		u.m.Cursor = i
		lines, cur := rowLines(t, u)
		if len(lines) > 5 {
			t.Fatalf("cursor %d: %d lines of rows in a five-line viewport:\n%s", i, len(lines), u.View())
		}
		if cur < 0 {
			t.Fatalf("cursor %d is off screen:\n%s", i, u.View())
		}
		if h := headerLine(u.m.Rows()[i], 0); h != "" && (cur == 0 || !strings.HasSuffix(lines[cur-1], h)) {
			t.Fatalf("cursor %d: its header %q is not drawn above it:\n%s", i, h, u.View())
		}
	}
}

// ⏎, b and s on a branch's first turn act on that turn, not on its header.
func TestKeysOnABranchsFirstTurnActOnTheTurn(t *testing.T) {
	u, b, _ := headersFamily(t)
	at := -1
	for i, r := range u.m.Rows() {
		if r.Node.SessionID == b && r.Node.Node.ID == "BEATS-p" {
			at = i
		}
	}
	if at < 0 {
		t.Fatal("no BEATS row")
	}
	u.m.Cursor = at

	if got := drive(t, u, enter); !strings.Contains(got.confirm, `"prompt BEATS"`) {
		t.Errorf("⏎ confirms %q, want BEATS", got.confirm)
	}
	if got := drive(t, u, key('s')); got.m.RangeEnd == nil || got.m.RangeEnd.Node.ID != "BEATS-p" {
		t.Errorf("s fixed the range end at %+v, want BEATS-p", got.m.RangeEnd)
	}
	u.m.CancelRange() // u.m is shared with the s above
	u.m.Cursor = at
	drive(t, u, key('b'))
	st, err := store.Load(u.repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, br := range st.Branches {
		if br.GraftedFrom.SessionID == b && strings.HasPrefix(br.GraftedFrom.Node, "BEATS-") {
			found = true
		}
	}
	if !found {
		t.Errorf("b did not branch at BEATS: %+v", st.Branches)
	}
}

// TestAMergedSummaryStartsItsOwnSection is §5.3f, on real files whose turns
// end with a turn_duration entry (w.durations): merging a stored summary
// onto HORSE22's tip lands the ⤶ row as its own section — at HORSE22's own
// depth, right after it — HORSE22 stays folded, and the cursor is on the ⤶
// row. A reply typed after the seed then folds under the seed itself, like
// any other section.
func TestAMergedSummaryStartsItsOwnSection(t *testing.T) {
	w := newWorld(t)
	w.durations = true
	w.trunk(sidT, "NUGGET", "TRIPPLEDIP")
	b := w.branch(sidT, sidT, "TRIPPLEDIP-p")
	w.typeInto(b, "BEATS", "BEATS22")
	c := w.branch(sidT, sidT, "TRIPPLEDIP-p")
	w.typeInto(c, "HORSE", "HORSE22")

	// Squash all of B, storing a summary p can later place elsewhere.
	u := allOf(w.open(c))
	u = selectRange(t, u, b, "BEATS-p", "BEATS22-r", 0)
	u = drive(t, u, enter, enter) // confirm, then commit the review
	if w.summaries() != 1 {
		t.Fatalf("%d summary calls, want 1; status %q", w.summaries(), u.status)
	}

	// p at HORSE22's tip, merge here, confirm.
	u = cursorTo(t, u, c, "HORSE22-r")
	u = drive(t, u, key('p'))
	if u.picking == nil {
		t.Fatalf("no summary offered: %q", u.status)
	}
	u = drive(t, u, enter) // the only summary on offer
	if u.menu != "place" {
		t.Fatalf("no place menu: %q", u.status)
	}
	u = drive(t, u, enter) // merge here
	if !strings.Contains(u.confirm, "Merge the summary after turn") {
		t.Fatalf("no merge confirmation: %q", u.confirm)
	}
	u = drive(t, u, enter) // confirm

	r := w.replacement(c)
	seed := u.m.Selected()
	if seed == nil || seed.SessionID != r || seed.Node.Kind != adapter.KindSummaryImport {
		t.Fatalf("cursor is not on the merged seed: %+v (status %q)", seed, u.status)
	}
	rows := u.m.Rows()
	if u.m.Cursor == 0 {
		t.Fatalf("nothing precedes the seed")
	}
	prev := rows[u.m.Cursor-1]
	if prev.Node.SessionID != r || prev.Node.Node.ID != "HORSE22-p" {
		t.Fatalf("the seed does not directly follow HORSE22's section head, got %+v", prev)
	}
	if rows[u.m.Cursor].Depth != prev.Depth {
		t.Fatalf("seed depth %d, want HORSE22's own depth %d", rows[u.m.Cursor].Depth, prev.Depth)
	}
	if !u.m.Folded[prev.Node] {
		t.Fatalf("HORSE22 must stay folded — the merge must not unfold the turn before the seed")
	}

	// An assistant reply after the seed — not a new prompt — is the seed's
	// own body, one level under it, and hidden by the seed's own default
	// fold, exactly like any other section's body.
	w.appendLines(w.path(r), []map[string]any{{
		"type": "assistant", "uuid": "NEXT-r", "parentUuid": seed.Node.ID, "sessionId": r, "cwd": w.repo,
		"version": "2.1.278", "timestamp": w.tick(), "isSidechain": false, "userType": "external",
		"requestId": "req-NEXT-1",
		"message":   map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "ok"}}},
	}})
	unf := allOf(w.open(c))
	seedIdx, nextIdx := rowOf(unf, r, seed.Node.ID), rowOf(unf, r, "NEXT-r")
	if seedIdx < 0 || nextIdx < 0 {
		t.Fatalf("seed or NEXT-r missing:\n%s", strings.Join(screen(unf), "\n"))
	}
	unfRows := unf.m.Rows()
	if nextIdx != seedIdx+1 || unfRows[nextIdx].Depth != unfRows[seedIdx].Depth+1 {
		t.Fatalf("NEXT-r must be the seed's own body: seed row %+v, NEXT-r row %+v", unfRows[seedIdx], unfRows[nextIdx])
	}

	folded := allOf(w.open(c)) // fresh, default fold state
	var sawSeed, sawNext bool
	for _, row := range folded.m.Rows() {
		if row.Node.SessionID != r {
			continue
		}
		switch row.Node.Node.ID {
		case seed.Node.ID:
			sawSeed = true
			if !row.Folded {
				t.Fatalf("the seed's new reply must be folded by default")
			}
		case "NEXT-r":
			sawNext = true
		}
	}
	if !sawSeed {
		t.Fatalf("the seed row is missing from the default view")
	}
	if sawNext {
		t.Fatalf("NEXT-r must be hidden while the seed's own section is folded by default")
	}
}

// TestASquashSeedStartsTheFirstSection is §5.3f's other half: a squash of a
// line's first turn writes a new session whose first entry IS the ⤶
// squashed seed, and it renders as the first section — not the body of
// nothing before it — with the remaining turns as their own sections at the
// same depth.
func TestASquashSeedStartsTheFirstSection(t *testing.T) {
	w := newWorld(t)
	w.trunk(sidT, "t1", "t2", "t3")
	u := selectRange(t, w.open(sidT), sidT, "t1-p", "t1-r", 0)
	u = drive(t, u, enter, enter) // confirm, then commit the review
	r := w.replacement(sidT)

	rows := allOf(w.open(r)).m.Rows()
	if len(rows) < 2 {
		t.Fatalf("want at least the seed and t2's head, got %+v", rows)
	}
	seed, next := rows[0], rows[1]
	if seed.Node.SessionID != r || seed.Node.Node.Kind != adapter.KindSummaryCompaction || seed.Depth != 0 {
		t.Fatalf("the squash seed must be the first section, at depth 0: %+v", seed)
	}
	if next.Node.SessionID != r || next.Node.Node.ID != "t2-p" || next.Depth != seed.Depth {
		t.Fatalf("t2 must follow the seed as its own section, at the same depth: %+v", next)
	}
}

// A long typed-in message Claude Code delivers to a live agent is stored
// wrapped in its paste marker, not as a bare ⤶ line (§2.5d). It must still
// be recognised as a summary: an import row, its own section, titled by the
// ⤶ line inside the wrapper.
func TestAPastedSummaryDeliveredToTheTipIsRecognised(t *testing.T) {
	w := newWorld(t)
	w.trunk(sidT, "t1")

	path := w.path(sidT)
	es, _, err := claude.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tip := ""
	for _, e := range es {
		if u := e.UUID(); u != "" && (e.Type() == "user" || e.Type() == "assistant" || e.Type() == "attachment") {
			tip = u
		}
	}
	wantTitle := claudeSummaryPrefix + " 3510bb7c: Ranked options for belt-fryer HMI query performance"
	text := "\n\n<pasted_content id=\"8aa6\">\n" + wantTitle + "\n\n**state**\n- No" + "\n</pasted_content>"
	pasted := map[string]any{
		"type": "user", "uuid": "s1", "parentUuid": tip, "sessionId": sidT, "cwd": w.repo,
		"version": "2.1.278", "timestamp": w.tick(), "isSidechain": false, "userType": "external",
		"origin":  map[string]any{"kind": "human"},
		"message": map[string]any{"role": "user", "content": text},
	}
	reply := map[string]any{
		"type": "assistant", "uuid": "s1-r", "parentUuid": "s1", "sessionId": sidT, "cwd": w.repo,
		"version": "2.1.278", "timestamp": w.tick(), "isSidechain": false, "requestId": "req-s1",
		"message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "reply s1"}}},
	}
	w.appendLines(path, []map[string]any{pasted, reply})

	u := allOf(w.open(sidT))
	i := rowOf(u, sidT, "s1")
	if i < 0 {
		t.Fatalf("no row for the pasted summary on screen:\n%s", strings.Join(screen(u), "\n"))
	}
	row := u.m.Rows()[i]
	if row.Node.Node.Kind != adapter.KindSummaryImport {
		t.Fatalf("kind = %v, want KindSummaryImport", row.Node.Node.Kind)
	}
	if row.Node.Node.Title != wantTitle {
		t.Fatalf("title = %q, want %q", row.Node.Node.Title, wantTitle)
	}
	if !row.Node.IsHead {
		t.Fatal("the pasted summary must open its own section")
	}
	line, style := renderRow(row, false, u.current, 0)
	if style != StyleImport {
		t.Fatalf("style = %v, want StyleImport, rendering %q", style, line)
	}
}

// fixture copies one of internal/claude's transcripts into the world as sid,
// its session id and cwd rewritten.
func (w *world) fixture(sid, name string) {
	w.t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "claude", "testdata", name))
	if err != nil {
		w.t.Fatal(err)
	}
	s := strings.ReplaceAll(string(b), `"sessionId":"S"`, `"sessionId":"`+sid+`"`)
	s = strings.ReplaceAll(s, `"cwd":"/repo"`, `"cwd":"`+w.repo+`"`)
	dir := filepath.Join(w.proj, claude.SlugFor(w.repo))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, sid+".jsonl"), []byte(s), 0o600); err != nil {
		w.t.Fatal(err)
	}
}

// history is sid's transcript as the agent resumes it: its uuids in file
// order and its leaf pointer.
func (w *world) history(sid string) (uuids []string, leaf string) {
	w.t.Helper()
	es, _, err := claude.ParseFile(w.path(sid))
	if err != nil {
		w.t.Fatal(err)
	}
	for _, e := range es {
		if u := e.UUID(); u != "" {
			uuids = append(uuids, u)
		}
		if e.Type() == "last-prompt" {
			leaf, _ = e.Raw["leafUuid"].(string)
		}
	}
	return uuids, leaf
}

// branchOff is the one session recorded as grafted from sid.
func (w *world) branchOff(sid string) string {
	w.t.Helper()
	st, err := store.Load(w.repo)
	if err != nil {
		w.t.Fatal(err)
	}
	for id, b := range st.Branches {
		if b.GraftedFrom.SessionID == sid {
			return id
		}
	}
	w.t.Fatalf("no branch off %s", shortID(sid))
	return ""
}

// A turn before a native /compact has a row, and every way of branching from
// it works: the branch resumes the pre-compact history through the end of
// that turn. It is not on the session's current line, so an edit of it is
// still refused (§2.5b).
func TestScenarioBranchingFromBeforeANativeCompact(t *testing.T) {
	for _, fx := range []string{"compacted.jsonl", "compacted-preorigin.jsonl"} {
		for _, how := range []string{"continue", "b", "branch here"} {
			t.Run(fx+"/"+how, func(t *testing.T) {
				w := newWorld(t)
				w.fixture(sidT, fx)
				if how == "branch here" {
					st, _ := store.Load(w.repo)
					st.AddSummary(store.Summary{Text: "the fixed summary", SessionID: sidU, FromTurn: "x", ToTurn: "y", CreatedAt: w.clock})
					if err := st.Save(); err != nil {
						t.Fatal(err)
					}
				}
				u := cursorTo(t, w.open(sidT), sidT, "u1")
				want := "u1,a1"
				switch how {
				case "continue":
					u = drive(t, u, enter)
					if !strings.Contains(u.confirm, "Continue from") {
						t.Fatalf("no confirmation: %q", u.status)
					}
					u = drive(t, u, enter)
				case "b":
					u = drive(t, u, key('b'))
				case "branch here":
					u = drive(t, u, key('p'), enter, down, enter)
					if !strings.Contains(u.confirm, "Branch at") {
						t.Fatalf("no branch here confirmation: %q", u.status)
					}
					u = drive(t, u, enter)
				}
				br := w.branchOff(sidT)
				uuids, leaf := w.history(br)
				got := strings.Join(uuids, ",")
				if how == "branch here" {
					if len(uuids) != 3 || leaf != uuids[2] {
						t.Fatalf("branch holds %s, leaf %s: want u1,a1 and the seed as leaf (status %q)", got, leaf, u.status)
					}
					got = strings.Join(uuids[:2], ",")
				} else if leaf != "a1" {
					t.Fatalf("branch leaf %s, want a1", leaf)
				}
				if got != want {
					t.Fatalf("branch holds %s, want %s (status %q)", got, want, u.status)
				}
				checkLines(t, w.open(sidT), sidT, br) // the family: it hangs off T
			})
		}
	}
}

func TestScenarioAnEditBeforeANativeCompactIsStillRefused(t *testing.T) {
	w := newWorld(t)
	w.fixture(sidT, "compacted.jsonl")
	u := cursorTo(t, w.open(sidT), sidT, "a1")
	u = drive(t, u, key('s'))
	u = cursorTo(t, u, sidT, "u1")
	u = drive(t, u, enter, enter) // the range menu, squash
	if u.confirm != "" || !strings.Contains(u.status, "before a /compact — already summarised by Claude Code") {
		t.Fatalf("squash before the boundary: confirm %q, status %q", u.confirm, u.status)
	}
	if w.summaries() != 0 {
		t.Fatal("a refused squash was paid for")
	}
	if !strings.Contains(shown(allOf(w.open(sidT))), "compacted by Claude Code — context starts here") {
		t.Errorf("the tree does not mark where the context starts:\n%s", shown(allOf(w.open(sidT))))
	}
}

// After a squash or merge in the middle of a line the cursor lands on its
// seed, not on the line's tip (§5.3f).
func TestAMidLineSquashOrMergeLandsOnItsSeed(t *testing.T) {
	for _, how := range []string{"squash", "merge"} {
		t.Run(how, func(t *testing.T) {
			w := newWorld(t)
			w.trunk(sidT, "t1", "t2", "t3", "t4")
			w.trunk(sidU, "u1", "u2")
			u := allOf(w.open(sidT))
			if how == "squash" {
				u = selectRange(t, u, sidT, "t2-p", "t2-r", 0)
				u = drive(t, u, enter, enter) // confirm, then commit the review
			} else {
				u = selectRange(t, u, sidU, "u1-p", "u1-r", 0)
				u = drive(t, u, enter, enter)
				u = drive(t, cursorTo(t, u, sidT, "t2-r"), key('p'), enter, enter, enter) // pick, merge here, confirm
			}
			n := u.m.Selected()
			if n == nil || n.SessionID != w.replacement(sidT) || !strings.HasPrefix(n.Node.Title, "⤶") {
				t.Fatalf("cursor on %+v, want the seed (status %q)", n, u.status)
			}
		})
	}
}

// b lands the cursor on the new branch without unfolding the turn it came
// from: a folded turn shows the branches off its body already (bodyGrafts).
func TestBLeavesTheParentsTurnFolded(t *testing.T) {
	w := newWorld(t)
	w.durations = true
	w.trunk(sidT, "NUGGET", "TRIPPLEDIP", "BULLDOG")
	u := drive(t, cursorTo(t, w.open(sidT), sidT, "TRIPPLEDIP-p"), key('b'))
	br := w.branchOff(sidT)
	if n := u.m.Selected(); n == nil || n.SessionID != br {
		t.Fatalf("cursor on %+v, want the branch (status %q)", n, u.status)
	}
	for _, r := range u.m.Rows() {
		if r.Node.SessionID == sidT && r.Node.Node.ID == "TRIPPLEDIP-p" && !u.m.Folded[r.Node] {
			t.Fatalf("b unfolded the turn the branch came from:\n%s", strings.Join(screen(u), "\n"))
		}
	}
}

// After an edit the line has no reply of its own yet, so its header shows the
// edit's estimate, marked ~ — smaller than before for a drop — until the next
// reply brings a real number.
func TestScenarioAnEditedLineShowsAnEstimatedContextSize(t *testing.T) {
	w := newWorld(t)
	w.durations = true
	w.trunk(sidT, "t1", "t2", "t3")
	// Give t3's reply real-shaped usage by rewriting the fixture file's last reply.
	path := w.path(sidT)
	es, _, err := claude.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []byte
	for _, e := range es {
		if e.UUID() == "t3-r" {
			e.Raw["message"].(map[string]any)["usage"] = map[string]any{
				"input_tokens": 0, "cache_read_input_tokens": 90000, "cache_creation_input_tokens": 0, "output_tokens": 5}
		}
		b, err := claude.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		out = append(append(out, b...), '\n')
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}

	u := allOf(w.open(sidT))
	if !strings.Contains(shown(u), shortID(sidT)+" · 90k") {
		t.Fatalf("no real number before the edit:\n%s", shown(u))
	}
	u = drive(t, selectRange(t, u, sidT, "t2-p", "t2-r", 1), enter)
	v := w.replacement(sidT)
	h := ""
	for _, l := range screen(allOf(u)) {
		if strings.Contains(l, shortID(v)) {
			h, _, _ = strings.Cut(l, "\n")
		}
	}
	i := strings.Index(h, " · ~")
	if i < 0 {
		t.Fatalf("edited line's header has no ~ estimate: %q", h)
	}
	var k int
	if _, err := fmt.Sscanf(h[i+len(" · ~"):], "%dk", &k); err != nil || k <= 0 || k >= 90 {
		t.Fatalf("estimate %q is not a drop's smaller number", h[i:])
	}
}

// TestScenarioFoldedTurnsShowTheirSizes is task 2: every turn head row on
// screen carries a size, given real-shaped usage.
func TestScenarioFoldedTurnsShowTheirSizes(t *testing.T) {
	w := newWorld(t)
	w.durations = true
	w.trunk(sidT, "t1", "t2", "t3")
	path := w.path(sidT)
	es, _, err := claude.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	usage := map[string]int{"t1-r": 5000, "t2-r": 40000, "t3-r": 90000}
	var out []byte
	for _, e := range es {
		if n, ok := usage[e.UUID()]; ok {
			e.Raw["message"].(map[string]any)["usage"] = map[string]any{
				"input_tokens": 0, "cache_read_input_tokens": n, "cache_creation_input_tokens": 0, "output_tokens": 5}
		}
		b, err := claude.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		out = append(append(out, b...), '\n')
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}

	u := w.open(sidT)
	rows := asShown(u)
	for _, l := range rows {
		if strings.HasPrefix(l, shortID(sidT)) {
			continue // the session header, not a turn row
		}
		if !strings.Contains(l, "k") {
			t.Fatalf("a folded turn row has no size: %q\nall:\n%s", l, strings.Join(rows, "\n"))
		}
	}
}

// The stretch a native /compact summarised is one folded row (§3.4
// amendment): closed by default, → opens it muted one level in, ← on it
// closes it, and the divider note is not repeated while it is closed.
func TestScenarioTheCompactedStretchIsOneFoldedRow(t *testing.T) {
	w := newWorld(t)
	w.fixture(sidT, "compacted.jsonl")
	u := w.open(sidT)
	got := strings.Join(asShown(u), "\n")
	want := shortID(sidT) + "\n⋮ compacted by Claude Code · 2 turns · ~<1k\n▸ user: three  (1)  ~<1k\n▸ user: four  (1)  ~<1k"
	if got != want {
		t.Fatalf("closed:\n%s\nwant:\n%s", got, want)
	}
	if u.m.Cursor != 0 || u.m.Selected().Node.ID != "u1" {
		t.Fatalf("the group row stands on %+v, want u1", u.m.Selected())
	}

	u = drive(t, u, key('l'))
	rows := u.m.Rows()
	got = strings.Join(asShown(u), "\n")
	want = shortID(sidT) + "\n⋮ compacted by Claude Code · 2 turns · ~<1k\n  ▸ user: one  (1)  ~<1k\n  ▸ user: two  (1)  ~<1k\n" +
		"▸ user: three  (1)  ~<1k   ⋮ compacted by Claude Code — context starts here\n▸ user: four  (1)  ~<1k"
	if got != want {
		t.Fatalf("open:\n%s\nwant:\n%s", got, want)
	}
	for i, id := range []string{"u1", "u2", "u3"} {
		_, style := renderRow(rows[i], false, u.current, 0)
		if muted := style == StyleMuted; muted != (id != "u3") {
			t.Errorf("%s style %v: only the old turns are muted", id, style)
		}
	}
	if _, _, total := u.m.Window(3); total != 4 {
		t.Fatalf("open: %d rows, want 4", total)
	}
	// ⏎ on u1 opens its body, ← folds it back, ← again closes the group.
	u = drive(t, u, key('l'))
	if len(u.m.Rows()) != 5 {
		t.Fatalf("→ on the open group's first turn did not unfold it:\n%s", strings.Join(asShown(u), "\n"))
	}
	u = drive(t, u, key('h'), key('h'))
	if got := strings.Join(asShown(u), "\n"); !strings.HasPrefix(got, shortID(sidT)+"\n⋮ compacted by Claude Code · 2 turns · ~<1k\n▸ user: three") {
		t.Fatalf("← on the group row did not close it:\n%s", got)
	}
}

// A branch off a turn inside a closed group hangs under the group row.
func TestScenarioABranchOffACompactedTurnShowsWhileClosed(t *testing.T) {
	w := newWorld(t)
	w.fixture(sidT, "compacted.jsonl")
	br := w.branch(sidT, sidT, "u2")
	u := w.open(sidT)
	rows := u.m.Rows()
	if !rows[0].Group || !rows[0].GroupClosed {
		t.Fatalf("no closed group row:\n%s", strings.Join(asShown(u), "\n"))
	}
	found := false
	for _, r := range rows {
		if r.Node.SessionID == br {
			found = true
			if r.Depth != rows[0].Depth+2 {
				t.Errorf("branch row at depth %d, want %d", r.Depth, rows[0].Depth+2)
			}
		}
	}
	if !found {
		t.Fatalf("the branch is hidden by the closed group:\n%s", strings.Join(asShown(u), "\n"))
	}
	// The branch's copied prefix has no rows, so it gets no group of its own.
	n := 0
	for _, r := range u.m.Rows() {
		if r.Group {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d group rows, want 1", n)
	}
}

// ⏎ and b on the closed group row act on its first turn.
func TestScenarioBranchingFromTheClosedGroupRow(t *testing.T) {
	for _, how := range []string{"continue", "b"} {
		t.Run(how, func(t *testing.T) {
			w := newWorld(t)
			w.fixture(sidT, "compacted.jsonl")
			u := w.open(sidT)
			if !u.m.Rows()[0].Group {
				t.Fatal("cursor is not on the group row")
			}
			if how == "b" {
				u = drive(t, u, key('b'))
			} else {
				u = drive(t, u, enter, enter)
			}
			uuids, leaf := w.history(w.branchOff(sidT))
			if strings.Join(uuids, ",") != "u1,a1" || leaf != "a1" {
				t.Fatalf("branch holds %v leaf %s, want u1,a1 (status %q)", uuids, leaf, u.status)
			}
		})
	}
}

// A range from the group row reaches into it and is refused, unpaid.
func TestScenarioARangeIntoTheClosedGroupIsRefused(t *testing.T) {
	w := newWorld(t)
	w.fixture(sidT, "compacted.jsonl")
	u := w.open(sidT)
	u = drive(t, u, down, key('s'), tea.KeyMsg{Type: tea.KeyUp}, enter, enter) // u3 end, group row start, squash
	if u.confirm != "" || !strings.Contains(u.status, "before a /compact — already summarised by Claude Code") {
		t.Fatalf("confirm %q, status %q", u.confirm, u.status)
	}
	if w.summaries() != 0 {
		t.Fatal("a refused squash was paid for")
	}
}

// A reveal of a node inside a closed group opens it first.
func TestScenarioARevealIntoTheClosedGroupOpensIt(t *testing.T) {
	w := newWorld(t)
	w.fixture(sidT, "compacted.jsonl")
	u := w.open(sidT)
	u.m.Reveal(sidT, "a1")
	if n := u.m.Selected(); n == nil || n.Node.ID != "a1" {
		t.Fatalf("cursor on %+v, want a1:\n%s", n, strings.Join(asShown(u), "\n"))
	}
	if r := u.m.Rows()[0]; !r.Group || r.GroupClosed {
		t.Fatalf("the group did not open:\n%s", strings.Join(asShown(u), "\n"))
	}
}

// A line with no compact has no group row.
func TestScenarioALineWithoutACompactHasNoGroup(t *testing.T) {
	w := newWorld(t)
	w.trunk(sidT, "t1", "t2")
	for _, r := range w.open(sidT).m.Rows() {
		if r.Group || r.GroupClosed || r.Node.Compacted {
			t.Fatalf("group row on a plain line: %+v", r)
		}
	}
}

// m into or out of a closed group is refused like any edit before a
// /compact, and nothing is written.
func TestScenarioAMoveOntoOrFromTheClosedGroupIsRefused(t *testing.T) {
	up := tea.KeyMsg{Type: tea.KeyUp}
	for name, keys := range map[string][]tea.Msg{
		"u4 put down on the group row": {down, down, key('m'), up, up, enter},
		"the group row put down on u4": {key('m'), down, down, enter},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			w.fixture(sidT, "compacted.jsonl")
			before := w.storeFile()
			u := drive(t, w.open(sidT), keys...)
			if !strings.Contains(u.status, "before a /compact") {
				t.Fatalf("status %q, want the before-a-/compact refusal", u.status)
			}
			w.checkTranscripts(1)
			if w.storeFile() != before {
				t.Fatal("a refused move wrote the store")
			}
		})
	}
}
