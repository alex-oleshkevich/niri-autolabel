package main

import "testing"

func ptr(i int) *int { return &i }

func TestModelReducer(t *testing.T) {
	m := NewModel()

	m.Apply(Event{WorkspacesChanged: &struct {
		Workspaces []Workspace `json:"workspaces"`
	}{Workspaces: []Workspace{{ID: 1, Idx: 1}, {ID: 2, Idx: 2}}}})

	m.Apply(Event{WindowsChanged: &struct {
		Windows []Window `json:"windows"`
	}{Windows: []Window{
		{ID: 10, AppID: "slack", Title: "general", WorkspaceID: ptr(1)},
		{ID: 11, AppID: "google-chrome", Title: "Gmail", WorkspaceID: ptr(1)},
		{ID: 12, AppID: "ghostty", Title: "vim", WorkspaceID: ptr(2)},
	}}})

	if got := len(m.WindowsIn(1)); got != 2 {
		t.Fatalf("workspace 1 windows = %d, want 2", got)
	}
	if got := len(m.WindowsIn(2)); got != 1 {
		t.Fatalf("workspace 2 windows = %d, want 1", got)
	}

	// Closing a window updates membership.
	m.Apply(Event{WindowClosed: &struct {
		ID int `json:"id"`
	}{ID: 12}})
	if got := len(m.WindowsIn(2)); got != 0 {
		t.Fatalf("workspace 2 after close = %d, want 0", got)
	}

	// Opening/moving a window upserts it.
	m.Apply(Event{WindowOpenedOrChanged: &struct {
		Window Window `json:"window"`
	}{Window: Window{ID: 13, AppID: "code", Title: "main.go", WorkspaceID: ptr(2)}}})
	if got := len(m.WindowsIn(2)); got != 1 {
		t.Fatalf("workspace 2 after open = %d, want 1", got)
	}
}

func TestSignatureStability(t *testing.T) {
	build := func(order []Window) string {
		m := NewModel()
		m.Apply(Event{WindowsChanged: &struct {
			Windows []Window `json:"windows"`
		}{Windows: order}})
		return m.Signature(1)
	}

	a := build([]Window{
		{ID: 1, AppID: "slack", Title: "general", WorkspaceID: ptr(1)},
		{ID: 2, AppID: "chrome", Title: "Gmail", WorkspaceID: ptr(1)},
	})
	b := build([]Window{
		{ID: 9, AppID: "chrome", Title: "Gmail", WorkspaceID: ptr(1)},
		{ID: 8, AppID: "slack", Title: "general", WorkspaceID: ptr(1)},
	})
	if a != b {
		t.Fatalf("signature should be order/id independent: %s != %s", a, b)
	}

	// Meaningful title changes MUST change the signature (keep labels current).
	c := build([]Window{
		{ID: 1, AppID: "slack", Title: "general", WorkspaceID: ptr(1)},
		{ID: 2, AppID: "chrome", Title: "GitHub", WorkspaceID: ptr(1)},
	})
	if a == c {
		t.Fatal("a real title change should change the signature")
	}

	// Spinner/status glyph churn must NOT change the signature.
	e := build([]Window{
		{ID: 1, AppID: "slack", Title: "general", WorkspaceID: ptr(1)},
		{ID: 2, AppID: "chrome", Title: "✳ Gmail", WorkspaceID: ptr(1)},
	})
	f := build([]Window{
		{ID: 1, AppID: "slack", Title: "general", WorkspaceID: ptr(1)},
		{ID: 2, AppID: "chrome", Title: "⠂ Gmail", WorkspaceID: ptr(1)},
	})
	if e != f {
		t.Fatal("leading spinner glyph changes should not affect signature")
	}

	// App membership changes MUST change the signature.
	d := build([]Window{
		{ID: 1, AppID: "slack", Title: "general", WorkspaceID: ptr(1)},
		{ID: 2, AppID: "code", Title: "main.go", WorkspaceID: ptr(1)},
	})
	if a == d {
		t.Fatal("app membership change should change signature")
	}
}
