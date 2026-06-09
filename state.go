package main

import "sync"

// wsState records what autolabel set for a workspace this session: the label,
// and the content signature that produced it. Label drives ownership (we only
// touch names we set); Signature drives the cache (skip the model when
// unchanged). Ownership is intentionally session-scoped and never persisted:
// names that already exist at startup are foreign and left untouched, and our
// own labels are cleared on exit.
type wsState struct {
	Label     string
	Signature string
}

type State struct {
	mu    sync.Mutex
	items map[int]wsState
}

func NewState() *State {
	return &State{items: map[int]wsState{}}
}

func (s *State) Get(id int) (wsState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.items[id]
	return v, ok
}

func (s *State) Set(id int, label, signature string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[id] = wsState{Label: label, Signature: signature}
}

func (s *State) Delete(id int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, id)
}

// IDs returns the workspace ids we currently manage.
func (s *State) IDs() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]int, 0, len(s.items))
	for id := range s.items {
		ids = append(ids, id)
	}
	return ids
}
