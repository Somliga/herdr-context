package claude

import (
	"encoding/json"
	"errors"

	"herdr-context/internal/adapter"
)

// ErrNotOnLine means a selected entry is not on the line that ends at the
// session's tip: it sits on a stretch the session already rewound away from,
// so editing it would change nothing the agent reads.
var ErrNotOnLine = errors.New("that entry is not on this session's current line")

// errBeforeCompact is ErrNotOnLine for an entry the line's native /compact
// already summarised: it is in the file but not in the context.
type errBeforeCompact struct{}

func (errBeforeCompact) Error() string {
	return "that turn is before a /compact — already summarised by Claude Code, not in the context"
}
func (errBeforeCompact) Is(target error) bool { return target == ErrNotOnLine }

// notOn is why u is not on the line: in the file before the /compact the
// line restarts at (the chain's root is then its compact_boundary), or simply
// on another stretch.
func (l *line) notOn(u string) error {
	if len(l.chain) == 0 {
		return ErrNotOnLine
	}
	root := l.chain[0]
	compacted := false
	for _, e := range l.es {
		if e.UUID() == root {
			compacted = e.IsCompactBoundary()
		}
	}
	if !compacted {
		return ErrNotOnLine
	}
	for _, e := range l.es {
		switch e.UUID() {
		case root:
			return ErrNotOnLine // u, if anywhere, comes after the line's start
		case u:
			return errBeforeCompact{}
		}
	}
	return ErrNotOnLine
}

// line is a transcript's current line — what the agent reads on resume —
// divided into turns. A turn runs from something the user typed (or an entry
// we injected) up to, not including, the next one. Turn 0 is the preamble:
// whatever precedes the first prompt.
//
// Whole turns are the only safe unit to remove. Measured over 112 local
// transcripts, all 9146 tool_use/tool_result pairs lie inside one turn, and
// the only uuid reference crossing a boundary is each prompt's parentUuid.
type line struct {
	es    []Entry
	keep  map[string]bool // Select at the tip
	chain []string        // the parentUuid chain to the tip, root first
	turn  map[string]int  // every kept entry's turn
	last  int             // the highest turn number
}

// tipOf is where the session resumes: the last user or assistant entry of the
// main conversation, in file order. Sidechains are a subagent's own
// conversation; system and attachment entries are written after the reply
// they follow and parented to it, so taking one as the tip is harmless for
// system entries but an attachment parented to an earlier prompt would drop
// the reply entirely.
func tipOf(es []Entry) string {
	for i := len(es) - 1; i >= 0; i-- {
		e := es[i]
		if e.UUID() == "" || e.IsSidechain() {
			continue
		}
		if t := e.Type(); t == "user" || t == "assistant" {
			return e.UUID()
		}
	}
	return ""
}

// opensTurn reports whether e starts a turn.
func opensTurn(e Entry, hasOrigin bool) bool {
	k, keep := Classify(e, hasOrigin)
	return keep && (k == adapter.KindHuman || k == adapter.KindSummaryImport || k == adapter.KindSummaryCompaction)
}

func buildLine(es []Entry) (*line, error) { return buildLineAt(es, tipOf(es)) }

// contextTokens is what the line ending at tipOf(es) currently reads (§3.1):
// the last assistant entry on the tip's chain that carries message.usage,
// summed as input + cache_read + cache_creation tokens. 0 if the chain has
// none — a new branch, a squashed line not yet replied to, or an older
// transcript format. Split entries of one reply share one usage; walking
// back from the tip and stopping at the first one found counts it once.
func contextTokens(es []Entry) int {
	tip := tipOf(es)
	if tip == "" {
		return 0
	}
	byUUID := make(map[string]Entry, len(es))
	for _, e := range es {
		if u := e.UUID(); u != "" {
			byUUID[u] = e
		}
	}
	for cur := tip; cur != ""; {
		e, ok := byUUID[cur]
		if !ok {
			break
		}
		if e.Type() == "assistant" {
			// A synthetic reply after an interrupt carries usage that sums
			// to 0: not a real reply, so keep walking back past it.
			if n, ok := usageTokens(e); ok && n != 0 {
				return n
			}
		}
		cur = e.ParentUUID()
	}
	return 0
}

// usageTokens is an assistant entry's message.usage, summed, if it has one.
// A transcript is decoded with json.Decoder.UseNumber (large ints must not
// become float64), so each field is a json.Number, not a float64.
func usageTokens(e Entry) (int, bool) {
	msg, _ := e.Raw["message"].(map[string]any)
	if msg == nil {
		return 0, false
	}
	usage, ok := msg["usage"].(map[string]any)
	if !ok {
		return 0, false
	}
	sum := 0.0
	for _, k := range []string{"input_tokens", "cache_read_input_tokens", "cache_creation_input_tokens"} {
		if n, ok := usage[k].(json.Number); ok {
			if f, err := n.Float64(); err == nil {
				sum += f
			}
		}
	}
	return int(sum), true
}

// tipReaching is the latest entry, in file order, that tipOf could take as a
// tip and whose ancestor chain holds node: the end of the line node is on,
// which for an entry before a native /compact or on a rewound stretch is not
// the session's own tip. "" if there is none.
func tipReaching(es []Entry, node string) string {
	byUUID := make(map[string]Entry, len(es))
	for _, e := range es {
		if u := e.UUID(); u != "" {
			byUUID[u] = e
		}
	}
	// reaches memoises every entry walked, so the scan is linear; an entry
	// is marked false while it is being walked, which also stops a cycle.
	reaches := map[string]bool{}
	walk := func(from string) bool {
		var path []string
		found := false
		for cur := from; cur != ""; cur = byUUID[cur].ParentUUID() {
			if cur == node {
				found = true
				break
			}
			if v, seen := reaches[cur]; seen {
				found = v
				break
			}
			if _, ok := byUUID[cur]; !ok {
				break
			}
			reaches[cur] = false
			path = append(path, cur)
		}
		for _, u := range path {
			reaches[u] = found
		}
		return found
	}
	for i := len(es) - 1; i >= 0; i-- {
		e := es[i]
		if e.UUID() == "" || e.IsSidechain() {
			continue
		}
		if t := e.Type(); (t == "user" || t == "assistant") && walk(e.UUID()) {
			return e.UUID()
		}
	}
	return ""
}

// buildLineAt is buildLine for the line that ends at tip.
func buildLineAt(es []Entry, tip string) (*line, error) {
	if tip == "" {
		return nil, ErrNodeNotFound
	}
	keep, err := Select(es, tip)
	if err != nil {
		return nil, err
	}
	byUUID := make(map[string]Entry, len(es))
	for _, e := range es {
		if u := e.UUID(); u != "" {
			byUUID[u] = e
		}
	}
	var chain []string
	seen := map[string]bool{}
	for cur := tip; cur != "" && keep[cur] && !seen[cur]; cur = byUUID[cur].ParentUUID() {
		seen[cur] = true
		chain = append(chain, cur)
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}

	l := &line{es: es, keep: keep, chain: chain, turn: map[string]int{}}
	hasOrigin := HasHumanOrigin(es)
	t := 0
	for _, u := range chain {
		if opensTurn(byUUID[u], hasOrigin) {
			t++
		}
		l.turn[u] = t
	}
	l.last = t

	// What Select kept off the chain belongs to the turn of whatever kept it:
	// an assistant block to its requestId's turn (rule 2), an attachment or a
	// tool result to its parent's (rules 3 and 4). Iterated because a
	// recovered tool result can have attachment children of its own.
	reqTurn := map[string]int{}
	for _, u := range chain {
		if r := byUUID[u].RequestID(); r != "" {
			if _, ok := reqTurn[r]; !ok {
				reqTurn[r] = l.turn[u]
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for _, e := range es {
			u := e.UUID()
			if u == "" || !keep[u] {
				continue
			}
			if _, done := l.turn[u]; done {
				continue
			}
			if tn, ok := reqTurn[e.RequestID()]; ok && e.RequestID() != "" {
				l.turn[u] = tn
				changed = true
			} else if tn, ok := l.turn[e.ParentUUID()]; ok {
				l.turn[u] = tn
				changed = true
			}
		}
	}
	return l, nil
}

// span widens from..to, given in either order, to whole turns.
func (l *line) span(from, to string) (first, last int, err error) {
	a, okA := l.turn[from]
	b, okB := l.turn[to]
	if !okA {
		return 0, 0, l.notOn(from)
	}
	if !okB {
		return 0, 0, l.notOn(to)
	}
	if a > b {
		a, b = b, a
	}
	return a, b, nil
}

// firstOf is the chain entry that opens turn t; lastOf is its last chain entry.
func (l *line) firstOf(t int) string {
	for _, u := range l.chain {
		if l.turn[u] == t {
			return u
		}
	}
	return ""
}

// lastNodeOf is turn t's last entry that Entries makes a node of, in the
// same file order Entries walks.
func (l *line) lastNodeOf(t int) string {
	hasOrigin := HasHumanOrigin(l.es)
	out := ""
	for _, e := range l.es {
		u := e.UUID()
		if tn, ok := l.turn[u]; !ok || tn != t || !l.keep[u] {
			continue
		}
		if _, node := Classify(e, hasOrigin); node {
			out = u
		}
	}
	return out
}

func (l *line) lastOf(t int) string {
	out := ""
	for _, u := range l.chain {
		if l.turn[u] == t {
			out = u
		}
	}
	return out
}
