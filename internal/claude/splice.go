package claude

import (
	"errors"
	"strings"

	"herdr-tree/internal/adapter"
)

// ErrNothingLeft means a cut would remove every turn of the line.
var ErrNothingLeft = errors.New("a cut must leave at least one turn")

// A move is refused, nothing written, when it would put its turns back
// where they are, when it would carry the preamble (which is no turn), and
// when it is handed a seed: a move carries turns verbatim.
var (
	ErrMoveNowhere  = errors.New("that puts the turns back where they are")
	ErrMovePreamble = errors.New("only whole turns move, not what precedes the first prompt")
	ErrMoveSeed     = errors.New("a move carries turns, not a seed")
)

func parseForEdit(srcPath string) (*line, error) {
	es, skipped, err := ParseFile(srcPath)
	if err != nil {
		return nil, err
	}
	if skipped > 0 {
		return nil, ErrPartialTranscript
	}
	if err := checkVersion(es); err != nil {
		return nil, err
	}
	return buildLine(es)
}

// Widen reports what from..to covers once widened to whole turns.
func Widen(srcPath, from, to string) (adapter.Span, error) {
	l, err := parseForEdit(srcPath)
	if err != nil {
		return adapter.Span{}, err
	}
	return l.widen(from, to)
}

func (l *line) widen(from, to string) (adapter.Span, error) {
	a, b, err := l.span(from, to)
	if err != nil {
		return adapter.Span{}, err
	}
	return adapter.Span{First: a, Last: b, End: l.lastOf(b), EndNode: l.lastNodeOf(b), Turns: l.last}, nil
}

// WidenBranch is Widen for a branch at node (§2.5b): node's whole turn on the
// session's current line, or, when node is not on it — before a native
// /compact, or on a stretch the session rewound away from — its whole turn on
// the line node is on, ending at tipReaching. A branch only reads the file,
// so a turn the current line no longer holds is still somewhere to branch
// from; an edit, which rewrites the current line, refuses it. If no line
// through node can be found, the span is node itself.
func WidenBranch(srcPath, node string) (adapter.Span, error) {
	l, err := parseForEdit(srcPath)
	if err != nil {
		return adapter.Span{}, err
	}
	if _, on := l.turn[node]; !on {
		tip := tipReaching(l.es, node)
		if tip == "" {
			return adapter.Span{End: node, EndNode: node}, nil
		}
		if l, err = buildLineAt(l.es, tip); err != nil {
			return adapter.Span{}, err
		}
	}
	return l.widen(node, node)
}

// Splice writes a new session holding srcPath's current line with e applied:
// the widened range dropped, e.Seed (if any) in its place, and the first
// entry after the range re-parented onto the seed, or onto the last entry
// before the range. Entry uuids are kept, so branches and cut markers that
// name them still resolve. The source is never modified.
func Splice(srcPath string, e adapter.Edit, dstCWD string) (adapter.Spliced, error) {
	if e.Carry != nil {
		return move(srcPath, e, dstCWD)
	}
	if e.Seed != "" && !strings.HasPrefix(e.Seed, SummaryPrefix) && !strings.HasPrefix(e.Seed, CompactionPrefix) {
		return adapter.Spliced{}, ErrUnmarkedSeed
	}
	l, err := parseForEdit(srcPath)
	if err != nil {
		return adapter.Spliced{}, err
	}

	// a..b is the range of turns to drop. An insert is the empty range just
	// after the turn e.After belongs to.
	var a, b int
	if e.After != "" {
		t, ok := l.turn[e.After]
		if !ok {
			return adapter.Spliced{}, ErrNotOnLine
		}
		if e.Seed == "" {
			return adapter.Spliced{}, errors.New("an insert needs something to insert")
		}
		a, b = t+1, t
	} else if a, b, err = l.span(e.From, e.To); err != nil {
		return adapter.Spliced{}, err
	}
	if e.Seed == "" && a <= 1 && b >= l.last {
		return adapter.Spliced{}, ErrNothingLeft
	}

	var before, after string
	for _, u := range l.chain {
		switch t := l.turn[u]; {
		case t < a:
			before = u
		case t > b && after == "":
			after = u
		}
	}

	sid, err := newUUIDv4()
	if err != nil {
		return adapter.Spliced{}, err
	}
	joinTo := before
	var seedLine []byte
	var seedUUID string
	if e.Seed != "" {
		if seedUUID, err = newUUIDv4(); err != nil {
			return adapter.Spliced{}, err
		}
		if seedLine, err = Marshal(seedEntry(e.Seed, seedUUID, before, sid, dstCWD)); err != nil {
			return adapter.Spliced{}, err
		}
		joinTo = seedUUID
	}

	var buf []byte
	wroteSeed := false
	for _, en := range l.es {
		u := en.UUID()
		if u == "" || !l.keep[u] {
			continue // bookkeeping and everything off the line, as in GraftSeeded
		}
		if t := l.turn[u]; t >= a && t <= b {
			continue
		}
		m := rehome(en, sid, dstCWD)
		if u == after {
			if seedLine != nil {
				buf = append(append(buf, seedLine...), '\n')
				wroteSeed = true
			}
			if joinTo == "" {
				m["parentUuid"] = nil
			} else {
				m["parentUuid"] = joinTo
			}
		}
		enc, err := Marshal(Entry{Raw: m})
		if err != nil {
			return adapter.Spliced{}, err
		}
		buf = append(append(buf, enc...), '\n')
	}
	if seedLine != nil && !wroteSeed {
		buf = append(append(buf, seedLine...), '\n')
	}

	leaf := l.chain[len(l.chain)-1]
	if after == "" {
		leaf = joinTo
	}
	lp, err := Marshal(Entry{Raw: map[string]any{"type": "last-prompt", "leafUuid": leaf, "sessionId": sid}})
	if err != nil {
		return adapter.Spliced{}, err
	}
	buf = append(append(buf, lp...), '\n')

	if _, err := writeSession(dstCWD, sid, buf); err != nil {
		return adapter.Spliced{}, err
	}
	removed := b - max(a, 1) + 1
	if removed < 0 {
		removed = 0
	}
	return adapter.Spliced{SessionID: sid, Removed: removed, After: after, First: seedUUID}, nil
}

// move is Splice for an Edit with Carry (§2.8): the one turn of Carry's
// e.From is written verbatim after the turn of dstPath's e.After. When Carry
// is dstPath's own session the turn leaves its old place in the same pass;
// otherwise Carry's file is only read. A stretch is squashed first and moved
// as its one ⤶ turn.
//
// Within the line every id is kept: nothing is duplicated in one file, and
// branches and labels on the moved turn keep resolving. Into another line
// every id the turn carries is renewed, consistently within it — uuids,
// requestIds, message ids, tool_use ids and whatever names them — since the
// target may hold copies under the same ids, and a repeated requestId or
// message id would merge or mis-assign turns. The first moved entry hangs on
// the target turn's last entry, and the entry after the insertion on the last
// moved one. A ⤶ squashed turn moved into another line is relabelled.
func move(dstPath string, e adapter.Edit, dstCWD string) (adapter.Spliced, error) {
	if e.Seed != "" {
		return adapter.Spliced{}, ErrMoveSeed
	}
	l, err := parseForEdit(dstPath)
	if err != nil {
		return adapter.Spliced{}, err
	}
	same := sourcePath(*e.Carry) == dstPath
	from := l
	if !same {
		if from, err = parseForEdit(sourcePath(*e.Carry)); err != nil {
			return adapter.Spliced{}, err
		}
	}
	mt, okFrom := from.turn[e.From] // the moved turn
	at, okAt := l.turn[e.After]
	switch {
	case !okFrom || !okAt:
		return adapter.Spliced{}, ErrNotOnLine
	case mt == 0:
		return adapter.Spliced{}, ErrMovePreamble
	case same && (at == mt || at == mt-1):
		return adapter.Spliced{}, ErrMoveNowhere
	case !same && from.last == 1:
		return adapter.Spliced{}, ErrNothingLeft
	}
	carried := func(ln *line, u string) bool {
		return ln == from && ln.turn[u] == mt
	}
	orNull := func(u string) any {
		if u == "" {
			return nil
		}
		return u
	}

	sid, err := newUUIDv4()
	if err != nil {
		return adapter.Spliced{}, err
	}
	fresh := map[string]string{} // every id the moved turn carries, to its new one
	for _, en := range from.es {
		u := en.UUID()
		if u == "" || !from.keep[u] || !carried(from, u) {
			continue
		}
		for i, id := range append([]string{u}, carriedIDs(en)...) {
			switch {
			case id == "" || fresh[id] != "":
			case same:
				fresh[id] = id
			case i == 0:
				if fresh[id], err = newUUIDv4(); err != nil {
					return adapter.Spliced{}, err
				}
			default:
				if fresh[id], err = renewID(id); err != nil {
					return adapter.Spliced{}, err
				}
			}
		}
	}
	var block []byte
	for _, en := range from.es {
		u := en.UUID()
		if u == "" || !from.keep[u] || !carried(from, u) {
			continue
		}
		m := renamed(rehome(en, sid, dstCWD), fresh).(map[string]any)
		if u == from.firstOf(mt) {
			m["parentUuid"] = orNull(l.lastOf(at))
			if !same {
				relabel(m, e.Carry.ID)
			}
		}
		enc, err := Marshal(Entry{Raw: m})
		if err != nil {
			return adapter.Spliced{}, err
		}
		block = append(append(block, enc...), '\n')
	}
	first, last := fresh[from.firstOf(mt)], fresh[from.lastOf(mt)]

	// next is where the block goes: the first entry after the target turn.
	// gap, within the line only, is the first entry after where it was.
	var next, gap string
	for _, u := range l.chain {
		if carried(l, u) {
			continue
		}
		if t := l.turn[u]; t > at && next == "" {
			next = u
		}
		if t := l.turn[u]; same && t > mt && gap == "" {
			gap = u
		}
	}

	var buf []byte
	for _, en := range l.es {
		u := en.UUID()
		if u == "" || !l.keep[u] || carried(l, u) {
			continue
		}
		m := rehome(en, sid, dstCWD)
		switch u {
		case next:
			buf = append(buf, block...)
			m["parentUuid"] = last
		case gap:
			m["parentUuid"] = orNull(l.lastOf(mt - 1))
		}
		enc, err := Marshal(Entry{Raw: m})
		if err != nil {
			return adapter.Spliced{}, err
		}
		buf = append(append(buf, enc...), '\n')
	}

	leaf := l.chain[len(l.chain)-1]
	switch {
	case next == "":
		buf = append(buf, block...)
		leaf = last
	case same && gap == "":
		leaf = l.lastOf(mt - 1) // the line's last turn moved earlier
	}
	lp, err := Marshal(Entry{Raw: map[string]any{"type": "last-prompt", "leafUuid": leaf, "sessionId": sid}})
	if err != nil {
		return adapter.Spliced{}, err
	}
	buf = append(append(buf, lp...), '\n')
	if _, err := writeSession(dstCWD, sid, buf); err != nil {
		return adapter.Spliced{}, err
	}
	out := adapter.Spliced{SessionID: sid, Removed: 1, First: first}
	if same {
		out.After = gap
	}
	return out, nil
}

// relabel makes m, a moved turn's first entry, a ⤶ merged from src8 seed if
// it is a ⤶ squashed one (§2.8): it is knowledge arriving in another line,
// not a contraction of it. Only the prefix changes; m is already a copy.
func relabel(m map[string]any, src string) {
	msg, _ := m["message"].(map[string]any)
	blocks, _ := msg["content"].([]any)
	for _, b := range blocks {
		bm, _ := b.(map[string]any)
		if bm["type"] != "text" {
			continue
		}
		t, _ := bm["text"].(string)
		if !strings.HasPrefix(t, CompactionPrefix) {
			return // the text opens with its first block
		}
		bm["text"] = SummaryPrefix + " " + src[:min(8, len(src))] + strings.TrimPrefix(t, CompactionPrefix)
		if k, ok := m["herdrTree"].(map[string]any); ok {
			k["kind"] = "summary"
		}
		return
	}
}

// carriedIDs is every id other than its uuid that e carries and a copy must
// not share: its requestId, its message id and the id of every tool call
// block — tool_use, server_tool_use, mcp_tool_use, whatever *_tool_use comes
// next. renamed rewrites the *_tool_result blocks that name them.
func carriedIDs(e Entry) []string {
	ids := []string{e.RequestID()}
	msg, _ := e.Raw["message"].(map[string]any)
	if id, _ := msg["id"].(string); id != "" {
		ids = append(ids, id)
	}
	blocks, _ := msg["content"].([]any)
	for _, b := range blocks {
		bm, _ := b.(map[string]any)
		if typ, _ := bm["type"].(string); strings.HasSuffix(typ, "tool_use") {
			if id, _ := bm["id"].(string); id != "" {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// renewID is a fresh id shaped like id: its prefix up to the last "_"
// (toolu_, msg_, req_) kept, the rest random.
func renewID(id string) (string, error) {
	u, err := newUUIDv4()
	if err != nil {
		return "", err
	}
	return id[:strings.LastIndex(id, "_")+1] + strings.ReplaceAll(u, "-", ""), nil
}

// renamed is a deep copy of v with every string that is a key of to replaced
// by its value: a moved turn's ids wherever they appear — parentUuid, a
// tool_result's tool_use_id, toolUseResult, sourceToolAssistantUUID.
// ponytail: exact-match on every string; a free-text value equal to an id
// would be renamed too.
func renamed(v any, to map[string]string) any {
	switch x := v.(type) {
	case string:
		if n, ok := to[x]; ok {
			return n
		}
	case map[string]any:
		m := make(map[string]any, len(x))
		for k, e := range x {
			m[k] = renamed(e, to)
		}
		return m
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = renamed(e, to)
		}
		return out
	}
	return v
}
