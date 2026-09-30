package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"herdr-context/internal/store"
)

// undoCmd takes back the latest edit of sid's line (spec §2): the group's
// records are marked undone, so the versions they replaced are current
// again. It writes only the store.
func undoCmd(st *store.Store, sid string, live LiveFunc) tea.Cmd {
	return func() tea.Msg {
		cur := st.Current(sid)
		rec := st.Branches[cur]
		if rec.Replaces == "" {
			return actionDoneMsg{status: "nothing to undo on " + shortID(cur)}
		}
		return toggle(st, st.Group(cur), true, live, "undone "+editWord(rec)+" on ", rec.Replaces)
	}
}

// redoCmd puts back the most recent undone edit of sid's line.
func redoCmd(st *store.Store, sid string, live LiveFunc) tea.Cmd {
	return func() tea.Msg {
		cur := st.Current(sid)
		next := st.Branches[cur].ReplacedBy
		if next == "" || !st.Branches[next].Undone {
			return actionDoneMsg{status: "nothing to redo on " + shortID(cur)}
		}
		// Every line of the edit must still lead to it: one whose version
		// was edited again since has its own redo path (§2.2).
		group := st.Group(next)
		for _, id := range group {
			if st.Branches[st.Branches[id].Replaces].ReplacedBy != id {
				return actionDoneMsg{status: "nothing to redo on " + shortID(cur)}
			}
		}
		return toggle(st, group, false, live, "redone "+editWord(st.Branches[next])+" on ", next)
	}
}

// editWord is the bare word for the edit that wrote b, as §2.5's status has
// it. A move's drop from its source is the move.
func editWord(b store.Branch) string {
	switch {
	case b.Kind == store.KindCompacted:
		return "squash"
	case b.Kind == store.KindInserted:
		return "merge"
	case b.Kind == store.KindMoved, b.Cut != nil && b.Cut.To != "":
		return "move"
	}
	return "drop"
}

// toggle flips Undone on every record of one edit, after checking each of
// their lines: current at the version the edit expects, unchanged on disk,
// and no busy pane on any version of it.
func toggle(st *store.Store, group []string, undo bool, live LiveFunc, word, tip string) tea.Msg {
	for _, id := range group {
		// For undo, id is its line's current version; for redo, the one it
		// replaced is.
		expect := id
		if !undo {
			expect = st.Branches[id].Replaces
		}
		if line := st.Current(expect); line != expect {
			return actionDoneMsg{status: shortID(line) + " was edited since — undo that first"}
		}
		if stale := changedElsewhere(st, expect); stale != "" {
			return actionDoneMsg{status: stale}
		}
		if live == nil {
			continue
		}
		for _, v := range st.Lineage(expect) {
			pane, status, err := live(v)
			if err != nil {
				return actionDoneMsg{status: "cannot tell whether this session is open: " + err.Error() + " — nothing was written"}
			}
			if busy(status) {
				return actionDoneMsg{status: busyStatus("", v, pane, status)}
			}
		}
	}
	now := time.Now().UTC()
	was := map[string]store.Branch{}
	for _, id := range group {
		b := st.Branches[id]
		was[id] = b
		b.Undone, b.UndoneAt = undo, now
		st.Branches[id] = b
	}
	if err := st.Save(); err != nil {
		// Put it back: a later save, of a label say, must not write it.
		for id, b := range was {
			st.Branches[id] = b
		}
		return actionDoneMsg{status: "not saved: " + err.Error()}
	}
	return actionDoneMsg{status: word + shortID(tip) + continueThere, reload: true, tip: tip}
}
