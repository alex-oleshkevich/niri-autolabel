package main

import (
	"context"
	"strconv"
	"testing"
	"time"
)

type mockNiri struct {
	workspaces []Workspace
	setCalls   [][2]string
	unsetCalls []string
}

func (m *mockNiri) ListWorkspaces(context.Context) ([]Workspace, error) { return m.workspaces, nil }
func (m *mockNiri) ListWindows(context.Context) ([]Window, error)       { return nil, nil }

func (m *mockNiri) SetName(_ context.Context, ref, name string) error {
	m.setCalls = append(m.setCalls, [2]string{ref, name})
	// Simulate niri applying the name so verify-by-id can find it.
	for i := range m.workspaces {
		ws := &m.workspaces[i]
		if ref == strconv.Itoa(ws.Idx) || (ws.Name != nil && *ws.Name == ref) {
			n := name
			ws.Name = &n
			break
		}
	}
	return nil
}

func (m *mockNiri) UnsetName(_ context.Context, ref string) error {
	m.unsetCalls = append(m.unsetCalls, ref)
	return nil
}

type mockLabeler struct {
	out      string
	called   int
	gotAvoid []string
}

func (l *mockLabeler) Generate(_ context.Context, _ []Window, avoid []string) (string, error) {
	l.called++
	l.gotAvoid = avoid
	return l.out, nil
}

func workspacesEvent(wss ...Workspace) Event {
	return Event{WorkspacesChanged: &struct {
		Workspaces []Workspace `json:"workspaces"`
	}{Workspaces: wss}}
}

func windowsEvent(wins ...Window) Event {
	return Event{WindowsChanged: &struct {
		Windows []Window `json:"windows"`
	}{Windows: wins}}
}

func newTestEngine(niri Niri, lab Labeler) *Engine {
	return NewEngine(niri, lab, NewState(), NopLogger(), time.Millisecond, time.Second, 1)
}

func TestApplyLabelUnnamedUsesIdxAndVerifies(t *testing.T) {
	niri := &mockNiri{workspaces: []Workspace{{ID: 1, Idx: 3}}}
	e := newTestEngine(niri, &mockLabeler{})
	e.model.Apply(workspacesEvent(Workspace{ID: 1, Idx: 3}))
	e.model.Apply(windowsEvent(Window{ID: 9, AppID: "code", Title: "main.go", WorkspaceID: ptr(1)}))

	if err := e.applyLabel(context.Background(), 1, "code", "sig1"); err != nil {
		t.Fatalf("applyLabel: %v", err)
	}
	if len(niri.setCalls) != 1 || niri.setCalls[0] != [2]string{"3", "code"} {
		t.Fatalf("expected SetName by idx 3, got %v", niri.setCalls)
	}
	if st, _ := e.state.Get(1); st.Label != "code" {
		t.Fatalf("state not committed: %+v", st)
	}
	if e.acted[1] != "sig1" {
		t.Fatalf("acted not recorded: %q", e.acted[1])
	}
}

func TestApplyLabelWrongTargetIsReverted(t *testing.T) {
	// idx 1 exists on two outputs; niri applies the name to the other one (id 2).
	niri := &mockNiri{workspaces: []Workspace{{ID: 2, Idx: 1}, {ID: 1, Idx: 1}}}
	e := newTestEngine(niri, &mockLabeler{})
	e.model.Apply(workspacesEvent(Workspace{ID: 1, Idx: 1}))
	e.model.Apply(windowsEvent(Window{ID: 9, AppID: "code", Title: "x", WorkspaceID: ptr(1)}))

	err := e.applyLabel(context.Background(), 1, "code", "sig1")
	if err == nil {
		t.Fatal("expected error when idx resolves to wrong workspace")
	}
	if len(niri.unsetCalls) != 1 || niri.unsetCalls[0] != "code" {
		t.Fatalf("expected the misapplied name to be unset, got %v", niri.unsetCalls)
	}
	if _, ok := e.state.Get(1); ok {
		t.Fatal("state should not be committed on wrong target")
	}
}

func TestForeignNameIsNeverTouched(t *testing.T) {
	niri := &mockNiri{}
	lab := &mockLabeler{out: "code"}
	e := newTestEngine(niri, lab)
	name := "mine"
	e.model.Apply(workspacesEvent(Workspace{ID: 1, Idx: 1, Name: &name}))
	e.model.Apply(windowsEvent(Window{ID: 9, AppID: "code", Title: "x", WorkspaceID: ptr(1)}))

	e.onFire(context.Background(), 1)

	if len(niri.setCalls) != 0 || e.inflight[1] {
		t.Fatalf("foreign workspace was touched: set=%v inflight=%v", niri.setCalls, e.inflight[1])
	}
}

func TestEmptyOwnedWorkspaceIsCleared(t *testing.T) {
	name := "code"
	niri := &mockNiri{workspaces: []Workspace{{ID: 1, Idx: 1, Name: &name}}}
	e := newTestEngine(niri, &mockLabeler{})
	e.model.Apply(workspacesEvent(Workspace{ID: 1, Idx: 1, Name: &name}))
	e.model.Apply(windowsEvent()) // no windows
	e.state.Set(1, "code", "sig1")
	e.acted[1] = "sig1"

	e.onFire(context.Background(), 1)

	if len(niri.unsetCalls) != 1 || niri.unsetCalls[0] != "code" {
		t.Fatalf("expected unset by name, got %v", niri.unsetCalls)
	}
	if _, ok := e.state.Get(1); ok {
		t.Fatal("state should be deleted after unset")
	}
}

func TestDebounceFiresWithinMaxWaitUnderChurn(t *testing.T) {
	e := newTestEngine(&mockNiri{}, &mockLabeler{})
	e.debounce = 50 * time.Millisecond
	e.maxWait = 80 * time.Millisecond
	e.model.Apply(workspacesEvent(Workspace{ID: 1, Idx: 1}))

	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	timeout := time.After(400 * time.Millisecond)
	i := 0

	for {
		select {
		case <-e.fireCh:
			return // fired within maxWait despite never-settling titles — success
		case <-timeout:
			t.Fatal("debounce never fired: continuous title churn starved it past maxWait")
		case <-tick.C:
			// A window whose title changes every tick (faster than the debounce),
			// so without a maxWait cap the timer would reset forever.
			i++
			e.model.Apply(Event{WindowOpenedOrChanged: &struct {
				Window Window `json:"window"`
			}{Window: Window{ID: 9, AppID: "ghostty", Title: "frame " + strconv.Itoa(i), WorkspaceID: ptr(1)}}})
			e.evaluateAll()
		}
	}
}

func TestClearOwnedLabelsOnShutdown(t *testing.T) {
	niri := &mockNiri{}
	e := newTestEngine(niri, &mockLabeler{})
	e.state.Set(7, "apiserver", "sig1")
	e.state.Set(9, "gmail", "sig2")

	e.clearOwnedLabels()

	if len(niri.unsetCalls) != 2 {
		t.Fatalf("expected 2 unset calls on shutdown, got %v", niri.unsetCalls)
	}
	got := map[string]bool{}
	for _, c := range niri.unsetCalls {
		got[c] = true
	}
	if !got["apiserver"] || !got["gmail"] {
		t.Fatalf("expected owned labels cleared, got %v", niri.unsetCalls)
	}
}

func TestAvoidListPassedToLabeler(t *testing.T) {
	other := "gmail"
	niri := &mockNiri{}
	lab := &mockLabeler{out: "apiserver"}
	e := newTestEngine(niri, lab)
	e.model.Apply(workspacesEvent(
		Workspace{ID: 1, Idx: 1},
		Workspace{ID: 2, Idx: 2, Name: &other},
	))
	e.model.Apply(windowsEvent(Window{ID: 9, AppID: "ghostty", Title: "x", WorkspaceID: ptr(1)}))

	e.onFire(context.Background(), 1)

	// onFire dispatches the label request in a goroutine; wait for it.
	deadline := time.Now().Add(time.Second)
	for lab.called == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(lab.gotAvoid) != 1 || lab.gotAvoid[0] != "gmail" {
		t.Fatalf("expected avoid=[gmail], got %v", lab.gotAvoid)
	}
}

func TestOwnedWorkspaceRelabelsByName(t *testing.T) {
	name := "old"
	niri := &mockNiri{workspaces: []Workspace{{ID: 1, Idx: 1, Name: &name}}}
	e := newTestEngine(niri, &mockLabeler{})
	e.model.Apply(workspacesEvent(Workspace{ID: 1, Idx: 1, Name: &name}))
	e.model.Apply(windowsEvent(Window{ID: 9, AppID: "code", Title: "x", WorkspaceID: ptr(1)}))
	e.state.Set(1, "old", "sig0") // we own "old"

	if err := e.applyLabel(context.Background(), 1, "newlabel", "sig1"); err != nil {
		t.Fatalf("applyLabel: %v", err)
	}
	if len(niri.setCalls) != 1 || niri.setCalls[0] != [2]string{"old", "newlabel"} {
		t.Fatalf("expected relabel by name ref 'old', got %v", niri.setCalls)
	}
}
