package tui

import (
	"fmt"
	"sort"

	"herdr-context/internal/tree"
)

// rangeHint is the status after s fixes a range's end.
const rangeHint = "range end fixed — move to its start, then s or ⏎ (esc cancels)"

// heavyCount is how many turns ] and [ step through, largest first.
const heavyCount = 10

// summaryGuess is the squash preview's stand-in for a summary not yet made.
// ponytail: a flat guess; the review after summarising shows the real one.
const summaryGuess = 1000

// heavyTurns is the session you are in's largest turns, largest first: every
// head with a size, not in the compacted stretch (out of the context) and
// not a branch's copy of its parent's turn.
func (u uiModel) heavyTurns() []*tree.Node {
	target := u.current
	if u.st != nil {
		target = u.st.Current(u.current)
	}
	var out []*tree.Node
	seen := map[*tree.Node]bool{}
	var walk func([]*tree.Node)
	walk = func(ns []*tree.Node) {
		for _, n := range ns {
			if seen[n] {
				continue
			}
			seen[n] = true
			if n.SessionID == target && n.IsHead && !n.Compacted && !n.Superseded && n.Node.TurnTokens > 0 {
				out = append(out, n)
			}
			walk(n.Children)
		}
	}
	walk(u.m.Roots)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Node.TurnTokens > out[j].Node.TurnTokens })
	if len(out) > heavyCount {
		out = out[:heavyCount]
	}
	return out
}

// jumpHeavy moves the cursor to the next (step 1) or previous (step -1)
// heavy turn, wrapping, and says which one it is.
func (u *uiModel) jumpHeavy(step int) {
	hs := u.heavyTurns()
	if len(hs) == 0 {
		u.status = "no turn sizes in this session"
		return
	}
	i := 0 // the first ] lands on the largest
	switch {
	case u.heavyAt == 0 && step < 0:
		i = len(hs) - 1
	case u.heavyAt > 0:
		i = ((u.heavyAt-1+step)%len(hs) + len(hs)) % len(hs)
	}
	u.heavyAt = i + 1
	n := hs[i]
	u.m.reveal(n)
	u.status = fmt.Sprintf("heavy %d/%d · %s", u.heavyAt, len(hs), sized(n.Node.TurnTokens, n.Node.TurnEstimated))
}

// rangeStats is what the selected range holds: its whole turns, their summed
// size, whether any size is estimated, and the range's session's number.
type rangeStats struct {
	turns, size, total   int
	est, totalEst, known bool
	// blocked, when set, is why the range cannot be edited as it stands —
	// the preview says that instead of adding it up.
	blocked [2]string
}

func (u uiModel) rangeStats() rangeStats {
	var rs rangeStats
	from, _, ok := u.m.RangeSpan()
	if !ok {
		return rs
	}
	heads := map[*tree.Node]bool{}
	for _, r := range u.m.Rows() {
		if !r.InRange {
			continue
		}
		switch {
		case r.Node.SessionID != from.SessionID:
			rs.blocked = [2]string{"crosses sessions:", "keep it in one session"}
		case r.Node.Compacted:
			rs.blocked = [2]string{"before a /compact:", "already summarised"}
		}
		// A range may start on a body row; its turn counts whole (Widen).
		h := r.Node
		for !h.IsHead {
			p := u.m.parent[h]
			if p == nil || p.SessionID != h.SessionID {
				break
			}
			h = p
		}
		if heads[h] {
			continue
		}
		heads[h] = true
		rs.turns++
		rs.size += h.Node.TurnTokens
		rs.est = rs.est || h.Node.TurnEstimated || h.Node.TurnTokens == 0
	}
	rs.total, rs.totalEst, rs.known = from.SessionTokens, from.TokensEstimated, true
	return rs
}

// sized is n formatted, ~ first when estimated.
func sized(n int, est bool) string {
	if est {
		return "~" + humanTokens(n)
	}
	return humanTokens(n)
}

func turnsWord(n int) string {
	if n == 1 {
		return "1 turn"
	}
	return fmt.Sprintf("%d turns", n)
}

// after is the context left once the range is gone and add put in.
func (rs rangeStats) after(add int) int {
	if n := rs.total - rs.size + add; n > 0 {
		return n
	}
	return 0
}

// rangeLines is the sidebar while a range is selected: the range, its share,
// and the context after a drop or a squash. Without the line's number only
// the range's own size is known.
func rangeLines(rs rangeStats) []string {
	if rs.blocked[0] != "" {
		return []string{"range", "  " + rs.blocked[0], "  " + rs.blocked[1]}
	}
	lines := []string{"range  " + turnsWord(rs.turns) + " · " + sized(rs.size, rs.est)}
	if rs.total == 0 {
		return lines
	}
	approx := rs.est || rs.totalEst
	lines = append(lines,
		fmt.Sprintf("       %d%% of %s", pct(rs.size, rs.total), sized(rs.total, rs.totalEst)),
		"drop   → "+sized(rs.after(0), approx),
		"squash → ~"+humanTokens(rs.after(summaryGuess)))
	return lines
}

// rangeLine is rangeLines on one row, for a pane too narrow for the sidebar.
func rangeLine(rs rangeStats) string {
	if rs.blocked[0] != "" {
		return "range " + rs.blocked[0] + " " + rs.blocked[1]
	}
	s := "range " + turnsWord(rs.turns) + " · " + sized(rs.size, rs.est)
	if rs.total == 0 {
		return s
	}
	approx := rs.est || rs.totalEst
	return fmt.Sprintf("%s (%d%%) · drop → %s · squash → ~%s",
		s, pct(rs.size, rs.total), sized(rs.after(0), approx), humanTokens(rs.after(summaryGuess)))
}

func pct(a, b int) int {
	if b == 0 {
		return 0
	}
	return int(float64(a)*100/float64(b) + 0.5)
}
