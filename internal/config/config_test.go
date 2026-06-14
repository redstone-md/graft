package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------- Helpers ----------

func validYAML(t *testing.T) string {
	t.Helper()
	return `
server:
  port: "8080"
  auth_token: "test-token"
providers:
  openrouter:
    base_url: "https://openrouter.ai/api/v1"
    api_key: "sk-or-xxx"
  openai:
    base_url: "https://api.openai.com/v1"
    api_key: "sk-xxx"
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
    provider: openai
    model: "gpt-4o-mini"
    context_window: 128000
profiles:
  default:
    panel: ["claude", "gpt4"]
    judge: "gpt4-mini"
    final: "claude"
`
}

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

// ---------- Load ----------

func TestLoad_ValidConfig(t *testing.T) {
	cfg, err := Load(writeTemp(t, validYAML(t)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Server.Port != "8080" {
		t.Errorf("port = %q, want %q", cfg.Server.Port, "8080")
	}
	if cfg.Server.AuthToken != "test-token" {
		t.Errorf("auth_token = %q, want %q", cfg.Server.AuthToken, "test-token")
	}
	if len(cfg.Providers) != 2 {
		t.Errorf("providers = %d, want 2", len(cfg.Providers))
	}
	if len(cfg.Models) != 3 {
		t.Errorf("models = %d, want 3", len(cfg.Models))
	}
	if len(cfg.Profiles) != 1 {
		t.Errorf("profiles = %d, want 1", len(cfg.Profiles))
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
	if !strings.Contains(err.Error(), "read config") {
		t.Errorf("error = %q, should contain 'read config'", err.Error())
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	_, err := Load(writeTemp(t, "server: {{invalid yaml"))
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
	if !strings.Contains(err.Error(), "parse yaml") {
		t.Errorf("error = %q, should contain 'parse yaml'", err.Error())
	}
}

// ---------- Validate ----------

func TestValidate_MissingAuthToken(t *testing.T) {
	yaml := `
server:
  port: "8080"
providers:
  openrouter:
    base_url: "https://openrouter.ai/api/v1"
    api_key: "sk-or-xxx"
models:
  claude:
    provider: openrouter
    model: "anthropic/claude-3.5-sonnet"
profiles:
  default:
    panel: ["claude"]
    judge: "claude"
    final: "claude"
`
	err := Validate_from_yaml(t, yaml)
	if err == nil {
		t.Fatal("expected error for missing auth_token")
	}
	if !strings.Contains(err.Error(), "auth_token") {
		t.Errorf("error = %q, should mention auth_token", err.Error())
	}
}

func TestValidate_MissingProviders(t *testing.T) {
	yaml := `
server:
  auth_token: "test"
providers: {}
models:
  claude:
    provider: openrouter
    model: "anthropic/claude-3.5-sonnet"
profiles:
  default:
    panel: ["claude"]
    judge: "claude"
    final: "claude"
`
	err := Validate_from_yaml(t, yaml)
	if err == nil {
		t.Fatal("expected error for empty providers")
	}
	if !strings.Contains(err.Error(), "providers") {
		t.Errorf("error = %q, should mention providers", err.Error())
	}
}

func TestValidate_ProviderMissingBaseURL(t *testing.T) {
	yaml := `
server:
  auth_token: "test"
providers:
  openrouter:
    api_key: "sk-or-xxx"
models:
  claude:
    provider: openrouter
    model: "anthropic/claude-3.5-sonnet"
profiles:
  default:
    panel: ["claude"]
    judge: "claude"
    final: "claude"
`
	err := Validate_from_yaml(t, yaml)
	if err == nil {
		t.Fatal("expected error for missing base_url")
	}
	if !strings.Contains(err.Error(), "base_url") {
		t.Errorf("error = %q, should mention base_url", err.Error())
	}
}

func TestValidate_ProviderMissingAPIKey(t *testing.T) {
	yaml := `
server:
  auth_token: "test"
providers:
  openrouter:
    base_url: "https://openrouter.ai/api/v1"
models:
  claude:
    provider: openrouter
    model: "anthropic/claude-3.5-sonnet"
profiles:
  default:
    panel: ["claude"]
    judge: "claude"
    final: "claude"
`
	err := Validate_from_yaml(t, yaml)
	if err == nil {
		t.Fatal("expected error for missing api_key")
	}
	if !strings.Contains(err.Error(), "api_key") {
		t.Errorf("error = %q, should mention api_key", err.Error())
	}
}

func TestValidate_MissingModels(t *testing.T) {
	yaml := `
server:
  auth_token: "test"
providers:
  openrouter:
    base_url: "https://openrouter.ai/api/v1"
    api_key: "sk-or-xxx"
models: {}
profiles:
  default:
    panel: ["claude"]
    judge: "claude"
    final: "claude"
`
	err := Validate_from_yaml(t, yaml)
	if err == nil {
		t.Fatal("expected error for empty models")
	}
	if !strings.Contains(err.Error(), "models") {
		t.Errorf("error = %q, should mention models", err.Error())
	}
}

func TestValidate_ModelUnknownProvider(t *testing.T) {
	yaml := `
server:
  auth_token: "test"
providers:
  openrouter:
    base_url: "https://openrouter.ai/api/v1"
    api_key: "sk-or-xxx"
models:
  claude:
    provider: nonexistent
    model: "anthropic/claude-3.5-sonnet"
profiles:
  default:
    panel: ["claude"]
    judge: "claude"
    final: "claude"
`
	err := Validate_from_yaml(t, yaml)
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
	if !strings.Contains(err.Error(), "unknown provider") {
		t.Errorf("error = %q, should mention 'unknown provider'", err.Error())
	}
}

func TestValidate_ModelEmptyModel(t *testing.T) {
	yaml := `
server:
  auth_token: "test"
providers:
  openrouter:
    base_url: "https://openrouter.ai/api/v1"
    api_key: "sk-or-xxx"
models:
  claude:
    provider: openrouter
profiles:
  default:
    panel: ["claude"]
    judge: "claude"
    final: "claude"
`
	err := Validate_from_yaml(t, yaml)
	if err == nil {
		t.Fatal("expected error for empty model")
	}
	if !strings.Contains(err.Error(), ".model: required") {
		t.Errorf("error = %q, should mention '.model: required'", err.Error())
	}
}

func TestValidate_EmptyProfiles(t *testing.T) {
	yaml := `
server:
  auth_token: "test"
providers:
  openrouter:
    base_url: "https://openrouter.ai/api/v1"
    api_key: "sk-or-xxx"
models:
  claude:
    provider: openrouter
    model: "anthropic/claude-3.5-sonnet"
profiles: {}
`
	err := Validate_from_yaml(t, yaml)
	if err == nil {
		t.Fatal("expected error for empty profiles")
	}
	if !strings.Contains(err.Error(), "profiles") {
		t.Errorf("error = %q, should mention profiles", err.Error())
	}
}

func TestValidate_ProfileEmptyPanel(t *testing.T) {
	yaml := `
server:
  auth_token: "test"
providers:
  openrouter:
    base_url: "https://openrouter.ai/api/v1"
    api_key: "sk-or-xxx"
models:
  claude:
    provider: openrouter
    model: "anthropic/claude-3.5-sonnet"
profiles:
  default:
    panel: []
    judge: "claude"
    final: "claude"
`
	err := Validate_from_yaml(t, yaml)
	if err == nil {
		t.Fatal("expected error for empty panel")
	}
	if !strings.Contains(err.Error(), "panel") {
		t.Errorf("error = %q, should mention panel", err.Error())
	}
}

func TestValidate_ProfileUnknownPanelModel(t *testing.T) {
	yaml := `
server:
  auth_token: "test"
providers:
  openrouter:
    base_url: "https://openrouter.ai/api/v1"
    api_key: "sk-or-xxx"
models:
  claude:
    provider: openrouter
    model: "anthropic/claude-3.5-sonnet"
profiles:
  default:
    panel: ["nonexistent"]
    judge: "claude"
    final: "claude"
`
	err := Validate_from_yaml(t, yaml)
	if err == nil {
		t.Fatal("expected error for unknown panel model")
	}
	if !strings.Contains(err.Error(), "unknown model") {
		t.Errorf("error = %q, should mention 'unknown model'", err.Error())
	}
}

func TestValidate_ProfileMissingJudge(t *testing.T) {
	yaml := `
server:
  auth_token: "test"
providers:
  openrouter:
    base_url: "https://openrouter.ai/api/v1"
    api_key: "sk-or-xxx"
models:
  claude:
    provider: openrouter
    model: "anthropic/claude-3.5-sonnet"
profiles:
  default:
    panel: ["claude"]
    judge: ""
    final: "claude"
`
	err := Validate_from_yaml(t, yaml)
	if err == nil {
		t.Fatal("expected error for missing judge")
	}
	if !strings.Contains(err.Error(), "judge") {
		t.Errorf("error = %q, should mention judge", err.Error())
	}
}

func TestValidate_ProfileUnknownJudge(t *testing.T) {
	yaml := `
server:
  auth_token: "test"
providers:
  openrouter:
    base_url: "https://openrouter.ai/api/v1"
    api_key: "sk-or-xxx"
models:
  claude:
    provider: openrouter
    model: "anthropic/claude-3.5-sonnet"
profiles:
  default:
    panel: ["claude"]
    judge: "nonexistent"
    final: "claude"
`
	err := Validate_from_yaml(t, yaml)
	if err == nil {
		t.Fatal("expected error for unknown judge model")
	}
	if !strings.Contains(err.Error(), "unknown model") {
		t.Errorf("error = %q, should mention 'unknown model'", err.Error())
	}
}

func TestValidate_ProfileMissingFinal(t *testing.T) {
	yaml := `
server:
  auth_token: "test"
providers:
  openrouter:
    base_url: "https://openrouter.ai/api/v1"
    api_key: "sk-or-xxx"
models:
  claude:
    provider: openrouter
    model: "anthropic/claude-3.5-sonnet"
profiles:
  default:
    panel: ["claude"]
    judge: "claude"
    final: ""
`
	err := Validate_from_yaml(t, yaml)
	if err == nil {
		t.Fatal("expected error for missing final")
	}
	if !strings.Contains(err.Error(), "final") {
		t.Errorf("error = %q, should mention final", err.Error())
	}
}

func TestValidate_ProfileUnknownFinal(t *testing.T) {
	yaml := `
server:
  auth_token: "test"
providers:
  openrouter:
    base_url: "https://openrouter.ai/api/v1"
    api_key: "sk-or-xxx"
models:
  claude:
    provider: openrouter
    model: "anthropic/claude-3.5-sonnet"
profiles:
  default:
    panel: ["claude"]
    judge: "claude"
    final: "nonexistent"
`
	err := Validate_from_yaml(t, yaml)
	if err == nil {
		t.Fatal("expected error for unknown final model")
	}
	if !strings.Contains(err.Error(), "unknown model") {
		t.Errorf("error = %q, should mention 'unknown model'", err.Error())
	}
}

func TestValidate_MultipleErrors(t *testing.T) {
	yaml := `
server: {}
providers: {}
models: {}
profiles: {}
`
	err := Validate_from_yaml(t, yaml)
	if err == nil {
		t.Fatal("expected multiple validation errors")
	}
	msg := err.Error()
	// Should contain multiple error indicators
	for _, want := range []string{"auth_token", "providers", "models", "profiles"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error should mention %q, got: %s", want, msg)
		}
	}
}

func TestValidate_DefaultPort(t *testing.T) {
	yaml := `
server:
  auth_token: "test"
providers:
  openrouter:
    base_url: "https://openrouter.ai/api/v1"
    api_key: "sk-or-xxx"
models:
  claude:
    provider: openrouter
    model: "anthropic/claude-3.5-sonnet"
profiles:
  default:
    panel: ["claude"]
    judge: "claude"
    final: "claude"
`
	cfg, err := Load(writeTemp(t, yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Server.Port != "8080" {
		t.Errorf("default port = %q, want %q", cfg.Server.Port, "8080")
	}
}

// ---------- ResolveModel ----------

func TestResolveModel_Valid(t *testing.T) {
	cfg, err := Load(writeTemp(t, validYAML(t)))
	if err != nil {
		t.Fatal(err)
	}

	baseURL, apiKey, modelID, err := cfg.ResolveModel("claude")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if baseURL != "https://openrouter.ai/api/v1" {
		t.Errorf("baseURL = %q", baseURL)
	}
	if apiKey != "sk-or-xxx" {
		t.Errorf("apiKey = %q", apiKey)
	}
	if modelID != "anthropic/claude-3.5-sonnet" {
		t.Errorf("modelID = %q", modelID)
	}
}

func TestResolveModel_Unknown(t *testing.T) {
	cfg, err := Load(writeTemp(t, validYAML(t)))
	if err != nil {
		t.Fatal(err)
	}

	_, _, _, err = cfg.ResolveModel("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown model")
	}
	if !strings.Contains(err.Error(), "unknown model") {
		t.Errorf("error = %q, should mention 'unknown model'", err.Error())
	}
}

// ---------- ResolveModelFull ----------

func TestResolveModelFull_Valid(t *testing.T) {
	cfg, err := Load(writeTemp(t, validYAML(t)))
	if err != nil {
		t.Fatal(err)
	}

	ref, err := cfg.ResolveModelFull("claude")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ref.Provider != "openrouter" {
		t.Errorf("provider = %q, want %q", ref.Provider, "openrouter")
	}
	if ref.Model != "anthropic/claude-3.5-sonnet" {
		t.Errorf("model = %q", ref.Model)
	}
	if ref.ContextWindow != 200000 {
		t.Errorf("context_window = %d, want 200000", ref.ContextWindow)
	}
}

func TestResolveModelFull_Unknown(t *testing.T) {
	cfg, err := Load(writeTemp(t, validYAML(t)))
	if err != nil {
		t.Fatal(err)
	}

	_, err = cfg.ResolveModelFull("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown model")
	}
}

// ---------- MinContextWindow ----------

func TestMinContextWindow_AllHaveWindows(t *testing.T) {
	cfg, err := Load(writeTemp(t, validYAML(t)))
	if err != nil {
		t.Fatal(err)
	}

	min := cfg.MinContextWindow([]string{"claude", "gpt4", "gpt4-mini"})
	if min != 128000 {
		t.Errorf("min = %d, want 128000", min)
	}
}

func TestMinContextWindow_OneUnknown(t *testing.T) {
	yaml := `
server:
  auth_token: "test"
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
    context_window: 0
profiles:
  default:
    panel: ["claude", "gpt4"]
    judge: "claude"
    final: "claude"
`
	cfg, err := Load(writeTemp(t, yaml))
	if err != nil {
		t.Fatal(err)
	}

	min := cfg.MinContextWindow([]string{"claude", "gpt4"})
	if min != 200000 {
		t.Errorf("min = %d, want 200000 (unknown context models are skipped)", min)
	}
}

func TestMinContextWindow_EmptyRefs(t *testing.T) {
	cfg, err := Load(writeTemp(t, validYAML(t)))
	if err != nil {
		t.Fatal(err)
	}

	min := cfg.MinContextWindow([]string{})
	if min != 0 {
		t.Errorf("min = %d, want 0 for empty refs", min)
	}
}

func TestMinContextWindow_SingleRef(t *testing.T) {
	cfg, err := Load(writeTemp(t, validYAML(t)))
	if err != nil {
		t.Fatal(err)
	}

	min := cfg.MinContextWindow([]string{"claude"})
	if min != 200000 {
		t.Errorf("min = %d, want 200000", min)
	}
}

// ---------- Helper ----------

// Validate_from_yaml is a test helper that loads YAML and validates.
func Validate_from_yaml(t *testing.T, yaml string) error {
	t.Helper()
	path := writeTemp(t, yaml)
	_, err := Load(path)
	return err
}
