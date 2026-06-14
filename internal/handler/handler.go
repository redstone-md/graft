package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redstone-md/graft/internal/config"
	"github.com/redstone-md/graft/internal/engine"
	"github.com/redstone-md/graft/internal/llm"
)

// ---------- OpenAI-compatible types ----------

type ChatCompletionRequest struct {
	Model       string          `json:"model"`
	Messages    []llm.Message   `json:"messages"`
	Stream      bool            `json:"stream,omitempty"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
	TopP        *float64        `json:"top_p,omitempty"`
	Stop        interface{}     `json:"stop,omitempty"`
	Tools       json.RawMessage `json:"tools,omitempty"`
	ToolChoice  interface{}     `json:"tool_choice,omitempty"`
	Plugins     []Plugin        `json:"plugins,omitempty"`
}

type Plugin struct {
	ID             string   `json:"id"`
	Enabled        *bool    `json:"enabled,omitempty"`
	AnalysisModels []string `json:"analysis_models,omitempty"`
	Model          string   `json:"model,omitempty"`
}

type ChatCompletionResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

type ModelsResponse struct {
	Object string `json:"object"`
	Data   []struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		OwnedBy string `json:"owned_by"`
	} `json:"data"`
}

// ---------- Handler ----------

type Handler struct {
	cfg    *config.Config
	llm    *llm.Client
	engine *engine.Engine
}

func NewHandler(cfg *config.Config, eng *engine.Engine) *Handler {
	return &Handler{
		cfg:    cfg,
		llm:    llm.NewClient(),
		engine: eng,
	}
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	v1 := r.Group("/v1")
	v1.Use(h.authMiddleware())
	{
		v1.POST("/chat/completions", h.chatCompletions)
		v1.GET("/models", h.listModels)
	}

	r.GET("/health", h.health)
	r.GET("/", h.health)
}

// ---------- Auth ----------

func (h *Handler) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		auth := c.GetHeader("Authorization")
		if auth == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{"message": "Authorization header required", "type": "auth_error"},
			})
			return
		}

		token := strings.TrimPrefix(auth, "Bearer ")
		token = strings.TrimSpace(token)

		if token != h.cfg.Server.AuthToken {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": gin.H{"message": "invalid API key", "type": "auth_error"},
			})
			return
		}

		c.Next()
	}
}

// ---------- Routes ----------

func (h *Handler) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) listModels(c *gin.Context) {
	var models []map[string]string

	for name := range h.cfg.Profiles {
		models = append(models, map[string]string{
			"id": name, "object": "model", "owned_by": "graft",
		})
	}
	for name := range h.cfg.Models {
		models = append(models, map[string]string{
			"id": name, "object": "model", "owned_by": "provider",
		})
	}

	c.JSON(http.StatusOK, ModelsResponse{Object: "list", Data: toModelList(models)})
}

func (h *Handler) chatCompletions(c *gin.Context) {
	var req ChatCompletionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"message": err.Error(), "type": "invalid_request_error"},
		})
		return
	}

	if len(req.Messages) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"message": "messages is required", "type": "invalid_request_error"},
		})
		return
	}

	isGraft := isGraftModel(req.Model) || hasGraftPlugin(req.Plugins)

	if isGraft {
		if req.Stream {
			h.handleGraftStream(c, req)
		} else {
			h.handleGraft(c, req)
		}
	} else {
		h.handleProxy(c, req)
	}
}

// ---------- Graft pipeline (non-streaming) ----------

func (h *Handler) handleGraft(c *gin.Context, req ChatCompletionRequest) {
	profile, panelModels, judgeModel, finalModel := h.extractConfig(req.Model, req.Plugins)

	result, err := h.engine.Run(c.Request.Context(), engine.GraftRequest{
		Messages:    req.Messages,
		Profile:     profile,
		PanelModels: panelModels,
		JudgeModel:  judgeModel,
		FinalModel:  finalModel,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"message": err.Error(), "type": "server_error"},
		})
		return
	}

	resp := buildResponse(req.Model, result.FinalAnswer)
	resp.Usage.PromptTokens = estimateTokens(req.Messages)
	resp.Usage.CompletionTokens = estimateTokens(result.FinalAnswer)
	resp.Usage.TotalTokens = resp.Usage.PromptTokens + resp.Usage.CompletionTokens

	c.JSON(http.StatusOK, resp)
}

// ---------- Graft pipeline (streaming) ----------

func (h *Handler) handleGraftStream(c *gin.Context, req ChatCompletionRequest) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"message": "streaming not supported", "type": "server_error"},
		})
		return
	}

	profile, panelModels, judgeModel, finalModel := h.extractConfig(req.Model, req.Plugins)

	h.engine.RunStream(c.Request.Context(), engine.GraftRequest{
		Messages:    req.Messages,
		Profile:     profile,
		PanelModels: panelModels,
		JudgeModel:  judgeModel,
		FinalModel:  finalModel,
		Stream:      true,
	}, c.Writer, flusher)
}

// ---------- Proxy (non-graft) ----------

func (h *Handler) handleProxy(c *gin.Context, req ChatCompletionRequest) {
	body, _ := json.Marshal(req)

	baseURL, apiKey, modelID, err := h.resolveModelForProxy(req.Model)
	if err != nil {
		for _, p := range h.cfg.Providers {
			baseURL, apiKey, modelID = p.BaseURL, p.APIKey, req.Model
			break
		}
	}

	req.Model = modelID
	body, _ = json.Marshal(req)

	httpReq, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost,
		baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"message": err.Error(), "type": "server_error"},
		})
		return
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("HTTP-Referer", "https://github.com/redstone-md/graft")
	httpReq.Header.Set("X-Title", "Graft")

	resp, err := h.llm.HTTPClient.Do(httpReq)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"error": gin.H{"message": fmt.Sprintf("upstream: %v", err), "type": "upstream_error"},
		})
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	c.Data(resp.StatusCode, "application/json", respBody)
}

// ---------- Helpers ----------

func (h *Handler) extractConfig(model string, plugins []Plugin) (profile string, panelModels []string, judgeModel, finalModel string) {
	// Check if model matches a profile name.
	if _, ok := h.cfg.Profiles[model]; ok {
		profile = model
	}

	// Check plugins for overrides.
	for _, p := range plugins {
		if strings.EqualFold(p.ID, "graft") || strings.EqualFold(p.ID, "openrouter:fusion") {
			if p.Enabled != nil && !*p.Enabled {
				continue
			}
			if len(p.AnalysisModels) > 0 {
				panelModels = p.AnalysisModels
			}
			if p.Model != "" {
				judgeModel = p.Model
				finalModel = p.Model
			}
		}
	}

	return
}

func (h *Handler) resolveModelForProxy(model string) (baseURL, apiKey, modelID string, err error) {
	if m, ok := h.cfg.Models[model]; ok {
		p, ok := h.cfg.Providers[m.Provider]
		if !ok {
			return "", "", "", fmt.Errorf("provider %q not found", m.Provider)
		}
		return p.BaseURL, p.APIKey, m.Model, nil
	}

	if fp, ok := h.cfg.Profiles[model]; ok {
		return h.cfg.ResolveModel(fp.Judge)
	}

	return "", "", "", fmt.Errorf("model %q not found", model)
}

func isGraftModel(model string) bool {
	_, ok := graftModels[model]
	return ok
}

var graftModels = map[string]bool{
	"graft": true,
}

func hasGraftPlugin(plugins []Plugin) bool {
	for _, p := range plugins {
		if strings.EqualFold(p.ID, "graft") || strings.EqualFold(p.ID, "openrouter:fusion") {
			if p.Enabled == nil || *p.Enabled {
				return true
			}
		}
	}
	return false
}

func buildResponse(model, content string) ChatCompletionResponse {
	return ChatCompletionResponse{
		ID:      fmt.Sprintf("chatcmpl-graft-%d", time.Now().UnixNano()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []struct {
			Index   int `json:"index"`
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		}{
			{
				Index: 0,
				Message: struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				}{Role: "assistant", Content: content},
				FinishReason: "stop",
			},
		},
	}
}

func estimateTokens(v interface{}) int {
	switch val := v.(type) {
	case string:
		if len(val) == 0 {
			return 0
		}
		return len(val) / 4
	case []llm.Message:
		total := 0
		for _, m := range val {
			total += len(m.Content) / 4
		}
		return total
	default:
		return 0
	}
}

func toModelList(maps []map[string]string) []struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	OwnedBy string `json:"owned_by"`
} {
	out := make([]struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		OwnedBy string `json:"owned_by"`
	}, len(maps))
	for i, m := range maps {
		out[i] = struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			OwnedBy string `json:"owned_by"`
		}{m["id"], m["object"], m["owned_by"]}
	}
	return out
}
