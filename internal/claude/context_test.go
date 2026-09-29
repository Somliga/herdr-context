package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"herdr-tree/internal/adapter"
)

// lines parses JSONL strings through ParseFile on a temp file, so numbers
// decode as json.Number exactly as in production.
func lines(t *testing.T, raw ...string) []Entry {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(raw, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	es, skipped, err := ParseFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if skipped > 0 {
		t.Fatalf("%d malformed lines", skipped)
	}
	return es
}

// jline builds one JSONL line from a map, so fixtures never hand-escape
// strings.
func jline(t *testing.T, v map[string]any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func usage(ctx, out int) map[string]any {
	return map[string]any{
		"input_tokens":                0,
		"cache_read_input_tokens":     ctx,
		"cache_creation_input_tokens": 0,
		"output_tokens":               out,
	}
}

// twoTurns is the shared fixture: turn 1 is p1 -> a1 (thinking + text,
// signature-only thinking) -> a1t (a split tool-call sharing a1's message id
// and usage) -> r1 (its tool result) -> a2 (the turn's real reply) -> d1 (the
// turn_duration entry Claude Code writes at the end of a turn). Turn 2 is p2
// -> a3.
func twoTurns(t *testing.T) []Entry {
	sig := strings.Repeat("s", 400)
	txt := strings.Repeat("t", 40)
	res := strings.Repeat("r", 800)

	l1 := jline(t, map[string]any{
		"type": "user", "uuid": "p1", "parentUuid": nil,
		"sessionId": "S", "cwd": "/r", "version": "2.1.278",
		"timestamp": "2026-01-01T10:00:00Z",
		"origin":    map[string]any{"kind": "human"},
		"message":   map[string]any{"role": "user", "content": "first"},
	})
	l2 := jline(t, map[string]any{
		"type": "assistant", "uuid": "a1", "parentUuid": "p1",
		"sessionId": "S", "cwd": "/r", "version": "2.1.278",
		"timestamp": "2026-01-01T10:00:01Z", "requestId": "q1",
		"message": map[string]any{
			"role": "assistant", "id": "m1",
			"content": []any{
				map[string]any{"type": "thinking", "thinking": "", "signature": sig},
				map[string]any{"type": "text", "text": txt},
			},
			"usage": usage(1000, 300),
		},
	})
	l3 := jline(t, map[string]any{
		"type": "assistant", "uuid": "a1t", "parentUuid": "a1",
		"sessionId": "S", "cwd": "/r", "version": "2.1.278",
		"timestamp": "2026-01-01T10:00:02Z", "requestId": "q1",
		"message": map[string]any{
			"role": "assistant", "id": "m1",
			"content": []any{
				map[string]any{"type": "tool_use", "id": "tu1", "name": "Bash", "input": map[string]any{"command": "ls"}},
			},
			"usage": usage(1000, 300),
		},
	})
	l4 := jline(t, map[string]any{
		"type": "user", "uuid": "r1", "parentUuid": "a1t",
		"sessionId": "S", "cwd": "/r", "version": "2.1.278",
		"timestamp": "2026-01-01T10:00:03Z",
		"message": map[string]any{
			"role": "user",
			"content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": "tu1", "content": res},
			},
		},
		"toolUseResult": map[string]any{"stdout": res},
	})
	l5 := jline(t, map[string]any{
		"type": "assistant", "uuid": "a2", "parentUuid": "r1",
		"sessionId": "S", "cwd": "/r", "version": "2.1.278",
		"timestamp": "2026-01-01T10:00:04Z", "requestId": "q2",
		"message": map[string]any{
			"role": "assistant", "id": "m2",
			"content": []any{map[string]any{"type": "text", "text": "reply two"}},
			"usage":   usage(1600, 50),
		},
	})
	l6 := jline(t, map[string]any{
		"type": "system", "subtype": "turn_duration", "uuid": "d1", "parentUuid": "a2",
		"sessionId": "S", "cwd": "/r", "version": "2.1.278",
		"timestamp": "2026-01-01T10:00:05Z", "durationMs": 1234,
	})
	l7 := jline(t, map[string]any{
		"type": "user", "uuid": "p2", "parentUuid": "d1",
		"sessionId": "S", "cwd": "/r", "version": "2.1.278",
		"timestamp": "2026-01-01T10:00:06Z",
		"origin":    map[string]any{"kind": "human"},
		"message":   map[string]any{"role": "user", "content": "second"},
	})
	l8 := jline(t, map[string]any{
		"type": "assistant", "uuid": "a3", "parentUuid": "p2",
		"sessionId": "S", "cwd": "/r", "version": "2.1.278",
		"timestamp": "2026-01-01T10:00:07Z", "requestId": "q3",
		"message": map[string]any{
			"role": "assistant", "id": "m3",
			"content": []any{map[string]any{"type": "text", "text": "reply three"}},
			"usage":   usage(2100, 40),
		},
	})
	return lines(t, l1, l2, l3, l4, l5, l6, l7, l8)
}

// twoTurnsWithoutUsage is twoTurns with every "usage" object removed.
func twoTurnsWithoutUsage(t *testing.T) []Entry {
	es := twoTurns(t)
	var out []Entry
	for _, e := range es {
		if msg, ok := e.Raw["message"].(map[string]any); ok {
			delete(msg, "usage")
		}
		out = append(out, e)
	}
	return out
}

// duplicateOfA1t is another physical copy of twoTurns' a1t entry: same
// uuid, same message id and usage. A real transcript never repeats a uuid;
// this simulates the artifact and checks it changes nothing.
func duplicateOfA1t(t *testing.T) Entry {
	l := jline(t, map[string]any{
		"type": "assistant", "uuid": "a1t", "parentUuid": "a1",
		"sessionId": "S", "cwd": "/r", "version": "2.1.278",
		"timestamp": "2026-01-01T10:00:02Z", "requestId": "q1",
		"message": map[string]any{
			"role": "assistant", "id": "m1",
			"content": []any{
				map[string]any{"type": "tool_use", "id": "tu1", "name": "Bash", "input": map[string]any{"command": "ls"}},
			},
			"usage": usage(1000, 300),
		},
	})
	es := lines(t, l)
	return es[0]
}

// compactedMidTurn: turn 1's reply ctx 90000, then a native compact_boundary
// mid-turn (no new prompt), then its continuation reply ctx 8000; turn 2 has
// its own prompt and a reply ctx 9000. Turn 1's real end would need to reach
// across the boundary, which invalidates it: bytes, ~.
func compactedMidTurn(t *testing.T) []Entry {
	l1 := jline(t, map[string]any{
		"type": "user", "uuid": "p1", "parentUuid": nil,
		"sessionId": "S", "cwd": "/r", "version": "2.1.278",
		"timestamp": "2026-01-01T11:00:00Z",
		"origin":    map[string]any{"kind": "human"},
		"message":   map[string]any{"role": "user", "content": "one"},
	})
	l2 := jline(t, map[string]any{
		"type": "assistant", "uuid": "a1a", "parentUuid": "p1",
		"sessionId": "S", "cwd": "/r", "version": "2.1.278",
		"timestamp": "2026-01-01T11:00:01Z", "requestId": "qa",
		"message": map[string]any{
			"role": "assistant", "id": "ma",
			"content": []any{map[string]any{"type": "text", "text": "reply pre-compact"}},
			"usage":   usage(90000, 500),
		},
	})
	l3 := jline(t, map[string]any{
		"type": "system", "subtype": "compact_boundary", "uuid": "cb",
		"parentUuid": nil, "logicalParentUuid": "a1a",
		"sessionId": "S", "cwd": "/r", "version": "2.1.278",
		"timestamp": "2026-01-01T11:00:02Z",
		"content":   "Conversation compacted", "level": "info", "isMeta": false,
		"compactMetadata": map[string]any{"trigger": "auto", "preTokens": 90000},
	})
	l4 := jline(t, map[string]any{
		"type": "user", "uuid": "cs", "parentUuid": "cb",
		"sessionId": "S", "cwd": "/r", "version": "2.1.278",
		"timestamp":        "2026-01-01T11:00:03Z",
		"isCompactSummary": true, "isVisibleInTranscriptOnly": true,
		"message": map[string]any{"role": "user", "content": "This session is being continued from a previous conversation that ran out of context. Summary: turn one."},
	})
	l5 := jline(t, map[string]any{
		"type": "assistant", "uuid": "a1b", "parentUuid": "cs",
		"sessionId": "S", "cwd": "/r", "version": "2.1.278",
		"timestamp": "2026-01-01T11:00:04Z", "requestId": "qb",
		"message": map[string]any{
			"role": "assistant", "id": "mb",
			"content": []any{map[string]any{"type": "text", "text": "continuation reply"}},
			"usage":   usage(8000, 60),
		},
	})
	l6 := jline(t, map[string]any{
		"type": "user", "uuid": "p2", "parentUuid": "a1b",
		"sessionId": "S", "cwd": "/r", "version": "2.1.278",
		"timestamp": "2026-01-01T11:00:05Z",
		"origin":    map[string]any{"kind": "human"},
		"message":   map[string]any{"role": "user", "content": "two"},
	})
	l7 := jline(t, map[string]any{
		"type": "assistant", "uuid": "a3", "parentUuid": "p2",
		"sessionId": "S", "cwd": "/r", "version": "2.1.278",
		"timestamp": "2026-01-01T11:00:06Z", "requestId": "qc",
		"message": map[string]any{
			"role": "assistant", "id": "mc",
			"content": []any{map[string]any{"type": "text", "text": "reply three"}},
			"usage":   usage(9000, 70),
		},
	})
	return lines(t, l1, l2, l3, l4, l5, l6, l7)
}

// rewound: a prompt p1, its reply a1 (a tool call and its result), then a
// second prompt p1b whose parent is p1's own parent — the user rewound
// before replying to p1. The tip's line holds p1b alone; a1's tool result
// must not count.
func rewound(t *testing.T) []Entry {
	l1 := jline(t, map[string]any{
		"type": "user", "uuid": "p1", "parentUuid": nil,
		"sessionId": "S", "cwd": "/r", "version": "2.1.278",
		"timestamp": "2026-01-01T12:00:00Z",
		"origin":    map[string]any{"kind": "human"},
		"message":   map[string]any{"role": "user", "content": "first prompt"},
	})
	l2 := jline(t, map[string]any{
		"type": "assistant", "uuid": "a1", "parentUuid": "p1",
		"sessionId": "S", "cwd": "/r", "version": "2.1.278",
		"timestamp": "2026-01-01T12:00:01Z", "requestId": "qx",
		"message": map[string]any{
			"role": "assistant",
			"content": []any{
				map[string]any{"type": "tool_use", "id": "tux", "name": "Bash", "input": map[string]any{"command": "ls"}},
			},
		},
	})
	l3 := jline(t, map[string]any{
		"type": "user", "uuid": "r1", "parentUuid": "a1",
		"sessionId": "S", "cwd": "/r", "version": "2.1.278",
		"timestamp": "2026-01-01T12:00:02Z",
		"message": map[string]any{
			"role": "user",
			"content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": "tux", "content": "abandoned branch output"},
			},
		},
		"toolUseResult": map[string]any{"stdout": "abandoned branch output"},
	})
	l4 := jline(t, map[string]any{
		"type": "user", "uuid": "p1b", "parentUuid": nil,
		"sessionId": "S", "cwd": "/r", "version": "2.1.278",
		"timestamp": "2026-01-01T12:00:03Z",
		"origin":    map[string]any{"kind": "human"},
		"message":   map[string]any{"role": "user", "content": "rewound prompt"},
	})
	return lines(t, l1, l2, l3, l4)
}

func TestTurnSizesAreRealGrowth(t *testing.T) {
	es := twoTurns(t)
	nodes := Entries(es)
	turnSizes(es, nodes)
	got := map[string]adapter.Node{}
	for _, n := range nodes {
		got[n.ID] = n
	}
	// end(turn) = its last usage-bearing reply's ctx + output_tokens.
	// turn 1 ends at a2: 1600 + 50 = 1650; turn 2 at a3: 2100 + 40 = 2140.
	if n := got["p1"]; n.TurnTokens != 1650 || n.TurnEstimated {
		t.Fatalf("turn 1 = %d est %v, want 1650 real", n.TurnTokens, n.TurnEstimated)
	}
	if n := got["p2"]; n.TurnTokens != 490 || n.TurnEstimated {
		t.Fatalf("turn 2 = %d est %v, want 2140 - 1650 = 490 real", n.TurnTokens, n.TurnEstimated)
	}
	if got["a1"].TurnTokens != 0 {
		t.Fatal("a body node carries a turn size")
	}
}

func TestTurnSizesFallBackToBytesWithoutUsage(t *testing.T) {
	es := twoTurnsWithoutUsage(t)
	nodes := Entries(es)
	turnSizes(es, nodes)
	for _, n := range nodes {
		if n.ID == "p1" && (!n.TurnEstimated || n.TurnTokens <= 0) {
			t.Fatalf("p1 = %d est %v, want a positive ~ from bytes", n.TurnTokens, n.TurnEstimated)
		}
	}
}

func TestTurnSizesFallBackAcrossACompactBoundary(t *testing.T) {
	es := compactedMidTurn(t)
	nodes := Entries(es)
	turnSizes(es, nodes)
	for _, n := range nodes {
		if n.ID == "p1" && (!n.TurnEstimated || n.TurnTokens <= 0 || n.TurnTokens > 90000) {
			t.Fatalf("p1 = %d est %v, want a positive bytes estimate", n.TurnTokens, n.TurnEstimated)
		}
	}
}

// clampCase is one reply whose visible text alone outweighs its
// output_tokens, so an unclamped thinking share would go negative.
func clampCase(t *testing.T) []Entry {
	l1 := jline(t, map[string]any{
		"type": "user", "uuid": "cp", "parentUuid": nil,
		"sessionId": "S", "cwd": "/r", "version": "2.1.278",
		"timestamp": "2026-01-01T13:00:00Z",
		"origin":    map[string]any{"kind": "human"},
		"message":   map[string]any{"role": "user", "content": "go"},
	})
	l2 := jline(t, map[string]any{
		"type": "assistant", "uuid": "ca", "parentUuid": "cp",
		"sessionId": "S", "cwd": "/r", "version": "2.1.278",
		"timestamp": "2026-01-01T13:00:01Z", "requestId": "qz",
		"message": map[string]any{
			"role": "assistant", "id": "mz",
			"content": []any{map[string]any{"type": "text", "text": strings.Repeat("z", 200)}},
			"usage":   usage(500, 1),
		},
	})
	return lines(t, l1, l2)
}

func TestBreakdownClampsThinkingToZero(t *testing.T) {
	es := clampCase(t)
	b := breakdown(es, 0)
	for i, v := range b.Tokens {
		if v < 0 {
			t.Fatalf("type %d is negative: %d, want the thinking clamp to floor it at 0", i, v)
		}
	}
}

func TestBreakdownSplitsTheLineAndSumsToItsNumber(t *testing.T) {
	es := twoTurns(t)
	b := breakdown(es, 2100)
	sum := 0
	for _, v := range b.Tokens {
		sum += v
	}
	if sum < 2095 || sum > 2105 || b.Estimated {
		t.Fatalf("sum %d est %v, want ~2100 real", sum, b.Estimated)
	}
	for i, v := range b.Tokens {
		if v < 0 {
			t.Fatalf("type %d is negative: %d", i, v)
		}
	}
	// thinking: per reply, output - visible bytes/4, once per message id:
	// m1: 300 - (40 text + tool_use block bytes)/4 > 0.
	if b.Tokens[0] == 0 {
		t.Fatal("no thinking share despite output_tokens beyond the visible blocks")
	}
}

// withEntry inserts e right after the entry whose uuid is at, so a fixture
// can gain a duplicate without moving the tip (which tipOf always finds at
// the end of file order).
func withEntry(es []Entry, at string, e Entry) []Entry {
	out := make([]Entry, 0, len(es)+1)
	for _, x := range es {
		out = append(out, x)
		if x.UUID() == at {
			out = append(out, e)
		}
	}
	return out
}

func TestBreakdownCountsASplitReplysThinkingOnce(t *testing.T) {
	es := twoTurns(t)
	once := breakdown(es, 0).Tokens[0]
	doubled := breakdown(withEntry(es, "a1t", duplicateOfA1t(t)), 0).Tokens[0]
	if doubled != once {
		t.Fatalf("thinking %d with another split entry, %d without: counted per entry", doubled, once)
	}
}

func TestBreakdownWithoutANumberIsEstimated(t *testing.T) {
	b := breakdown(twoTurnsWithoutUsage(t), 0)
	if !b.Estimated || b.Tokens[2] == 0 {
		t.Fatalf("got %+v, want estimated shares with tool results", b)
	}
}

func TestBreakdownIgnoresEverythingOffTheCurrentLine(t *testing.T) {
	es := rewound(t)
	if b := breakdown(es, 0); b.Tokens[2] != 0 {
		t.Fatalf("tool results %d from a rewound stretch", b.Tokens[2])
	}
}
