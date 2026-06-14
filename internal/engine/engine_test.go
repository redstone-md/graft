package engine

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/redstone-md/graft/internal/config"
	"github.com/redstone-md/graft/internal/llm"
)

// ---------- truncateConversation ----------

func TestTruncateConversation_NoLimit(t *testing.T) {
	msgs := []llm.Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there!"},
	}

	result := truncateConversation(msgs, 0, 0)
	if len(result) != 3 {
		t.Errorf("expected 3 messages, got %d", len(result))
	}
}

func TestTruncateConversation_EmptyMessages(t *testing.T) {
	result := truncateConversation([]llm.Message{}, 10000, 2000)
	if len(result) != 0 {
		t.Errorf("expected 0 messages, got %d", len(result))
	}
}

func TestTruncateConversation_KeepsSystemPrompt(t *testing.T) {
	msgs := []llm.Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: strings.Repeat("x", 10000)},
		{Role: "assistant", Content: strings.Repeat("y", 10000)},
	}

	// Budget: 5000 - 2000 headroom = 3000 tokens. System prompt ~254 tokens.
	// Each message ~2504 tokens. Can fit system + 1 message.
	result := truncateConversation(msgs, 5000, 2000)
	if len(result) < 2 {
		t.Errorf("expected at least 2 messages (system + 1), got %d", len(result))
	}
	if result[0].Role != "system" {
		t.Errorf("first message should be system, got %q", result[0].Role)
	}
}

func TestTruncateConversation_DropsOldest(t *testing.T) {
	msgs := []llm.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: strings.Repeat("a", 400)},    // ~100 tokens
		{Role: "assistant", Content: strings.Repeat("b", 400)}, // ~100 tokens
		{Role: "user", Content: strings.Repeat("c", 400)},    // ~100 tokens
		{Role: "assistant", Content: strings.Repeat("d", 400)}, // ~100 tokens
	}

	// Budget: 500 tokens. System ~2 tokens. Each msg ~104 tokens.
	// Can fit system + ~4 messages. All fit.
	result := truncateConversation(msgs, 500, 0)
	if result[0].Role != "system" {
		t.Errorf("first should be system, got %q", result[0].Role)
	}
	// Should keep most recent messages
	lastMsg := result[len(result)-1]
	if lastMsg.Content != strings.Repeat("d", 400) {
		t.Errorf("last message should be 'd' repeated, got %q", lastMsg.Content[:20])
	}
}

func TestTruncateConversation_OnlyLastMessageWhenBudgetTiny(t *testing.T) {
	msgs := []llm.Message{
		{Role: "system", Content: strings.Repeat("x", 4000)}, // ~1000 tokens
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}

	// Budget: 100 tokens. System alone is ~1000 tokens. Exceeds budget.
	result := truncateConversation(msgs, 100, 0)
	if len(result) != 1 {
		t.Errorf("expected 1 message when budget tiny, got %d", len(result))
	}
}

func TestTruncateConversation_PreservesOrder(t *testing.T) {
	msgs := []llm.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "msg1"},
		{Role: "assistant", Content: "msg2"},
		{Role: "user", Content: "msg3"},
		{Role: "assistant", Content: "msg4"},
	}

	result := truncateConversation(msgs, 50000, 0)
	if len(result) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(result))
	}

	// Verify chronological order
	for i := 1; i < len(result); i++ {
		if result[i].Role != msgs[i].Role {
			t.Errorf("message %d: role %q != expected %q", i, result[i].Role, msgs[i].Role)
		}
	}
}

func TestTruncateConversation_NegativeBudget(t *testing.T) {
	msgs := []llm.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hello"},
	}

	// Budget: -1000. Should return only last message.
	result := truncateConversation(msgs, 1000, 2000)
	if len(result) != 1 {
		t.Errorf("expected 1 message, got %d", len(result))
	}
}

// ---------- extractJSON ----------

func TestExtractJSON_PlainJSON(t *testing.T) {
	input := `{"key": "value"}`
	result := extractJSON(input)
	if result != input {
		t.Errorf("got %q, want %q", result, input)
	}
}

func TestExtractJSON_WithMarkdownFence(t *testing.T) {
	input := "Here is the result:\n```json\n{\"key\": \"value\"}\n```\n"
	result := extractJSON(input)
	if result != `{"key": "value"}` {
		t.Errorf("got %q, want %q", result, `{"key": "value"}`)
	}
}

func TestExtractJSON_WithGenericFence(t *testing.T) {
	input := "Result:\n```\n{\"key\": \"value\"}\n```\n"
	result := extractJSON(input)
	if result != `{"key": "value"}` {
		t.Errorf("got %q, want %q", result, `{"key": "value"}`)
	}
}

func TestExtractJSON_WithPrefixText(t *testing.T) {
	input := "Analysis complete. Here:\n{\"evaluations\": []}"
	result := extractJSON(input)
	if !strings.HasPrefix(result, "{") {
		t.Errorf("should start with {, got %q", result[:20])
	}
}

func TestExtractJSON_WithNestedBraces(t *testing.T) {
	input := `{"evaluations": [{"model": "a", "data": {"nested": true}}]}`
	result := extractJSON(input)
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("failed to parse extracted JSON: %v", err)
	}
	if _, ok := parsed["evaluations"]; !ok {
		t.Error("missing 'evaluations' key")
	}
}

func TestExtractJSON_NoJSON(t *testing.T) {
	input := "No JSON here at all"
	result := extractJSON(input)
	if result != input {
		t.Errorf("should return original text when no JSON found, got %q", result)
	}
}

// ---------- parseJudgeAnalysis ----------

func TestParseJudgeAnalysis_Valid(t *testing.T) {
	raw := `{"evaluations": [{"model": "claude", "summary": "good", "strengths": ["clear"], "weaknesses": [], "factual_correct": true, "completeness": "full", "reasoning_depth": "deep"}], "consensus": ["all agree"], "contradictions": [], "partial_coverage": [], "unique_insights": [], "blind_spots": [], "recommendation": "use claude", "confidence_note": "90%"}`
	analysis, rawOut, err := parseJudgeAnalysis(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(analysis.Evaluations) != 1 {
		t.Errorf("evaluations = %d, want 1", len(analysis.Evaluations))
	}
	if analysis.Evaluations[0].Model != "claude" {
		t.Errorf("model = %q", analysis.Evaluations[0].Model)
	}
	if analysis.Evaluations[0].FactualCorrect == nil || !*analysis.Evaluations[0].FactualCorrect {
		t.Error("factual_correct should be true")
	}
	if analysis.Recommendation != "use claude" {
		t.Errorf("recommendation = %q", analysis.Recommendation)
	}
	if rawOut != raw {
		t.Error("raw output should be preserved")
	}
}

func TestParseJudgeAnalysis_WithMarkdown(t *testing.T) {
	raw := "Here is the analysis:\n```json\n{\"evaluations\": [], \"consensus\": [], \"contradictions\": [], \"partial_coverage\": [], \"unique_insights\": [], \"blind_spots\": [], \"recommendation\": \"ok\", \"confidence_note\": \"80%\"}\n```"
	analysis, _, err := parseJudgeAnalysis(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if analysis.Recommendation != "ok" {
		t.Errorf("recommendation = %q", analysis.Recommendation)
	}
}

func TestParseJudgeAnalysis_InvalidJSON(t *testing.T) {
	raw := "This is not JSON at all"
	_, _, err := parseJudgeAnalysis(raw)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "judge non-JSON") {
		t.Errorf("error = %q, should mention 'judge non-JSON'", err.Error())
	}
}

func TestParseJudgeAnalysis_PartialJSON(t *testing.T) {
	raw := `{"evaluations": [{"model": "claude"}]}`
	analysis, _, err := parseJudgeAnalysis(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(analysis.Evaluations) != 1 {
		t.Errorf("evaluations = %d, want 1", len(analysis.Evaluations))
	}
}

// ---------- buildPanelMessages ----------

func TestBuildPanelMessages(t *testing.T) {
	conversation := []llm.Message{
		{Role: "user", Content: "What is 2+2?"},
		{Role: "assistant", Content: "4"},
		{Role: "user", Content: "And 3+3?"},
	}

	result := buildPanelMessages(conversation)
	if len(result) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(result))
	}
	if result[0].Role != "system" {
		t.Errorf("first message should be system, got %q", result[0].Role)
	}
	if !strings.Contains(result[0].Content, "independent expert") {
		t.Error("system prompt should contain 'independent expert'")
	}
	if result[1].Content != "What is 2+2?" {
		t.Errorf("second message should be user question, got %q", result[1].Content[:20])
	}
}

// ---------- formatConversation ----------

func TestFormatConversation(t *testing.T) {
	msgs := []llm.Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi"},
	}

	result := formatConversation(msgs)
	if !strings.Contains(result, "[USER]") {
		t.Error("should contain [USER]")
	}
	if !strings.Contains(result, "[ASSISTANT]") {
		t.Error("should contain [ASSISTANT]")
	}
	if !strings.Contains(result, "Hello") {
		t.Error("should contain message content")
	}
}

func TestFormatConversation_Empty(t *testing.T) {
	result := formatConversation([]llm.Message{})
	if result != "" {
		t.Errorf("empty messages should return empty string, got %q", result)
	}
}

// ---------- formatPanelBrief ----------

func TestFormatPanelBrief(t *testing.T) {
	panel := []PanelResult{
		{Model: "claude", Answer: "Answer from Claude"},
		{Model: "gpt4", Answer: "Answer from GPT4"},
	}

	result := formatPanelBrief(panel)
	if !strings.Contains(result, "claude") {
		t.Error("should contain claude")
	}
	if !strings.Contains(result, "Answer from Claude") {
		t.Error("should contain claude's answer")
	}
	if !strings.Contains(result, "gpt4") {
		t.Error("should contain gpt4")
	}
}

func TestFormatPanelBrief_TruncatesLongAnswers(t *testing.T) {
	longAnswer := strings.Repeat("x", 3000)
	panel := []PanelResult{
		{Model: "claude", Answer: longAnswer},
	}

	result := formatPanelBrief(panel)
	if !strings.Contains(result, "[...truncated...]") {
		t.Error("should truncate long answers")
	}
}

func TestFormatPanelBrief_SkipsErrors(t *testing.T) {
	panel := []PanelResult{
		{Model: "claude", Answer: "Answer", Error: "timeout"},
		{Model: "gpt4", Answer: "Good answer"},
	}

	result := formatPanelBrief(panel)
	if strings.Contains(result, "timeout") {
		t.Error("should not include error results")
	}
	if !strings.Contains(result, "Good answer") {
		t.Error("should include successful results")
	}
}

// ---------- effectiveContextForPipeline ----------

func TestEffectiveContextForPipeline(t *testing.T) {
	cfg := loadTestConfig(t)

	result := effectiveContextForPipeline(cfg, []string{"claude", "gpt4"}, "gpt4-mini", "claude")
	if result != 128000 {
		t.Errorf("effective = %d, want 128000 (min of 200000, 128000)", result)
	}
}

func TestEffectiveContextForPipeline_AllSame(t *testing.T) {
	yaml := `
server:
  auth_token: "test"
providers:
  openrouter:
    base_url: "https://openrouter.ai/api/v1"
    api_key: "sk-or-xxx"
models:
  a:
    provider: openrouter
    model: "a"
    context_window: 100000
  b:
    provider: openrouter
    model: "b"
    context_window: 100000
  c:
    provider: openrouter
    model: "c"
    context_window: 100000
profiles:
  default:
    panel: ["a"]
    judge: "b"
    final: "c"
`
	cfg := loadTestConfigWith(t, yaml)
	result := effectiveContextForPipeline(cfg, []string{"a"}, "b", "c")
	if result != 100000 {
		t.Errorf("effective = %d, want 100000", result)
	}
}

// ---------- buildJudgeMessages ----------

func TestBuildJudgeMessages(t *testing.T) {
	conversation := []llm.Message{
		{Role: "user", Content: "What is Rust?"},
	}
	answers := []string{
		"=== claude ===\nRust is a systems language",
		"=== gpt4 ===\nRust is a programming language",
	}

	result := buildJudgeMessages(conversation, answers)
	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result))
	}
	if result[0].Role != "system" {
		t.Errorf("first should be system, got %q", result[0].Role)
	}
	if !strings.Contains(result[1].Content, "claude") {
		t.Error("should contain panel answers")
	}
	if !strings.Contains(result[1].Content, "=== gpt4 ===") {
		t.Error("should contain gpt4 answer")
	}
}

// ---------- errText ----------

func TestErrText_Nil(t *testing.T) {
	result := errText(nil)
	if result != "" {
		t.Errorf("nil error should return empty string, got %q", result)
	}
}

func TestErrText_Error(t *testing.T) {
	result := errText(fmt.Errorf("fail"))
	if result != "fail" {
		t.Errorf("expected 'fail', got %q", result)
	}
}

// ---------- Helpers ----------

func loadTestConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load(writeTempConfig(t, validConfigYAML()))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func loadTestConfigWith(t *testing.T, yaml string) *config.Config {
	t.Helper()
	cfg, err := config.Load(writeTempConfig(t, yaml))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func validConfigYAML() string {
	return `
server:
  auth_token: "test-token"
providers:
  openrouter:
    base_url: "https://openrouter.ai/api/v1"
    api_key: "sk-or-xxx"
models:
  claude:
    provider: openrouter
    model: "anthropic/claude-3.5-sonnet"
    context_window: 200000
  gpt4:
    provider: openrouter
    model: "openai/gpt-4o"
    context_window: 128000
  gpt4-mini:
    provider: openrouter
    model: "openai/gpt-4o-mini"
    context_window: 128000
profiles:
  default:
    panel: ["claude", "gpt4"]
    judge: "gpt4-mini"
    final: "claude"
`
}

// ---------- Mock HTTP Server for pipeline tests ----------

func mockLLMServer(t *testing.T, response string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "/chat/completions") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") == "" {
			t.Error("missing Authorization header")
		}

		resp := llm.ChatResponse{
			Choices: []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
				FinishReason string `json:"finish_reason"`
			}{
				{
					Message: struct {
						Content string `json:"content"`
					}{Content: response},
					FinishReason: "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
}

func TestEngineRun_FullPipeline(t *testing.T) {
	// Start 3 mock servers for panel + judge + final
	panelServer := mockLLMServer(t, "Panel answer from model")
	judgeJSON := `{"evaluations": [{"model": "claude", "summary": "good", "strengths": ["clear"], "weaknesses": [], "factual_correct": true, "completeness": "full", "reasoning_depth": "deep"}], "consensus": [], "contradictions": [], "partial_coverage": [], "unique_insights": [], "blind_spots": [], "recommendation": "use claude", "confidence_note": "95%"}`
	judgeServer := mockLLMServer(t, judgeJSON)
	finalServer := mockLLMServer(t, "Final synthesized answer")

	cfg := configWithServers(t, panelServer.URL, judgeServer.URL, finalServer.URL)
	eng := NewEngine(cfg)

	result, err := eng.Run(t.Context(), GraftRequest{
		Messages: []llm.Message{
			{Role: "user", Content: "What is 2+2?"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.FinalAnswer != "Final synthesized answer" {
		t.Errorf("final answer = %q", result.FinalAnswer)
	}
	if len(result.Panel) != 1 {
		t.Errorf("panel results = %d, want 1", len(result.Panel))
	}
	if result.Judge == nil {
		t.Error("judge analysis should not be nil")
	}
	if result.DurationMs <= 0 {
		t.Errorf("duration = %d, should be > 0", result.DurationMs)
	}
	if result.ContextWindow != 128000 {
		t.Errorf("context window = %d, want 128000", result.ContextWindow)
	}
}

func TestEngineRun_AllPanelFail(t *testing.T) {
	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": {"message": "internal error"}}`))
	}))
	defer failServer.Close()

	cfg := configWithServers(t, failServer.URL, failServer.URL, failServer.URL)
	eng := NewEngine(cfg)

	_, err := eng.Run(t.Context(), GraftRequest{
		Messages: []llm.Message{
			{Role: "user", Content: "test"},
		},
	})
	if err == nil {
		t.Fatal("expected error when all panel models fail")
	}
	if !strings.Contains(err.Error(), "all panel models failed") {
		t.Errorf("error = %q, should mention 'all panel models failed'", err.Error())
	}
}

func TestEngineRun_UnknownProfile(t *testing.T) {
	cfg := loadTestConfig(t)
	eng := NewEngine(cfg)

	_, err := eng.Run(t.Context(), GraftRequest{
		Profile: "nonexistent",
		Messages: []llm.Message{
			{Role: "user", Content: "test"},
		},
	})
	if err == nil {
		t.Fatal("expected error for unknown profile")
	}
	if !strings.Contains(err.Error(), "unknown profile") {
		t.Errorf("error = %q, should mention 'unknown profile'", err.Error())
	}
}

func configWithServers(t *testing.T, panelURL, judgeURL, finalURL string) *config.Config {
	t.Helper()
	yaml := `
server:
  auth_token: "test-token"
providers:
  panel:
    base_url: "` + panelURL + `"
    api_key: "key"
  judge:
    base_url: "` + judgeURL + `"
    api_key: "key"
  final:
    base_url: "` + finalURL + `"
    api_key: "key"
models:
  panel-model:
    provider: panel
    model: "panel-m"
    context_window: 200000
  judge-model:
    provider: judge
    model: "judge-m"
    context_window: 128000
  final-model:
    provider: final
    model: "final-m"
    context_window: 128000
profiles:
  default:
    panel: ["panel-model"]
    judge: "judge-model"
    final: "final-model"
`
	cfg, err := config.Load(writeTempConfig(t, yaml))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}
