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

// Apply updates the model and reports whether anything that can affect a label
// changed. niri emits WindowOpenedOrChanged for focus/size/position updates too,
// so most events leave the label-relevant projection (app id, normalized title,
// workspace membership, workspace names) untouched and return false.
func (m *Model) Apply(e Event) (changed bool) {
	switch {
	case e.WorkspacesChanged != nil:
		next := make(map[int]Workspace, len(e.WorkspacesChanged.Workspaces))
		for _, ws := range e.WorkspacesChanged.Workspaces {
			next[ws.ID] = ws
		}
		changed = !sameWorkspaceProjection(m.workspaces, next)
		m.workspaces = next
	case e.WindowsChanged != nil:
		next := make(map[int]Window, len(e.WindowsChanged.Windows))
		for _, w := range e.WindowsChanged.Windows {
			next[w.ID] = w
		}
		changed = !sameWindowProjection(m.windows, next)
		m.windows = next
	case e.WindowOpenedOrChanged != nil:
		w := e.WindowOpenedOrChanged.Window
		old, existed := m.windows[w.ID]
		m.windows[w.ID] = w
		changed = !existed || windowKey(old) != windowKey(w)
	case e.WindowClosed != nil:
		_, existed := m.windows[e.WindowClosed.ID]
		delete(m.windows, e.WindowClosed.ID)
		changed = existed
	}
	return changed
}

// windowKey projects a window to just the fields that influence a label.
type winKey struct {
	app   string
	title string
	ws    int
}

func windowKey(w Window) winKey {
	ws := 0
	if w.WorkspaceID != nil {
		ws = *w.WorkspaceID
	}
	return winKey{app: w.AppID, title: normalizeTitle(w.Title), ws: ws}
}

func sameWindowProjection(a, b map[int]Window) bool {
	if len(a) != len(b) {
		return false
	}
	for id, wa := range a {
		wb, ok := b[id]
		if !ok || windowKey(wa) != windowKey(wb) {
			return false
		}
	}
	return true
}

func sameWorkspaceProjection(a, b map[int]Workspace) bool {
	if len(a) != len(b) {
		return false
	}
	for id, wa := range a {
		wb, ok := b[id]
		if !ok || nameOf(wa) != nameOf(wb) {
			return false
		}
	}
	return true
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
