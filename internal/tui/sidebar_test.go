package tui

import (
	"strings"
	"testing"

	"herdr-tree/internal/adapter"
)

func TestSidebarLines(t *testing.T) {
	b := adapter.Breakdown{Tokens: [6]int{26880, 21840, 21000, 5880, 5880, 2520}}
	got := sidebarLines(b, 84000, false)
	want := []string{
		"context  84k",
		"thinking     ▮▮▮▯▯▯▯▯ 32%",
		"tool calls   ▮▮▯▯▯▯▯▯ 26%",
		"tool results ▮▮▯▯▯▯▯▯ 25%",
		"replies      ▮▯▯▯▯▯▯▯  7%",
		"typed        ▮▯▯▯▯▯▯▯  7%",
		"injected     ▯▯▯▯▯▯▯▯  3%",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, l := range got {
		if n := len([]rune(l)); n > sidebarWidth {
			t.Fatalf("%q is %d wide", l, n)
		}
	}
}

func TestSidebarWithAnEstimateAndWithNothing(t *testing.T) {
	b := adapter.Breakdown{Tokens: [6]int{0, 100, 300, 0, 0, 0}, Estimated: true}
	if got := sidebarLines(b, 31000, true)[0]; got != "context ~31k" {
		t.Fatalf("%q", got)
	}
	if got := sidebarLines(adapter.Breakdown{Estimated: true}, 0, false); got[0] != "context —" || len(got) != 1 {
		t.Fatalf("nothing known: %q", got)
	}
}
