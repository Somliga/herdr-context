package claude

import (
	"bytes"
	"encoding/json"

	"herdr-context/internal/adapter"
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
// re-marshalled for a byte count.
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

// jsonLen is v's marshalled size without HTML escaping, so < > & count one
// byte each as Claude Code writes them, not six as json.Marshal's \u003c.
// Key order and spacing may still differ from the disk line.
func jsonLen(v any) int {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return 0
	}
	return buf.Len() - 1 // Encode's trailing newline
}

func blockBytes(b block) float64 {
	return float64(jsonLen(b.raw))
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
		if content, ok := msg["content"]; ok {
			total += jsonLen(content)
		}
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

	// ends[i] is turn i's end: its last usage-bearing reply's context plus
	// output, and where that reply sits. The output is the largest over the
	// reply's message.id — a streaming split entry may carry a partial count.
	maxOut := map[string]int{}
	for _, e := range filtered {
		if id := messageID(e); id != "" {
			if n := outputTokens(e); n > maxOut[id] {
				maxOut[id] = n
			}
		}
	}
	type end struct {
		pos, tokens int // pos -1: no usage-bearing reply
	}
	ends := make([]end, len(turns))
	turnOf := make(map[string]int, len(filtered))
	byUUID := make(map[string]Entry, len(filtered))
	for i, t := range turns {
		ends[i] = end{pos: -1}
		for j := t.start; j < t.end; j++ {
			e := filtered[j]
			if u := e.UUID(); u != "" {
				turnOf[u] = i
				byUUID[u] = e
			}
			if e.Type() != "assistant" {
				continue
			}
			if n, ok := usageTokens(e); ok && n != 0 {
				out := outputTokens(e)
				if m := maxOut[messageID(e)]; m > out {
					out = m
				}
				ends[i] = end{pos: j, tokens: n + out}
			}
		}
	}

	for i, t := range turns {
		own := ends[i]
		fallback := own.pos < 0
		for j := t.start; !fallback && j <= own.pos; j++ {
			fallback = filtered[j].IsCompactBoundary()
		}

		// The turn grew the line its head continues: walk the head's
		// parentUuid chain back to the nearest entry of another turn, and
		// on within that turn to its reply, taking that turn's end. A
		// compact_boundary on the way invalidates it; a chain that runs out
		// first starts from nothing.
		base := 0
		baseTurn := -1
		for cur := filtered[t.start].ParentUUID(); !fallback && cur != ""; {
			e, ok := byUUID[cur]
			if !ok {
				break
			}
			if e.IsCompactBoundary() {
				fallback = true
				break
			}
			k := turnOf[cur]
			if k != i && baseTurn < 0 {
				baseTurn = k
				if ends[k].pos < 0 {
					fallback = true // no end there to grow from
					break
				}
				base = ends[k].tokens
			}
			if baseTurn >= 0 && (k != baseTurn || cur == filtered[ends[baseTurn].pos].UUID()) {
				break
			}
			cur = e.ParentUUID()
		}

		var tokens int
		estimated := false
		if !fallback {
			if growth := own.tokens - base; growth > 0 {
				tokens = growth
			} else {
				fallback = true
			}
		}
		if fallback {
			tokens = turnBytes(filtered[t.start:t.end]) / 4
			estimated = true
		}
		if n, ok := byID[t.headID]; ok {
			n.TurnTokens = tokens
			n.TurnEstimated = estimated
		}
	}
}

// messageID is an entry's message.id, "" if it has none.
func messageID(e Entry) string {
	msg, _ := e.Raw["message"].(map[string]any)
	id, _ := msg["id"].(string)
	return id
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
		out      int // largest output_tokens over the id's entries
		visible  float64
		sigBytes int
		hasUsage bool
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
				if _, ok := msg["usage"].(map[string]any); ok {
					info.hasUsage = true
				}
				if n := outputTokens(e); n > info.out {
					info.out = n
				}
			}
			for _, b := range contentBlocks(msg["content"]) {
				if b.typ == "thinking" {
					// no direct bytes share: counted via output_tokens below,
					// or via its signature when the id has no usage at all
					if info != nil {
						sig, _ := b.raw.(map[string]any)["signature"].(string)
						info.sigBytes += len(sig)
					}
					continue
				}
				sz := blockBytes(b)
				switch b.typ {
				case "tool_use":
					raw[1] += sz / 4
				case "text":
					raw[3] += sz / 4
				}
				if info != nil {
					info.visible += sz / 4
				}
			}
		case "user":
			for _, b := range contentBlocks(msg["content"]) {
				sz := blockBytes(b)
				if b.typ == "tool_result" {
					raw[2] += sz / 4
					continue
				}
				if k, ok := Classify(e, hasOrigin); ok && k == adapter.KindHuman {
					raw[4] += sz / 4
				} else {
					raw[5] += sz / 4
				}
			}
		}
	}

	for _, info := range byMsgID {
		if !info.hasUsage {
			// ponytail: a spliced line has no usage (stripUsage), so the
			// signature stands in: 0.5 tokens per signature byte, the
			// measured 0.4–0.6 on 2026-09-29. Upgrade: a per-model fit.
			raw[0] += float64(info.sigBytes) * 0.5
			continue
		}
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
