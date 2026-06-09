package main

import (
	"context"
	"fmt"
	"time"
)

type Engine struct {
	niri     Niri
	labeler  Labeler
	state    *State
	logger   Logger
	debounce time.Duration
	maxWait  time.Duration

	model    *Model
	timers   map[int]*time.Timer
	inflight map[int]bool
	// acted[wsID] is the content signature we have successfully labelled for.
	acted map[int]string
	// armed[wsID] is the target signature the debounce timer is waiting on, so
	// unrelated event churn does not keep resetting it.
	armed map[int]string
	// pendingSince[wsID] marks when the current debounce episode began, so
	// continuous title churn still fires within maxWait instead of never.
	pendingSince map[int]time.Time

	fireCh   chan int
	resultCh chan jobResult
	sem      chan struct{}
}

type jobResult struct {
	wsID      int
	signature string
	raw       string
	err       error
}

func NewEngine(niri Niri, labeler Labeler, state *State, logger Logger, debounce, maxWait time.Duration, workers int) *Engine {
	if workers < 1 {
		workers = 1
	}
	if maxWait < debounce {
		maxWait = debounce
	}
	return &Engine{
		niri:         niri,
		labeler:      labeler,
		state:        state,
		logger:       logger,
		debounce:     debounce,
		maxWait:      maxWait,
		model:        NewModel(),
		timers:       map[int]*time.Timer{},
		inflight:     map[int]bool{},
		acted:        map[int]string{},
		armed:        map[int]string{},
		pendingSince: map[int]time.Time{},
		fireCh:       make(chan int, 64),
		resultCh:     make(chan jobResult, 64),
		sem:          make(chan struct{}, workers),
	}
}

// Run consumes the niri event stream until ctx is cancelled. The stream's
// initial WorkspacesChanged/WindowsChanged events resync the model on every
// (re)connect, so no separate snapshot query is needed.
func (e *Engine) Run(ctx context.Context) error {
	events := make(chan Event, 64)
	go e.feedEvents(ctx, events)

	for {
		select {
		case <-ctx.Done():
			e.clearOwnedLabels()
			return nil
		case ev := <-events:
			e.logger.Debug("event", "kind", ev.kind())
			e.model.Apply(ev)
			e.evaluateAll()
		case wsID := <-e.fireCh:
			e.onFire(ctx, wsID)
		case res := <-e.resultCh:
			e.onResult(ctx, res)
		}
	}
}

func (e *Engine) feedEvents(ctx context.Context, out chan Event) {
	backoff := time.Second
	for ctx.Err() == nil {
		e.logger.Debug("connecting to niri event stream")
		err := streamEvents(ctx, out, e.logger)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			e.logger.Warn("event stream ended; reconnecting", "err", err, "backoff", backoff)
		} else {
			e.logger.Warn("event stream closed; reconnecting", "backoff", backoff)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 10*time.Second {
			backoff *= 2
		}
	}
}

// evaluateAll re-arms debounce timers for every workspace whose desired label
// no longer matches what we last applied.
func (e *Engine) evaluateAll() {
	for _, id := range e.model.WorkspaceIDs() {
		e.evaluate(id)
	}
}

// sigEmpty is the debounce target for "owned workspace that must be cleared".
const sigEmpty = "\x00empty"

func (e *Engine) evaluate(wsID int) {
	ws, ok := e.model.Workspace(wsID)
	if !ok {
		return
	}
	if e.foreign(wsID, ws) {
		e.disarm(wsID) // user-set name; never touch
		return
	}

	if len(e.model.WindowsIn(wsID)) == 0 {
		if e.owned(wsID, ws) {
			e.armFor(wsID, sigEmpty)
		} else {
			e.disarm(wsID)
		}
		return
	}

	sig := e.model.Signature(wsID)
	if e.acted[wsID] == sig {
		e.disarm(wsID) // already labelled for this content
		return
	}
	e.armFor(wsID, sig)
}

// armFor (re)starts the debounce timer only when the workspace's target state
// changed since it was last armed; identical-target events leave it ticking so
// noisy unrelated windows cannot starve it. To bound the opposite case — a
// workspace whose title changes continuously — the wait is clamped so the timer
// still fires within maxWait of the first pending change.
func (e *Engine) armFor(wsID int, target string) {
	if e.armed[wsID] == target {
		return
	}
	e.armed[wsID] = target

	now := time.Now()
	if e.pendingSince[wsID].IsZero() {
		e.pendingSince[wsID] = now
	}
	wait := e.debounce
	if elapsed := now.Sub(e.pendingSince[wsID]); elapsed+wait > e.maxWait {
		wait = e.maxWait - elapsed
		if wait < 0 {
			wait = 0
		}
	}

	if t := e.timers[wsID]; t != nil {
		t.Reset(wait)
		return
	}
	e.timers[wsID] = time.AfterFunc(wait, func() {
		select {
		case e.fireCh <- wsID:
		default:
		}
	})
}

func (e *Engine) disarm(wsID int) {
	if t := e.timers[wsID]; t != nil {
		t.Stop()
		delete(e.timers, wsID)
	}
	delete(e.armed, wsID)
	delete(e.pendingSince, wsID)
}

func (e *Engine) onFire(ctx context.Context, wsID int) {
	delete(e.armed, wsID)
	delete(e.pendingSince, wsID)
	ws, ok := e.model.Workspace(wsID)
	if !ok || e.foreign(wsID, ws) || e.inflight[wsID] {
		return
	}

	windows := e.model.WindowsIn(wsID)
	sig := e.model.Signature(wsID)

	if len(windows) == 0 {
		if e.owned(wsID, ws) {
			e.unset(ctx, wsID, ws)
		}
		return
	}
	if e.acted[wsID] == sig {
		return
	}

	avoid := e.labelsInUse(wsID)
	e.logger.Debug("requesting label", "ws", wsID, "windows", len(windows), "avoid", avoid)
	e.inflight[wsID] = true
	go func() {
		e.sem <- struct{}{}
		defer func() { <-e.sem }()
		raw, err := e.labeler.Generate(ctx, windows, avoid)
		select {
		case e.resultCh <- jobResult{wsID: wsID, signature: sig, raw: raw, err: err}:
		case <-ctx.Done():
		}
	}()
}

func (e *Engine) onResult(ctx context.Context, res jobResult) {
	e.inflight[res.wsID] = false

	if res.err != nil {
		e.logger.Warn("label generation failed; keeping old label", "ws", res.wsID, "err", res.err)
		return // sanitize-then-keep: leave the old label, retry on next change
	}

	label, ok := sanitize(res.raw)
	if !ok {
		e.logger.Warn("model returned unusable label; keeping old", "ws", res.wsID, "raw", res.raw)
		return
	}

	if err := e.applyLabel(ctx, res.wsID, label, res.signature); err != nil {
		e.logger.Warn("apply label failed", "ws", res.wsID, "err", err)
		return
	}

	// Content may have drifted while codex ran; relabel if so.
	if e.model.Signature(res.wsID) != res.signature {
		e.evaluate(res.wsID)
	}
}

func (e *Engine) applyLabel(ctx context.Context, wsID int, label, sig string) error {
	// niri's idx shifts as workspaces are added/named, so resolve targeting
	// against live state, not the (possibly seconds-stale) model.
	live, err := e.niri.ListWorkspaces(ctx)
	if err != nil {
		return err
	}
	cur := findWorkspace(live, wsID)
	if cur == nil {
		return nil // workspace vanished while codex ran
	}

	// Keep managed names globally unique so they remain addressable.
	label = uniqueLabel(label, namesExcept(live, wsID))

	curName := nameOf(*cur)
	switch {
	case curName == label:
		e.model.SetLocalName(wsID, &label)
		e.commit(wsID, label, sig)
		return nil

	case curName != "":
		st, owned := e.state.Get(wsID)
		if !owned || st.Label != curName {
			return nil // became foreign mid-flight; leave it
		}
		// We own the current name → reference by it (stable, unique).
		if err := e.niri.SetName(ctx, curName, label); err != nil {
			return err
		}
		e.logger.Info("relabelled", "ws", wsID, "from", curName, "to", label)

	default:
		// Unnamed: idx is the only handle. Use the fresh idx, then verify the
		// intended workspace received the name (guards cross-output idx clashes).
		if err := e.niri.SetName(ctx, idxRef(cur.Idx), label); err != nil {
			return err
		}
		if got := e.workspaceNamed(ctx, label); got != wsID {
			_ = e.niri.UnsetName(ctx, label)
			return fmt.Errorf("idx %d resolved to workspace %d, not %d; skipping", cur.Idx, got, wsID)
		}
		e.logger.Info("labelled", "ws", wsID, "label", label)
	}

	e.model.SetLocalName(wsID, &label)
	e.commit(wsID, label, sig)
	return nil
}

func findWorkspace(wss []Workspace, id int) *Workspace {
	for i := range wss {
		if wss[i].ID == id {
			return &wss[i]
		}
	}
	return nil
}

func namesExcept(wss []Workspace, id int) map[string]bool {
	taken := map[string]bool{}
	for _, ws := range wss {
		if ws.ID != id && ws.Name != nil {
			taken[*ws.Name] = true
		}
	}
	return taken
}

func (e *Engine) unset(ctx context.Context, wsID int, ws Workspace) {
	if err := e.niri.UnsetName(ctx, *ws.Name); err != nil {
		e.logger.Warn("clear label failed", "ws", wsID, "err", err)
		return
	}
	e.model.SetLocalName(wsID, nil)
	delete(e.acted, wsID)
	e.state.Delete(wsID)
	e.logger.Info("cleared label (workspace empty)", "ws", wsID, "label", *ws.Name)
}

// clearOwnedLabels removes every label autolabel set this session, restoring
// the workspace names to the pre-startup snapshot. The incoming context is
// already cancelled on shutdown, so a fresh one is used for the niri calls.
func (e *Engine) clearOwnedLabels() {
	ids := e.state.IDs()
	if len(ids) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	e.logger.Info("clearing labels on shutdown", "count", len(ids))
	for _, wsID := range ids {
		st, ok := e.state.Get(wsID)
		if !ok {
			continue
		}
		if err := e.niri.UnsetName(ctx, st.Label); err != nil {
			e.logger.Warn("clear label on shutdown failed", "ws", wsID, "label", st.Label, "err", err)
			continue
		}
		e.logger.Debug("cleared label on shutdown", "ws", wsID, "label", st.Label)
	}
}

// labelsInUse returns the current names of all other workspaces, so the model
// can be asked to pick a distinct label.
func (e *Engine) labelsInUse(exceptWsID int) []string {
	seen := map[string]bool{}
	var labels []string
	for _, id := range e.model.WorkspaceIDs() {
		if id == exceptWsID {
			continue
		}
		ws, _ := e.model.Workspace(id)
		name := nameOf(ws)
		if name != "" && !seen[name] {
			seen[name] = true
			labels = append(labels, name)
		}
	}
	return labels
}

func (e *Engine) commit(wsID int, label, sig string) {
	e.acted[wsID] = sig
	e.state.Set(wsID, label, sig)
}

// workspaceNamed returns the id of the workspace currently holding name, or -1.
func (e *Engine) workspaceNamed(ctx context.Context, name string) int {
	wss, err := e.niri.ListWorkspaces(ctx)
	if err != nil {
		return -1
	}
	for _, ws := range wss {
		if ws.Name != nil && *ws.Name == name {
			return ws.ID
		}
	}
	return -1
}

func (e *Engine) owned(wsID int, ws Workspace) bool {
	name := nameOf(ws)
	if name == "" {
		return false
	}
	st, ok := e.state.Get(wsID)
	return ok && st.Label == name
}

func (e *Engine) foreign(wsID int, ws Workspace) bool {
	name := nameOf(ws)
	if name == "" {
		return false
	}
	st, ok := e.state.Get(wsID)
	return !ok || st.Label != name
}

func nameOf(ws Workspace) string {
	if ws.Name == nil {
		return ""
	}
	return *ws.Name
}
