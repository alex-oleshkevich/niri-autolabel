package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	defaultBaseURL = "https://openrouter.ai/api/v1"
	defaultModel   = "google/gemini-2.5-flash-lite"

	// Placeholders a custom --prompt template may use.
	windowsPlaceholder = "{{windows}}"
	avoidPlaceholder   = "{{avoid}}"

	labelSystemPrompt = `You name a niri workspace with a SINGLE word. A workspace groups related windows together. Your job: find what is COMMON to all (or most) of the windows and name that shared subject.

Method:
- Read ALL window titles and apps together, then identify the common thread that ties them: a shared project, repository, client/company, product, website or topic.
- A name that recurs across multiple windows is the strongest signal (e.g. a company appearing in a Slack title AND browser tabs; a project folder open in both an editor AND a terminal). Name that.
- Treat per-window incidental details as clues to the shared theme, not as the label themselves: a single source file ("client.test.ts"), one chat subject, or one video title is NOT the workspace label.
- If the windows share no common project or topic, do NOT pick an obscure word from one window. Instead label by the MOST COMMON activity: count the windows by kind and name the majority — mostly chat/messaging apps -> "chats", mostly terminals -> "terminal", mostly editors with no shared project -> "code", mostly browser tabs on unrelated sites -> the dominant site or "browsing", mostly media/video -> "media".

Extracting names:
- editor tab "projectname — file.ext": the project is the part before the dash.
- a path like "~/projects/web-shop" or "~/dev/web-shop": the project is the last folder -> "webshop".
- a company/brand/site recurring across windows: use it (e.g. the company name, github, gmail).

Output:
- EXACTLY one word: lowercase, letters and digits only, max 12 characters, no spaces or punctuation.
- Use a real, recognizable name. Join multi-word names cleanly: "ai assistant"->"aiassistant", "web shop"->"webshop". NEVER output a truncated fragment like "aiai".
- A generic activity word (chats, terminal, code, media, browsing) is correct ONLY when the windows share no specific project or topic; otherwise prefer the specific name.
- If a list of labels already in use is given, pick a DIFFERENT word.
- Output only the word.

Examples:
Windows:
- app=slack title="DM - Acme Corp - Slack"
- app=google-chrome title="Acme Corp - Admin"
- app=dev.zed.Zed title="acme-billing — invoice.test.ts"
- app=ghostty title="~/work/acme-billing"
Label: acme

Windows:
- app=ghostty title="vim"
- app=ghostty title="~/projects/photoapp"
Label: photoapp

Windows:
- app=dev.zed.Zed title="task-tracker — server.go"
- app=ghostty title="go test ./..."
Label: tasktracker

Windows:
- app=google-chrome title="Inbox - Gmail"
- app=google-chrome title="Calendar - Google"
Label: gmail

Windows:
- app=org.telegram.desktop title="Messages"
- app=discord title="general"
- app=google-chrome title="some clip - YouTube"
Label: chats`
)

// Labeler produces a raw label suggestion for a workspace's windows. avoid lists
// labels already used by other workspaces, so the model can stay distinct. The
// engine sanitizes and falls back, so Generate returns the model's raw reply.
type Labeler interface {
	Generate(ctx context.Context, windows []Window, avoid []string) (string, error)
}

type OpenRouterLabeler struct {
	apiKey   string
	model    string
	template string // custom --prompt; when set, replaces the built-in system+user prompt
	url      string
	client   *http.Client
	logger   Logger
}

// NewOpenRouterLabeler builds a labeler targeting an OpenAI-compatible chat API
// at baseURL (e.g. https://openrouter.ai/api/v1, or a local Ollama). A non-empty
// template replaces the built-in prompt; it may contain {{windows}} and {{avoid}}
// placeholders (the window list is appended if {{windows}} is absent).
func NewOpenRouterLabeler(apiKey, model, baseURL, template string, logger Logger) OpenRouterLabeler {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return OpenRouterLabeler{
		apiKey:   apiKey,
		model:    model,
		template: template,
		url:      strings.TrimRight(baseURL, "/") + "/chat/completions",
		client:   &http.Client{Timeout: 30 * time.Second},
		logger:   logger,
	}
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens"`
	Messages    []chatMessage `json:"messages"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (l OpenRouterLabeler) Generate(ctx context.Context, windows []Window, avoid []string) (string, error) {
	messages := l.messages(windows, avoid)
	if l.logger.Enabled(ctx, slog.LevelDebug) {
		var b strings.Builder
		fmt.Fprintf(&b, "\n======== autolabel prompt -> %s ========\n", l.model)
		for _, m := range messages {
			fmt.Fprintf(&b, "--- %s ---\n%s\n\n", m.Role, m.Content)
		}
		b.WriteString("=========================================\n")
		fmt.Fprint(os.Stderr, b.String())
	}

	payload, err := json.Marshal(chatRequest{
		Model:       l.model,
		Temperature: 0,
		MaxTokens:   16,
		Messages:    messages,
	})
	if err != nil {
		return "", err
	}
	start := time.Now()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.url, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+l.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Title", "niri-autolabel")

	resp, err := l.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("decode response: %w (%s)", err, truncate(string(body), 200))
	}
	if parsed.Error != nil {
		return "", fmt.Errorf("openrouter: %s", parsed.Error.Message)
	}
	if resp.StatusCode != http.StatusOK || len(parsed.Choices) == 0 {
		return "", fmt.Errorf("openrouter: status %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	content := parsed.Choices[0].Message.Content
	l.logger.Debug("label generated",
		"model", l.model, "windows", len(windows),
		"latency_ms", time.Since(start).Milliseconds(),
		"raw", strings.TrimSpace(content))
	return content, nil
}

// messages builds the chat messages. With a custom template it is sent as a
// single user message (placeholders substituted); otherwise the built-in
// system + user prompt is used.
func (l OpenRouterLabeler) messages(windows []Window, avoid []string) []chatMessage {
	if l.template != "" {
		return []chatMessage{{Role: "user", Content: renderTemplate(l.template, windows, avoid)}}
	}
	return []chatMessage{
		{Role: "system", Content: labelSystemPrompt},
		{Role: "user", Content: buildUserMessage(windows, avoid)},
	}
}

func renderTemplate(tpl string, windows []Window, avoid []string) string {
	out := tpl
	if strings.Contains(out, windowsPlaceholder) {
		out = strings.ReplaceAll(out, windowsPlaceholder, windowList(windows))
	} else {
		out = strings.TrimRight(out, "\n") + "\n\n" + windowList(windows)
	}
	out = strings.ReplaceAll(out, avoidPlaceholder, strings.Join(avoid, ", "))
	return out
}

func buildUserMessage(windows []Window, avoid []string) string {
	var b strings.Builder
	b.WriteString("Windows open in this workspace:\n")
	b.WriteString(windowList(windows))
	if len(avoid) > 0 {
		fmt.Fprintf(&b, "\n\nLabels already in use (choose a different word): %s", strings.Join(avoid, ", "))
	}
	b.WriteString("\n\nLabel:")
	return b.String()
}

func windowList(windows []Window) string {
	var b strings.Builder
	for i, w := range windows {
		app := w.AppID
		if app == "" {
			app = "unknown"
		}
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "- app=%s title=%q", app, w.Title)
	}
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
