package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenRouterGenerate(t *testing.T) {
	var gotReq chatRequest
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotReq)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"coding\n"}}]}`)
	}))
	defer srv.Close()

	lab := NewOpenRouterLabeler("secret-key", "test/model", "", "", NopLogger())
	lab.client = srv.Client()
	lab.url = srv.URL

	out, err := lab.Generate(context.Background(), []Window{
		{AppID: "ghostty", Title: "nvim main.go"},
	}, []string{"gmail", "slack"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if strings.TrimSpace(out) != "coding" {
		t.Fatalf("content = %q, want coding", out)
	}
	if gotAuth != "Bearer secret-key" {
		t.Fatalf("auth header = %q", gotAuth)
	}
	if gotReq.Model != "test/model" {
		t.Fatalf("model = %q, want test/model", gotReq.Model)
	}
	if len(gotReq.Messages) != 2 || gotReq.Messages[0].Role != "system" {
		t.Fatalf("unexpected messages: %+v", gotReq.Messages)
	}
	if !strings.Contains(gotReq.Messages[1].Content, "app=ghostty") {
		t.Fatalf("user message missing window list: %q", gotReq.Messages[1].Content)
	}
	if !strings.Contains(gotReq.Messages[1].Content, "gmail, slack") {
		t.Fatalf("user message missing avoid list: %q", gotReq.Messages[1].Content)
	}
}

func TestRenderTemplateInjectsWindowsAndAvoid(t *testing.T) {
	wins := []Window{{AppID: "ghostty", Title: "nvim"}, {AppID: "slack", Title: "general"}}

	got := renderTemplate("Label these:\n{{windows}}\nAvoid: {{avoid}}", wins, []string{"gmail", "code"})
	if !strings.Contains(got, "app=ghostty") || !strings.Contains(got, "app=slack") {
		t.Fatalf("windows not injected: %q", got)
	}
	if !strings.Contains(got, "Avoid: gmail, code") {
		t.Fatalf("avoid not injected: %q", got)
	}

	appended := renderTemplate("Name this workspace.", wins, nil)
	if !strings.Contains(appended, "app=ghostty") {
		t.Fatalf("windows not appended when placeholder absent: %q", appended)
	}
}

func TestBaseURLBuildsCompletionsEndpoint(t *testing.T) {
	lab := NewOpenRouterLabeler("k", "m", "http://localhost:11434/v1/", "", NopLogger())
	if lab.url != "http://localhost:11434/v1/chat/completions" {
		t.Fatalf("url = %q", lab.url)
	}
	def := NewOpenRouterLabeler("k", "m", "", "", NopLogger())
	if def.url != "https://openrouter.ai/api/v1/chat/completions" {
		t.Fatalf("default url = %q", def.url)
	}
}

func TestOpenRouterErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"no endpoints found"}}`)
	}))
	defer srv.Close()

	lab := NewOpenRouterLabeler("k", "bad/model", "", "", NopLogger())
	lab.client = srv.Client()
	lab.url = srv.URL

	if _, err := lab.Generate(context.Background(), nil, nil); err == nil {
		t.Fatal("expected error from error body")
	}
}
