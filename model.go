package main

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"unicode"
)

// Model is a reducer over niri events. niri's *Changed events carry the full
// current list, so applying them is a replace; the single-window events upsert
// or delete. The model never calls niri — it only tracks observed state.
type Model struct {
	workspaces map[int]Workspace
	windows    map[int]Window
}

func NewModel() *Model {
	return &Model{
		workspaces: map[int]Workspace{},
		windows:    map[int]Window{},
	}
}

func (m *Model) Apply(e Event) {
	switch {
	case e.WorkspacesChanged != nil:
		m.workspaces = map[int]Workspace{}
		for _, ws := range e.WorkspacesChanged.Workspaces {
			m.workspaces[ws.ID] = ws
		}
	case e.WindowsChanged != nil:
		m.windows = map[int]Window{}
		for _, w := range e.WindowsChanged.Windows {
			m.windows[w.ID] = w
		}
	case e.WindowOpenedOrChanged != nil:
		w := e.WindowOpenedOrChanged.Window
		m.windows[w.ID] = w
	case e.WindowClosed != nil:
		delete(m.windows, e.WindowClosed.ID)
	}
}

func (m *Model) Workspace(id int) (Workspace, bool) {
	ws, ok := m.workspaces[id]
	return ws, ok
}

// SetLocalName updates the cached workspace name so ownership checks stay
// consistent between the niri call and the next WorkspacesChanged event.
func (m *Model) SetLocalName(id int, name *string) {
	ws, ok := m.workspaces[id]
	if !ok {
		return
	}
	ws.Name = name
	m.workspaces[id] = ws
}

// WorkspaceIDs returns the known workspace ids in a stable order.
func (m *Model) WorkspaceIDs() []int {
	ids := make([]int, 0, len(m.workspaces))
	for id := range m.workspaces {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

// WindowsIn returns the workspace's windows ordered by id for stable output.
func (m *Model) WindowsIn(wsID int) []Window {
	var out []Window
	for _, w := range m.windows {
		if w.WorkspaceID != nil && *w.WorkspaceID == wsID {
			out = append(out, w)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Signature is a stable, order-independent fingerprint of a workspace's apps and
// (normalized) window titles. Titles are included so the label tracks what the
// windows actually show; normalizeTitle strips volatile spinner/status glyphs so
// pure animation does not churn the signature. Identical signatures mean the
// label can be reused without calling the model.
func (m *Model) Signature(wsID int) string {
	windows := m.WindowsIn(wsID)
	entries := make([]string, 0, len(windows))
	for _, w := range windows {
		entries = append(entries, w.AppID+"\x00"+normalizeTitle(w.Title))
	}
	sort.Strings(entries)

	h := sha256.New()
	for _, e := range entries {
		h.Write([]byte(e))
		h.Write([]byte("\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// normalizeTitle strips leading non-alphanumeric runes (terminal spinner and
// status glyphs like "⠂" or "✳") and collapses whitespace, so frame-by-frame
// animation does not change the signature while real title text still does.
func normalizeTitle(s string) string {
	s = strings.TrimLeftFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	return strings.Join(strings.Fields(s), " ")
}
