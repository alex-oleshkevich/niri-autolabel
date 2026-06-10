package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	dryRun := flag.Bool("dry-run", false, "print niri actions instead of running them")
	debug := flag.Bool("debug", false, "shorthand for -log-level debug (logs each prompt and response)")
	verbose := flag.Bool("verbose", false, "shorthand for -log-level debug")
	logLevel := flag.String("log-level", "info", "log level (debug|info|warn|error)")
	logFormat := flag.String("log-format", "text", "log format (text|json)")
	model := flag.String("model", envOr("OPENROUTER_MODEL", defaultModel), "model (overrides $OPENROUTER_MODEL)")
	baseURL := flag.String("base-url", envOr("OPENROUTER_BASE_URL", defaultBaseURL), "OpenAI-compatible API base URL (e.g. a local Ollama; overrides $OPENROUTER_BASE_URL)")
	debounce := flag.Duration("debounce", 5*time.Second, "settle time after a workspace change before labelling")
	maxWait := flag.Duration("max-wait", 30*time.Second, "force a relabel within this long even if a window keeps changing")
	workers := flag.Int("workers", 2, "max concurrent label requests")
	once := flag.Bool("once", false, "label the current workspaces once and exit (no daemon, keeps labels)")
	promptFile := flag.String("prompt", "", "file with a custom prompt template ({{windows}} and {{avoid}} placeholders)")
	flag.Parse()

	if *showVersion {
		fmt.Println("autolabel", version)
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	level := ParseLevel(*logLevel)
	if *verbose || *debug {
		level = slog.LevelDebug
	}
	logger := NewLogger(level, *logFormat, os.Stderr)

	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" && !*dryRun {
		logger.Error("OPENROUTER_API_KEY is not set")
		os.Exit(1)
	}

	var template string
	if *promptFile != "" {
		data, err := os.ReadFile(*promptFile)
		if err != nil {
			logger.Error("cannot read prompt file", "path", *promptFile, "err", err)
			os.Exit(1)
		}
		template = string(data)
	}

	logger.Info("starting",
		"model", *model, "base_url", *baseURL, "debounce", *debounce, "max_wait", *maxWait,
		"workers", *workers, "dry_run", *dryRun, "custom_prompt", *promptFile != "", "once", *once)

	niri := NewNiriClient(*dryRun, os.Stdout, logger)
	labeler := NewOpenRouterLabeler(apiKey, *model, *baseURL, template, logger)
	state := NewState()
	engine := NewEngine(niri, labeler, state, logger, *debounce, *maxWait, *workers)

	// One-shot mode: label the current state and exit, leaving labels in place.
	// No single-instance lock (it may run alongside the daemon) and no clear-on-exit.
	if *once {
		if err := engine.RunOnce(ctx); err != nil && ctx.Err() == nil {
			logger.Error("fatal", "err", err)
			os.Exit(1)
		}
		return
	}

	release, err := acquireSingleInstance()
	if err != nil {
		logger.Error("cannot start", "err", err)
		os.Exit(1)
	}
	defer release()

	if err := engine.Run(ctx); err != nil && ctx.Err() == nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
	logger.Info("stopped")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
