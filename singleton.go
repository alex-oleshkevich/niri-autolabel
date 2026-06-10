package main

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// errAlreadyRunning is returned when another niri-autolabel instance holds the lock.
var errAlreadyRunning = errors.New("another niri-autolabel instance is already running")

// acquireSingleInstance takes an exclusive, non-blocking advisory lock (flock)
// so only one niri-autolabel runs at a time. The lock is held for the process
// lifetime via the open file descriptor and released automatically by the
// kernel on exit, including crashes — so a dead instance never blocks a restart.
func acquireSingleInstance() (release func(), err error) {
	path := lockPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errAlreadyRunning
		}
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

func lockPath() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "niri-autolabel.lock")
}
