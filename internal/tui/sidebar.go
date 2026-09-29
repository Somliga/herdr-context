package tui

import (
	"fmt"
	"math"
	"strings"

	"herdr-tree/internal/adapter"
)

// sidebarWidth is the sidebar's own width, not counting the "│" separator
// column beside it. sidebarMin is the pane width the sidebar needs before it
// is shown at all (§4).
const (
	sidebarWidth = 26
	sidebarMin   = 110
	// sidebarRows is the sidebar's height when everything is known: a
	// header line plus one bar per type (§4). The rows area must have at
	// least this many lines of budget, or the sidebar is not shown — a
	// shorter viewport would otherwise be padded up to this height,
	// pushing the counter, footer and status off screen.
	sidebarRows = 1 + len(adapter.Types)
)

// sidebarLines renders the context sidebar (§4): a "context" header line,
// then one bar per type in adapter.Types order, each at most sidebarWidth
// runes. total and est are the line's context number and whether it is an
// edit's estimate (tree.Node.SessionTokens / TokensEstimated); b is its
// breakdown by type. When nothing is known — every type's tokens are zero —
// only the header line is returned, reading "context —". With shares but
// total == 0, the header reads "context —" above the six bars.
func sidebarLines(b adapter.Breakdown, total int, est bool) []string {
	sum := 0
	for _, t := range b.Tokens {
		sum += t
	}
	if sum == 0 {
		return []string{"context —"}
	}
	prefix := " "
	if est {
		prefix = "~"
	}
	head := "context " + prefix + humanTokens(total)
	if total == 0 {
		head = "context —" // shares from bytes, but no number to scale them to
	}
	lines := []string{head}
	for i, label := range adapter.Types {
		share := float64(b.Tokens[i]) / float64(sum)
		cells := int(math.Round(8 * share))
		if cells < 0 {
			cells = 0
		}
		if cells > 8 {
			cells = 8
		}
		pct := int(math.Round(100 * share))
		bar := strings.Repeat("▮", cells) + strings.Repeat("▯", 8-cells)
		lines = append(lines, fmt.Sprintf("%-13s%s%3d%%", label, bar, pct))
	}
	return lines
}
