package llm

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// testStreamEvent creates a StreamEvent with only content (no reasoning).
func testStreamEvent(content string) StreamEvent {
	return StreamEvent{
		Choices: []struct {
			Delta struct {
				Content         string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"delta"`
			FinishReason *string `json:"finish_reason"`
		}{
			{Delta: struct {
				Content         string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			}{Content: content}},
		},
	}
}

func testStreamEventEmpty() StreamEvent {
	return StreamEvent{Choices: []struct {
		Delta struct {
			Content         string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	}{}}
}

// ---------- Complete ----------

func TestComplete_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "/chat/completions") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("auth = %q, want 'Bearer test-key'", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content-type = %q", r.Header.Get("Content-Type"))
		}

		// Verify request body
		var req ChatRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.Model != "gpt-4o" {
			t.Errorf("model = %q", req.Model)
		}
		if len(req.Messages) != 1 {
			t.Errorf("messages = %d, want 1", len(req.Messages))
		}
		if req.Messages[0].Role != "user" {
			t.Errorf("role = %q", req.Messages[0].Role)
		}
		if req.Stream {
			t.Error("stream should be false")
		}

		resp := ChatResponse{
			Choices: []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
				FinishReason string `json:"finish_reason"`
			}{
				{
					Message: struct {
						Content string `json:"content"`
					}{Content: "Hello from server!"},
					FinishReason: "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient()
	result, err := client.Complete(t.Context(), server.URL, "test-key", "gpt-4o", []Message{
		{Role: "user", Content: "Hi"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Hello from server!" {
		t.Errorf("result = %q", result)
	}
}

func TestComplete_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		resp := ChatResponse{
			Error: &struct {
				Message string `json:"message"`
			}{Message: "invalid request"},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient()
	_, err := client.Complete(t.Context(), server.URL, "key", "model", []Message{
		{Role: "user", Content: "Hi"},
	})
	if err == nil {
		t.Fatal("expected error for API error response")
	}
	if !strings.Contains(err.Error(), "invalid request") {
		t.Errorf("error = %q, should contain API error message", err.Error())
	}
}

func TestComplete_StatusCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error": {"message": "unauthorized"}}`))
	}))
	defer server.Close()

	client := NewClient()
	_, err := client.Complete(t.Context(), server.URL, "bad-key", "model", []Message{
		{Role: "user", Content: "Hi"},
	})
	if err == nil {
		t.Fatal("expected error for 401 status")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error = %q, should mention status code", err.Error())
	}
}

func TestComplete_NoChoices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ChatResponse{Choices: []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		}{}}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient()
	_, err := client.Complete(t.Context(), server.URL, "key", "model", []Message{
		{Role: "user", Content: "Hi"},
	})
	if err == nil {
		t.Fatal("expected error for no choices")
	}
	if !strings.Contains(err.Error(), "no choices") {
		t.Errorf("error = %q, should mention 'no choices'", err.Error())
	}
}

func TestComplete_ServerDown(t *testing.T) {
	client := NewClient()
	_, err := client.Complete(t.Context(), "http://127.0.0.1:1", "key", "model", []Message{
		{Role: "user", Content: "Hi"},
	})
	if err == nil {
		t.Fatal("expected error for connection refused")
	}
}

func TestComplete_ContextCanceled(t *testing.T) {
	ctx, cancel := t.Context(), func() {}
	_ = ctx
	_ = cancel

	// Context with cancel
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(ChatResponse{
			Choices: []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
				FinishReason string `json:"finish_reason"`
			}{
				{Message: struct {
					Content string `json:"content"`
				}{Content: "ok"}},
			},
		})
	}))
	defer server.Close()

	client := NewClient()
	result, err := client.Complete(t.Context(), server.URL, "key", "model", []Message{
		{Role: "user", Content: "Hi"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "ok" {
		t.Errorf("result = %q", result)
	}
}

// ---------- StreamComplete ----------

func TestStreamComplete_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}

		// Verify stream request
		var req ChatRequest
		json.NewDecoder(r.Body).Decode(&req)
		if !req.Stream {
			t.Error("stream should be true")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)

		// Send chunks
		chunks := []string{"Hello", " ", "world", "!"}
		for _, chunk := range chunks {
			event := testStreamEvent(chunk)
			data, _ := json.Marshal(event)
			w.Write([]byte("data: " + string(data) + "\n\n"))
			flusher.Flush()
		}
		w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer server.Close()

	client := NewClient()
	var received strings.Builder
	sm := &StreamMetrics{}
	result, err := client.StreamComplete(t.Context(), server.URL, "key", "model", []Message{
		{Role: "user", Content: "Say hello"},
	}, &received, sm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Hello world!" {
		t.Errorf("result = %q, want %q", result, "Hello world!")
	}
	if sm.TokenCount != 4 {
		t.Errorf("token_count = %d, want 4", sm.TokenCount)
	}
	if sm.TTFT == 0 {
		t.Error("ttft should be > 0")
	}
	if sm.TokensPerSec <= 0 {
		t.Error("tokens_per_sec should be > 0")
	}
}

func TestStreamComplete_EmptyChunks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)

		// Send empty delta
		event := testStreamEvent("")
		data, _ := json.Marshal(event)
		w.Write([]byte("data: " + string(data) + "\n\n"))
		flusher.Flush()

		// Then real content
		event2 := testStreamEvent("hi")
		data, _ = json.Marshal(event2)
		w.Write([]byte("data: " + string(data) + "\n\n"))
		flusher.Flush()

		w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer server.Close()

	client := NewClient()
	var buf strings.Builder
	result, err := client.StreamComplete(t.Context(), server.URL, "key", "model", []Message{
		{Role: "user", Content: "Hi"},
	}, &buf, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "hi" {
		t.Errorf("result = %q, want %q", result, "hi")
	}
}

func TestStreamComplete_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": {"message": "bad request"}}`))
	}))
	defer server.Close()

	client := NewClient()
	var buf strings.Builder
	sm := &StreamMetrics{}
	_, err := client.StreamComplete(t.Context(), server.URL, "key", "model", []Message{
		{Role: "user", Content: "Hi"},
	}, &buf, sm)
	if err == nil {
		t.Fatal("expected error for 400 status")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error = %q, should mention status code", err.Error())
	}
	if sm.Error == "" {
		t.Error("metrics.error should be set on failure")
	}
}

func TestStreamComplete_ServerDown(t *testing.T) {
	client := NewClient()
	var buf strings.Builder
	sm := &StreamMetrics{}
	_, err := client.StreamComplete(t.Context(), "http://127.0.0.1:1", "key", "model", []Message{
		{Role: "user", Content: "Hi"},
	}, &buf, sm)
	if err == nil {
		t.Fatal("expected error for connection refused")
	}
	if sm.Error == "" {
		t.Error("metrics.error should be set on connection failure")
	}
}

func TestStreamComplete_MalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)

		// Send malformed JSON
		w.Write([]byte("data: {invalid json\n\n"))
		flusher.Flush()

		// Then valid content
		event := testStreamEvent("recovered")
		data, _ := json.Marshal(event)
		w.Write([]byte("data: " + string(data) + "\n\n"))
		flusher.Flush()

		w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer server.Close()

	client := NewClient()
	var buf strings.Builder
	result, err := client.StreamComplete(t.Context(), server.URL, "key", "model", []Message{
		{Role: "user", Content: "Hi"},
	}, &buf, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "recovered" {
		t.Errorf("result = %q, should recover from malformed JSON", result)
	}
}

func TestStreamComplete_NonDataLines(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)

		// Send non-data lines (comments, empty lines)
		w.Write([]byte(": comment\n\n"))
		w.Write([]byte("\n"))
		w.Write([]byte("event: message\n\n"))
		flusher.Flush()

		// Then valid content
		event := testStreamEvent("ok")
		data, _ := json.Marshal(event)
		w.Write([]byte("data: " + string(data) + "\n\n"))
		flusher.Flush()

		w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer server.Close()

	client := NewClient()
	var buf strings.Builder
	result, err := client.StreamComplete(t.Context(), server.URL, "key", "model", []Message{
		{Role: "user", Content: "Hi"},
	}, &buf, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "ok" {
		t.Errorf("result = %q", result)
	}
}

func TestStreamComplete_NilChoices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)

		event := testStreamEventEmpty()
		data, _ := json.Marshal(event)
		w.Write([]byte("data: " + string(data) + "\n\n"))
		flusher.Flush()

		// Then real content
		event2 := testStreamEvent("ok")
		data2, _ := json.Marshal(event2)
		w.Write([]byte("data: " + string(data2) + "\n\n"))
		flusher.Flush()

		w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer server.Close()

	client := NewClient()
	var buf strings.Builder
	result, err := client.StreamComplete(t.Context(), server.URL, "key", "model", []Message{
		{Role: "user", Content: "Hi"},
	}, &buf, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "ok" {
		t.Errorf("result = %q", result)
	}
}

// ---------- NewClient ----------

func TestNewClient(t *testing.T) {
	client := NewClient()
	if client.HTTPClient == nil {
		t.Error("HTTPClient should not be nil")
	}
	if client.HTTPClient.Timeout != 300*1e9 {
		t.Errorf("timeout = %v, want 300s", client.HTTPClient.Timeout)
	}
}

// ---------- Request body ----------

func TestComplete_SendsCorrectHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer my-api-key" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("HTTP-Referer") != "https://github.com/redstone-md/graft" {
			t.Errorf("HTTP-Referer = %q", r.Header.Get("HTTP-Referer"))
		}
		if r.Header.Get("X-Title") != "Fusion Orchestrator" {
			t.Errorf("X-Title = %q", r.Header.Get("X-Title"))
		}

		resp := ChatResponse{
			Choices: []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
				FinishReason string `json:"finish_reason"`
			}{
				{
					Message: struct {
						Content string `json:"content"`
					}{Content: "ok"},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient()
	result, err := client.Complete(t.Context(), server.URL, "my-api-key", "model", []Message{
		{Role: "user", Content: "Hi"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "ok" {
		t.Errorf("result = %q", result)
	}
}

// ---------- Stress: large response ----------

func TestComplete_LargeResponse(t *testing.T) {
	largeContent := strings.Repeat("x", 100000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ChatResponse{
			Choices: []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
				FinishReason string `json:"finish_reason"`
			}{
				{
					Message: struct {
						Content string `json:"content"`
					}{Content: largeContent},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient()
	result, err := client.Complete(t.Context(), server.URL, "key", "model", []Message{
		{Role: "user", Content: "Hi"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 100000 {
		t.Errorf("result length = %d, want 100000", len(result))
	}
}

// Needed for bufio.Scanner in StreamComplete
var _ = bufio.NewScanner
