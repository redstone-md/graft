package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// ---------- Types ----------

type Config struct {
	Server    ServerConfig                `yaml:"server"`
	Providers map[string]Provider         `yaml:"providers"`
	Models    map[string]ModelRef         `yaml:"models"`
	Profiles  map[string]PipelineProfile  `yaml:"profiles"`
}

type ServerConfig struct {
	Port      string `yaml:"port"`
	AuthToken string `yaml:"auth_token"`
}

type Provider struct {
	BaseURL string `yaml:"base_url"`
	APIKey  string `yaml:"api_key"`
}

type ModelRef struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
}

type PipelineProfile struct {
	Panel []string `yaml:"panel"`
	Judge string   `yaml:"judge"`
	Final string   `yaml:"final"`
}

// ---------- Loading ----------

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// ---------- Validation ----------

func (c *Config) Validate() error {
	var errs []string

	// --- server ---
	if c.Server.Port == "" {
		c.Server.Port = "8080"
	}
	if c.Server.AuthToken == "" {
		errs = append(errs, "server.auth_token: required (used to authenticate /v1 requests)")
	}

	// --- providers ---
	if len(c.Providers) == 0 {
		errs = append(errs, "providers: at least one provider is required")
	}
	for name, p := range c.Providers {
		prefix := fmt.Sprintf("providers.%s", name)
		if p.BaseURL == "" {
			errs = append(errs, prefix+".base_url: required")
		}
		if p.APIKey == "" {
			errs = append(errs, prefix+".api_key: required")
		}
	}

	// --- models ---
	if len(c.Models) == 0 {
		errs = append(errs, "models: at least one model is required")
	}
	for name, m := range c.Models {
		prefix := fmt.Sprintf("models.%s", name)
		if m.Provider == "" {
			errs = append(errs, prefix+".provider: required (key into providers)")
		} else if _, ok := c.Providers[m.Provider]; !ok {
			errs = append(errs, fmt.Sprintf("%s.provider: unknown provider %q (available: %s)",
				prefix, m.Provider, strings.Join(providerKeys(c), ", ")))
		}
		if m.Model == "" {
			errs = append(errs, prefix+".model: required (model ID on the provider)")
		}
	}

	// --- profiles ---
	if len(c.Profiles) == 0 {
		errs = append(errs, "profiles: at least one pipeline profile is required")
	}
	for name, fp := range c.Profiles {
		prefix := fmt.Sprintf("profiles.%s", name)

		if len(fp.Panel) == 0 {
			errs = append(errs, prefix+".panel: at least one model ref is required")
		}
		for i, ref := range fp.Panel {
			if _, ok := c.Models[ref]; !ok {
				errs = append(errs, fmt.Sprintf("%s.panel[%d]: unknown model %q (available: %s)",
					prefix, i, ref, strings.Join(modelKeys(c), ", ")))
			}
		}
		if fp.Judge == "" {
			errs = append(errs, prefix+".judge: required (model ref)")
		} else if _, ok := c.Models[fp.Judge]; !ok {
			errs = append(errs, fmt.Sprintf("%s.judge: unknown model %q", prefix, fp.Judge))
		}
		if fp.Final == "" {
			errs = append(errs, prefix+".final: required (model ref)")
		} else if _, ok := c.Models[fp.Final]; !ok {
			errs = append(errs, fmt.Sprintf("%s.final: unknown model %q", prefix, fp.Final))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation failed:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}

// ---------- Helpers ----------

func (c *Config) ResolveModel(ref string) (baseURL, apiKey, modelID string, err error) {
	m, ok := c.Models[ref]
	if !ok {
		return "", "", "", fmt.Errorf("unknown model: %q", ref)
	}
	p, ok := c.Providers[m.Provider]
	if !ok {
		return "", "", "", fmt.Errorf("unknown provider: %q", m.Provider)
	}
	return p.BaseURL, p.APIKey, m.Model, nil
}

func providerKeys(c *Config) []string {
	keys := make([]string, 0, len(c.Providers))
	for k := range c.Providers {
		keys = append(keys, k)
	}
	return keys
}

func modelKeys(c *Config) []string {
	keys := make([]string, 0, len(c.Models))
	for k := range c.Models {
		keys = append(keys, k)
	}
	return keys
}
