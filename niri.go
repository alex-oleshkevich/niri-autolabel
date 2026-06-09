package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

type Workspace struct {
	ID     int     `json:"id"`
	Idx    int     `json:"idx"`
	Name   *string `json:"name"`
	Output string  `json:"output"`
}

type Window struct {
	ID          int    `json:"id"`
	Title       string `json:"title"`
	AppID       string `json:"app_id"`
	WorkspaceID *int   `json:"workspace_id"`
}

// Event mirrors the subset of niri's event-stream we react to. niri sends one
// JSON object per line; each variant we ignore leaves all fields nil.
type Event struct {
	WorkspacesChanged *struct {
		Workspaces []Workspace `json:"workspaces"`
	} `json:"WorkspacesChanged"`
	WindowsChanged *struct {
		Windows []Window `json:"windows"`
	} `json:"WindowsChanged"`
	WindowOpenedOrChanged *struct {
		Window Window `json:"window"`
	} `json:"WindowOpenedOrChanged"`
	WindowClosed *struct {
		ID int `json:"id"`
	} `json:"WindowClosed"`
}

// relevant reports whether an event can change workspace membership or naming.
func (e Event) relevant() bool {
	return e.kind() != ""
}

func (e Event) kind() string {
	switch {
	case e.WorkspacesChanged != nil:
		return "WorkspacesChanged"
	case e.WindowsChanged != nil:
		return "WindowsChanged"
	case e.WindowOpenedOrChanged != nil:
		return "WindowOpenedOrChanged"
	case e.WindowClosed != nil:
		return "WindowClosed"
	default:
		return ""
	}
}

// Niri abstracts the compositor so the engine can be tested without a live niri.
type Niri interface {
	ListWorkspaces(ctx context.Context) ([]Workspace, error)
	ListWindows(ctx context.Context) ([]Window, error)
	SetName(ctx context.Context, ref, name string) error
	UnsetName(ctx context.Context, ref string) error
}

type NiriClient struct {
	dryRun bool
	out    io.Writer
	logger Logger
}

func NewNiriClient(dryRun bool, out io.Writer, logger Logger) NiriClient {
	return NiriClient{dryRun: dryRun, out: out, logger: logger}
}

func (c NiriClient) ListWorkspaces(ctx context.Context) ([]Workspace, error) {
	var ws []Workspace
	return ws, c.queryJSON(ctx, &ws, "msg", "-j", "workspaces")
}

func (c NiriClient) ListWindows(ctx context.Context) ([]Window, error) {
	var w []Window
	return w, c.queryJSON(ctx, &w, "msg", "-j", "windows")
}

func (c NiriClient) SetName(ctx context.Context, ref, name string) error {
	return c.run(ctx, "msg", "action", "set-workspace-name", "--workspace", ref, name)
}

func (c NiriClient) UnsetName(ctx context.Context, ref string) error {
	return c.run(ctx, "msg", "action", "unset-workspace-name", ref)
}

func (c NiriClient) queryJSON(ctx context.Context, dst any, args ...string) error {
	out, err := exec.CommandContext(ctx, "niri", args...).Output()
	if err != nil {
		return err
	}
	return json.Unmarshal(out, dst)
}

func (c NiriClient) run(ctx context.Context, args ...string) error {
	c.logger.Debug("niri", "args", joinArgs(args))
	if c.dryRun {
		_, _ = fmt.Fprintf(c.out, "niri %s\n", joinArgs(args))
		return nil
	}
	cmd := exec.CommandContext(ctx, "niri", args...)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// streamEvents runs the niri event-stream and pushes relevant events onto out
// until the stream ends or the context is cancelled.
func streamEvents(ctx context.Context, out chan<- Event, logger Logger) error {
	cmd := exec.CommandContext(ctx, "niri", "msg", "-j", "event-stream")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}

	scanErr := scanEvents(ctx, stdout, out, logger)
	waitErr := cmd.Wait()
	if scanErr != nil {
		return scanErr
	}
	return waitErr
}

func scanEvents(ctx context.Context, reader io.Reader, out chan<- Event, logger Logger) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	for scanner.Scan() {
		event, err := parseEvent(scanner.Bytes())
		if err != nil {
			logger.Warn("skipping malformed event", "err", err)
			continue
		}
		if !event.relevant() {
			continue
		}
		select {
		case out <- event:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return scanner.Err()
}

func parseEvent(data []byte) (Event, error) {
	var event Event
	if err := json.Unmarshal(data, &event); err != nil {
		return Event{}, err
	}
	return event, nil
}

func joinArgs(args []string) string {
	return strings.Join(args, " ")
}

func idxRef(idx int) string { return strconv.Itoa(idx) }
