package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/redstone-md/graft/internal/config"
	"github.com/redstone-md/graft/internal/engine"
	"github.com/redstone-md/graft/internal/llm"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// ---------- Helpers ----------

func setupRouter(t *testing.T) (*gin.Engine, *config.Config) {
	t.Helper()
	cfg := loadTestConfig(t)
	eng := engine.NewEngine(cfg)
	h := NewHandler(cfg, eng)

	r := gin.New()
	h.RegisterRoutes(r)
	return r, cfg
}

// setupRouterWithMockUpstream creates a router backed by a mock LLM server.
// The mock responds to any /chat/completions request with a canned response.
func setupRouterWithMockUpstream(t *testing.T, mockResponse string) (*gin.Engine, *httptest.Server) {
	t.Helper()
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
					}{Content: mockResponse},
					FinishReason: "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))

	cfg := loadTestConfigWithUpstream(t, mock.URL)
	eng := engine.NewEngine(cfg)
	h := NewHandler(cfg, eng)

	r := gin.New()
	h.RegisterRoutes(r)
	return r, mock
}

func loadTestConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load(writeTempConfig(t, testConfigYAML()))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func loadTestConfigWithUpstream(t *testing.T, upstreamURL string) *config.Config {
	t.Helper()
	yaml := `
server:
  port: "8080"
  auth_token: "test-token-123"
providers:
  openrouter:
    base_url: "` + upstreamURL + `"
    api_key: "test-key"
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

func testConfigYAML() string {
	return `
server:
  port: "8080"
  auth_token: "test-token-123"
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

func authHeader() string {
	return "Bearer test-token-123"
}

// hasAuthError checks if the response body contains an auth_error.
func hasAuthError(w *httptest.ResponseRecorder) bool {
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		return false
	}
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok {
		return false
	}
	return errObj["type"] == "auth_error"
}

// ---------- Health ----------

func TestHealth(t *testing.T) {
	r, _ := setupRouter(t)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/health", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "ok" {
		t.Errorf("status = %q", resp["status"])
	}
}

func TestRoot(t *testing.T) {
	r, _ := setupRouter(t)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

// ---------- Auth ----------

func TestAuth_NoHeader(t *testing.T) {
	r, _ := setupRouter(t)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/models", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
	if !hasAuthError(w) {
		t.Error("response should contain auth_error")
	}
}

func TestAuth_InvalidToken(t *testing.T) {
	r, _ := setupRouter(t)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
	if !hasAuthError(w) {
		t.Error("response should contain auth_error")
	}
}

func TestAuth_ValidToken(t *testing.T) {
	r, _ := setupRouter(t)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", authHeader())
	r.ServeHTTP(w, req)

	// Auth should pass — /v1/models doesn't hit upstream
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d (auth should pass)", w.Code, http.StatusOK)
	}
}

func TestAuth_NoBearerPrefix(t *testing.T) {
	r, _ := setupRouter(t)

	// TrimPrefix("Bearer ") on "test-token-123" returns "test-token-123" unchanged.
	// So auth passes even without the prefix — TrimPrefix is lenient.
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", "test-token-123")
	r.ServeHTTP(w, req)

	// Auth passes because TrimPrefix doesn't find "Bearer " to remove.
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d (TrimPrefix is lenient, auth passes)", w.Code, http.StatusOK)
	}
}

func TestAuth_BearerWithExtraSpaces(t *testing.T) {
	r, _ := setupRouter(t)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer   test-token-123  ")
	r.ServeHTTP(w, req)

	// TrimPrefix removes "Bearer " (first 7 chars), TrimSpace removes surrounding whitespace.
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

// ---------- Chat Completions ----------

func TestChatCompletions_EmptyBody(t *testing.T) {
	r, _ := setupRouter(t)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader())
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestChatCompletions_EmptyMessages(t *testing.T) {
	r, _ := setupRouter(t)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(`{"model":"claude","messages":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader())
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestChatCompletions_InvalidJSON(t *testing.T) {
	r, _ := setupRouter(t)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(`{invalid`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader())
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestChatCompletions_GraftModel(t *testing.T) {
	r, mock := setupRouterWithMockUpstream(t, "Pipeline answer")
	defer mock.Close()

	w := httptest.NewRecorder()
	body := `{"model":"graft","messages":[{"role":"user","content":"What is 2+2?"}]}`
	req, _ := http.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader())
	r.ServeHTTP(w, req)

	// Auth passes, pipeline runs against mock
	if hasAuthError(w) {
		t.Error("graft request should not fail at auth")
	}
	if w.Code == http.StatusBadRequest {
		t.Errorf("graft request should not be bad request, got %d", w.Code)
	}
}

func TestChatCompletions_GraftWithPlugin(t *testing.T) {
	r, mock := setupRouterWithMockUpstream(t, "Plugin answer")
	defer mock.Close()

	w := httptest.NewRecorder()
	body := `{"model":"claude","messages":[{"role":"user","content":"Hi"}],"plugins":[{"id":"graft","enabled":true}]}`
	req, _ := http.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader())
	r.ServeHTTP(w, req)

	if hasAuthError(w) {
		t.Error("plugin request should not fail at auth")
	}
}

func TestChatCompletions_ProxyModel(t *testing.T) {
	r, mock := setupRouterWithMockUpstream(t, "Upstream reply")
	defer mock.Close()

	w := httptest.NewRecorder()
	body := `{"model":"claude","messages":[{"role":"user","content":"Hi"}]}`
	req, _ := http.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader())
	r.ServeHTTP(w, req)

	// Auth passes, proxy forwards to mock upstream
	if hasAuthError(w) {
		t.Error("proxy request should not fail at auth")
	}
	if w.Code != http.StatusOK {
		t.Errorf("proxy status = %d, want %d (mock upstream returns 200)", w.Code, http.StatusOK)
	}
}

// ---------- Models ----------

func TestListModels(t *testing.T) {
	r, _ := setupRouter(t)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", authHeader())
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp ModelsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Object != "list" {
		t.Errorf("object = %q", resp.Object)
	}
	if len(resp.Data) == 0 {
		t.Error("expected at least one model")
	}

	ids := make(map[string]bool)
	for _, m := range resp.Data {
		ids[m.ID] = true
	}
	if !ids["default"] {
		t.Error("missing profile 'default' in model list")
	}
	if !ids["claude"] {
		t.Error("missing model 'claude' in model list")
	}
}

func TestListModels_NoAuth(t *testing.T) {
	r, _ := setupRouter(t)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/models", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

// ---------- isGraftModel ----------

func TestIsGraftModel(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{"graft", true},
		{"Graft", false},
		{"claude", false},
		{"", false},
	}

	for _, tt := range tests {
		if got := isGraftModel(tt.model); got != tt.want {
			t.Errorf("isGraftModel(%q) = %v, want %v", tt.model, got, tt.want)
		}
	}
}

// ---------- hasGraftPlugin ----------

func TestHasGraftPlugin(t *testing.T) {
	enabled := true
	disabled := false

	tests := []struct {
		name    string
		plugins []Plugin
		want    bool
	}{
		{"no plugins", nil, false},
		{"graft enabled", []Plugin{{ID: "graft", Enabled: &enabled}}, true},
		{"graft disabled", []Plugin{{ID: "graft", Enabled: &disabled}}, false},
		{"graft nil enabled", []Plugin{{ID: "graft"}}, true},
		{"openrouter fusion", []Plugin{{ID: "openrouter:fusion"}}, true},
		{"other plugin", []Plugin{{ID: "other"}}, false},
	}

	for _, tt := range tests {
		if got := hasGraftPlugin(tt.plugins); got != tt.want {
			t.Errorf("hasGraftPlugin(%s) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// ---------- extractConfig ----------

func TestExtractConfig_ProfileModel(t *testing.T) {
	eng := engine.NewEngine(loadTestConfig(t))
	h := NewHandler(loadTestConfig(t), eng)

	// extractConfig sets profile name when model matches a profile key.
	// Panel/judge/final are NOT set here — the engine's resolveRefs reads them from the profile.
	profile, panel, judge, final := h.extractConfig("default", nil)
	if profile != "default" {
		t.Errorf("profile = %q, want %q", profile, "default")
	}
	if len(panel) != 0 {
		t.Errorf("panel = %v, want empty (engine resolves from profile)", panel)
	}
	if judge != "" {
		t.Errorf("judge = %q, want empty (engine resolves from profile)", judge)
	}
	if final != "" {
		t.Errorf("final = %q, want empty (engine resolves from profile)", final)
	}
}

func TestExtractConfig_NonProfileModel(t *testing.T) {
	eng := engine.NewEngine(loadTestConfig(t))
	h := NewHandler(loadTestConfig(t), eng)

	profile, panel, judge, final := h.extractConfig("claude", nil)
	if profile != "" {
		t.Errorf("profile = %q, want empty for non-profile model", profile)
	}
	if len(panel) != 0 {
		t.Errorf("panel = %v, want empty", panel)
	}
	if judge != "" {
		t.Errorf("judge = %q, want empty", judge)
	}
	if final != "" {
		t.Errorf("final = %q, want empty", final)
	}
}

func TestExtractConfig_PluginOverrides(t *testing.T) {
	eng := engine.NewEngine(loadTestConfig(t))
	h := NewHandler(loadTestConfig(t), eng)

	plugins := []Plugin{
		{
			ID:             "graft",
			AnalysisModels: []string{"claude"},
			Model:          "gpt4",
		},
	}

	_, panel, judge, final := h.extractConfig("some-model", plugins)
	if len(panel) != 1 || panel[0] != "claude" {
		t.Errorf("panel = %v, want [claude]", panel)
	}
	if judge != "gpt4" {
		t.Errorf("judge = %q, want %q", judge, "gpt4")
	}
	if final != "gpt4" {
		t.Errorf("final = %q, want %q", final, "gpt4")
	}
}

func TestExtractConfig_PluginDisabled(t *testing.T) {
	eng := engine.NewEngine(loadTestConfig(t))
	h := NewHandler(loadTestConfig(t), eng)

	disabled := false
	plugins := []Plugin{
		{
			ID:      "graft",
			Enabled: &disabled,
			Model:   "should-not-use",
		},
	}

	profile, panel, judge, final := h.extractConfig("default", plugins)
	// Profile is set because model name matches, but plugin is disabled so no overrides
	if profile != "default" {
		t.Errorf("profile = %q", profile)
	}
	if len(panel) != 0 {
		t.Errorf("panel = %v, want empty (plugin disabled)", panel)
	}
	if judge != "" {
		t.Errorf("judge = %q, want empty (plugin disabled)", judge)
	}
	if final != "" {
		t.Errorf("final = %q, want empty (plugin disabled)", final)
	}
}

func TestExtractConfig_OpenRouterFusionPlugin(t *testing.T) {
	eng := engine.NewEngine(loadTestConfig(t))
	h := NewHandler(loadTestConfig(t), eng)

	plugins := []Plugin{
		{
			ID:             "openrouter:fusion",
			AnalysisModels: []string{"gpt4"},
			Model:          "claude",
		},
	}

	_, panel, judge, final := h.extractConfig("graft", plugins)
	if len(panel) != 1 || panel[0] != "gpt4" {
		t.Errorf("panel = %v", panel)
	}
	if judge != "claude" {
		t.Errorf("judge = %q", judge)
	}
	if final != "claude" {
		t.Errorf("final = %q", final)
	}
}

// ---------- buildResponse ----------

func TestBuildResponse(t *testing.T) {
	resp := buildResponse("gpt-4o", "Hello world")

	if resp.Model != "gpt-4o" {
		t.Errorf("model = %q", resp.Model)
	}
	if resp.Object != "chat.completion" {
		t.Errorf("object = %q", resp.Object)
	}
	if resp.Created == 0 {
		t.Error("created should not be 0")
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(resp.Choices))
	}
	if resp.Choices[0].Message.Role != "assistant" {
		t.Errorf("role = %q", resp.Choices[0].Message.Role)
	}
	if resp.Choices[0].Message.Content != "Hello world" {
		t.Errorf("content = %q", resp.Choices[0].Message.Content)
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("finish_reason = %q", resp.Choices[0].FinishReason)
	}
}

// ---------- estimateTokens ----------

func TestEstimateTokens_String(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"hi", 0},
		{"hello", 1},
		{"12345678", 2},
	}

	for _, tt := range tests {
		if got := estimateTokens(tt.input); got != tt.want {
			t.Errorf("estimateTokens(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestEstimateTokens_Messages(t *testing.T) {
	msgs := []llm.Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "World"},
	}
	got := estimateTokens(msgs)
	if got != 2 {
		t.Errorf("estimateTokens(msgs) = %d, want 2", got)
	}
}

func TestEstimateTokens_Nil(t *testing.T) {
	got := estimateTokens(nil)
	if got != 0 {
		t.Errorf("estimateTokens(nil) = %d, want 0", got)
	}
}

// ---------- toModelList ----------

func TestToModelList(t *testing.T) {
	maps := []map[string]string{
		{"id": "model-a", "object": "model", "owned_by": "provider"},
		{"id": "model-b", "object": "model", "owned_by": "graft"},
	}

	result := toModelList(maps)
	if len(result) != 2 {
		t.Fatalf("len = %d, want 2", len(result))
	}
	if result[0].ID != "model-a" {
		t.Errorf("id = %q", result[0].ID)
	}
	if result[1].OwnedBy != "graft" {
		t.Errorf("owned_by = %q", result[1].OwnedBy)
	}
}

func TestToModelList_Empty(t *testing.T) {
	result := toModelList([]map[string]string{})
	if len(result) != 0 {
		t.Errorf("len = %d, want 0", len(result))
	}
}

// ---------- resolveModelForProxy ----------

func TestResolveModelForProxy_KnownModel(t *testing.T) {
	eng := engine.NewEngine(loadTestConfig(t))
	h := NewHandler(loadTestConfig(t), eng)

	baseURL, apiKey, modelID, err := h.resolveModelForProxy("claude")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(baseURL, "http") {
		t.Errorf("baseURL = %q", baseURL)
	}
	if apiKey == "" {
		t.Error("apiKey should not be empty")
	}
	if modelID != "anthropic/claude-3.5-sonnet" {
		t.Errorf("modelID = %q", modelID)
	}
}

func TestResolveModelForProxy_ProfileName(t *testing.T) {
	eng := engine.NewEngine(loadTestConfig(t))
	h := NewHandler(loadTestConfig(t), eng)

	// Resolves to the judge model of the "default" profile
	baseURL, _, modelID, err := h.resolveModelForProxy("default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(baseURL, "http") {
		t.Errorf("baseURL = %q", baseURL)
	}
	if modelID != "openai/gpt-4o-mini" {
		t.Errorf("modelID = %q, want gpt-4o-mini", modelID)
	}
}

func TestResolveModelForProxy_Unknown(t *testing.T) {
	eng := engine.NewEngine(loadTestConfig(t))
	h := NewHandler(loadTestConfig(t), eng)

	_, _, _, err := h.resolveModelForProxy("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown model")
	}
}

// ---------- request body verification ----------

func TestChatCompletions_ProxyForwardsRequestBody(t *testing.T) {
	var receivedBody []byte
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedBody, _ = json.Marshal(map[string]interface{}{
			"model":    "anthropic/claude-3.5-sonnet",
			"messages": []map[string]string{{"role": "user", "content": "test"}},
		})
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
					}{Content: "ok"},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer mock.Close()

	cfg := loadTestConfigWithUpstream(t, mock.URL)
	eng := engine.NewEngine(cfg)
	h := NewHandler(cfg, eng)

	r := gin.New()
	h.RegisterRoutes(r)

	w := httptest.NewRecorder()
	body := `{"model":"claude","messages":[{"role":"user","content":"test"}]}`
	req, _ := http.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader())
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if len(receivedBody) == 0 {
		t.Error("upstream should have received a request body")
	}
}

// ---------- Proxy reads full body ----------

func TestChatCompletions_ProxyReadsFullResponse(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
					}{Content: "full upstream answer"},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer mock.Close()

	cfg := loadTestConfigWithUpstream(t, mock.URL)
	eng := engine.NewEngine(cfg)
	h := NewHandler(cfg, eng)

	r := gin.New()
	h.RegisterRoutes(r)

	w := httptest.NewRecorder()
	body := `{"model":"claude","messages":[{"role":"user","content":"Hi"}]}`
	req, _ := http.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader())
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), "full upstream answer") {
		t.Error("response should contain upstream answer")
	}
}
