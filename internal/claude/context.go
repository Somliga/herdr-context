package claude

import (
	"encoding/json"

	"herdr-tree/internal/adapter"
)

// outputTokens is an assistant entry's message.usage.output_tokens, 0 if
// absent. Decoded as json.Number like usageTokens, for the same reason.
func outputTokens(e Entry) int {
	msg, _ := e.Raw["message"].(map[string]any)
	if msg == nil {
		return 0
	}
	usage, ok := msg["usage"].(map[string]any)
	if !ok {
		return 0
	}
	if n, ok := usage["output_tokens"].(json.Number); ok {
		if f, err := n.Float64(); err == nil {
			return int(f)
		}
	}
	return 0
}

// block is one content block, keeping its raw value so it can be
// re-marshalled for a byte count exactly as it appeared on disk.
type block struct {
	typ string
	raw any
}

// contentBlocks splits message.content into blocks. A plain string (an
// older shape, and every user prompt) counts as one block of type "".
func contentBlocks(content any) []block {
	switch c := content.(type) {
	case string:
		return []block{{raw: c}}
	case []any:
		var out []block
		for _, b := range c {
			if m, ok := b.(map[string]any); ok {
				t, _ := m["type"].(string)
				out = append(out, block{typ: t, raw: m})
			} else {
				out = append(out, block{raw: b})
			}
		}
		return out
	}
	return nil
}

func blockBytes(b block) float64 {
	data, err := json.Marshal(b.raw)
	if err != nil {
		return 0
	}
	return float64(len(data))
}

// turnBytes is the marshalled size of a turn's entries' message content,
// the bytes/4 fallback's input.
func turnBytes(es []Entry) int {
	total := 0
	for _, e := range es {
		msg, ok := e.Raw["message"].(map[string]any)
		if !ok {
			continue
		}
		content, ok := msg["content"]
		if !ok {
			continue
		}
		b, err := json.Marshal(content)
		if err != nil {
			continue
		}
		total += len(b)
	}
	return total
}

// turnSizes sets TurnTokens/TurnEstimated on each turn's head node (§3.1 of
// the sidebar spec), in file order over the whole transcript — every turn,
// not only the current line. A head is an entry whose Entries node is
// KindHuman, KindSummaryImport or KindSummaryCompaction, or the first node,
// matching tree.Build's isHead.
func turnSizes(es []Entry, nodes []adapter.Node) {
	if len(nodes) == 0 {
		return
	}
	kind := make(map[string]adapter.Kind, len(nodes))
	byID := make(map[string]*adapter.Node, len(nodes))
	for i := range nodes {
		kind[nodes[i].ID] = nodes[i].Kind
		byID[nodes[i].ID] = &nodes[i]
	}
	firstNodeID := nodes[0].ID

	var filtered []Entry
	for _, e := range es {
		if !e.IsSidechain() {
			filtered = append(filtered, e)
		}
	}
	if len(filtered) == 0 {
		return
	}

	isHead := func(e Entry) bool {
		u := e.UUID()
		if u == "" {
			return false
		}
		if u == firstNodeID {
			return true
		}
		k, ok := kind[u]
		if !ok {
			return false // not a tree node at all: not a head
		}
		switch k {
		case adapter.KindHuman, adapter.KindSummaryImport, adapter.KindSummaryCompaction:
			return true
		}
		return false
	}

	type turn struct {
		headID     string
		start, end int // [start, end) into filtered
	}
	var turns []turn
	for i, e := range filtered {
		if isHead(e) {
			turns = append(turns, turn{headID: e.UUID(), start: i})
		}
	}
	if len(turns) == 0 {
		return
	}
	for i := range turns {
		if i+1 < len(turns) {
			turns[i].end = turns[i+1].start
		} else {
			turns[i].end = len(filtered)
		}
	}

	prevEnd := 0
	prevRPos := -1 // position of the last turn's usage-bearing reply; -1 = start of file
	prevHadReply := true
	for i, t := range turns {
		rPos, rCtx, rOut := -1, 0, 0
		for j := t.start; j < t.end; j++ {
			e := filtered[j]
			if e.Type() != "assistant" {
				continue
			}
			if n, ok := usageTokens(e); ok && n != 0 {
				rPos, rCtx, rOut = j, n, outputTokens(e)
			}
		}
		hasReply := rPos >= 0

		fallback := !hasReply || (i > 0 && !prevHadReply)
		if !fallback {
			for j := prevRPos + 1; j <= rPos; j++ {
				if filtered[j].IsCompactBoundary() {
					fallback = true
					break
				}
			}
		}

		var tokens int
		estimated := false
		if !fallback {
			growth := (rCtx + rOut) - prevEnd
			if growth <= 0 {
				fallback = true
			} else {
				tokens = growth
			}
		}
		if fallback {
			tokens = turnBytes(filtered[t.start:t.end]) / 4
			estimated = true
		}

		// prevEnd and prevRPos track the last real reply seen, whether or not
		// ITS OWN turn's size fell back: a turn with no usage-bearing reply of
		// its own (e.g. a squashed line's next turn, replied to before this
		// pass ever ran) forces the NEXT turn's growth to fall back too (the
		// prevHadReply rule above), but a turn whose reply exists and is
		// merely sandwiched between two fallbacks must not also poison the
		// turn after it — its own real end is still a valid baseline.
		if hasReply {
			prevEnd = rCtx + rOut
			prevRPos = rPos
		}
		prevHadReply = hasReply

		if n, ok := byID[t.headID]; ok {
			n.TurnTokens = tokens
			n.TurnEstimated = estimated
		}
	}
}

// lineKeep is the current line's kept uuids, buildLine's Select set, or,
// when the line cannot be built, every non-sidechain entry after the last
// compact_boundary.
func lineKeep(es []Entry) map[string]bool {
	if l, err := buildLine(es); err == nil {
		return l.keep
	}
	last := -1
	for i, e := range es {
		if e.IsCompactBoundary() && !e.IsSidechain() {
			last = i
		}
	}
	keep := map[string]bool{}
	for i := last + 1; i < len(es); i++ {
		e := es[i]
		if e.IsSidechain() {
			continue
		}
		if u := e.UUID(); u != "" {
			keep[u] = true
		}
	}
	return keep
}

// breakdown is the current line's context split by the six types (§3.2 of
// the sidebar spec), scaled to total when total > 0.
func breakdown(es []Entry, total int) adapter.Breakdown {
	keep := lineKeep(es)
	hasOrigin := HasHumanOrigin(es)

	var raw [6]float64 // thinking, tool calls, tool results, replies, typed, injected
	type usage struct {
		out     int
		visible float64
		set     bool
	}
	byMsgID := map[string]*usage{}

	seen := map[string]bool{}
	for _, e := range es {
		if e.IsSidechain() {
			continue
		}
		u := e.UUID()
		if u == "" || !keep[u] || seen[u] {
			continue
		}
		seen[u] = true
		msg, ok := e.Raw["message"].(map[string]any)
		if !ok {
			continue
		}
		switch e.Type() {
		case "assistant":
			var info *usage
			if id, _ := msg["id"].(string); id != "" {
				info = byMsgID[id]
				if info == nil {
					info = &usage{}
					byMsgID[id] = info
				}
				if !info.set {
					info.out = outputTokens(e)
					info.set = true
				}
			}
			for _, b := range contentBlocks(msg["content"]) {
				bytes := blockBytes(b)
				switch b.typ {
				case "thinking":
					// no direct bytes share: counted via output_tokens below
					continue
				case "tool_use":
					raw[1] += bytes / 4
				case "text":
					raw[3] += bytes / 4
				}
				if info != nil {
					info.visible += bytes / 4
				}
			}
		case "user":
			for _, b := range contentBlocks(msg["content"]) {
				bytes := blockBytes(b)
				if b.typ == "tool_result" {
					raw[2] += bytes / 4
					continue
				}
				if k, ok := Classify(e, hasOrigin); ok && k == adapter.KindHuman {
					raw[4] += bytes / 4
				} else {
					raw[5] += bytes / 4
				}
			}
		}
	}

	for _, info := range byMsgID {
		if think := float64(info.out) - info.visible; think > 0 {
			raw[0] += think
		}
	}

	var tokens [6]int
	if total > 0 {
		sum := raw[0] + raw[1] + raw[2] + raw[3] + raw[4] + raw[5]
		// ponytail: sum == 0 with total > 0 means a line with a context
		// number but no classifiable content at all, which no real Claude
		// Code transcript produces (every entry marshals to at least a few
		// bytes). Left as six zeros rather than an untested distribution
		// rule; revisit if a real transcript ever hits this.
		if sum > 0 {
			assigned := 0
			largest := 0
			for i, v := range raw {
				tokens[i] = int(v / sum * float64(total))
				assigned += tokens[i]
				if raw[i] > raw[largest] {
					largest = i
				}
			}
			tokens[largest] += total - assigned
		}
		return adapter.Breakdown{Tokens: tokens, Estimated: false}
	}
	for i, v := range raw {
		tokens[i] = int(v)
	}
	return adapter.Breakdown{Tokens: tokens, Estimated: true}
}
