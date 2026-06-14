package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ---------- Types ----------

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Stream      bool      `json:"stream,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Temperature *float64  `json:"temperature,omitempty"`
}

type ChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type StreamEvent struct {
	Choices []struct {
		Delta struct {
			Content         string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
}

// ---------- Metrics ----------

// StreamMetrics holds per-model streaming telemetry.
type StreamMetrics struct {
	TTFT         time.Duration `json:"ttft_ms"`         // time to first token
	Duration     time.Duration `json:"duration_ms"`     // total stream duration
	TokenCount   int           `json:"token_count"`     // number of chunks received
	TokensPerSec float64       `json:"tokens_per_sec"`  // tokens / duration
	Error        string        `json:"error,omitempty"` // non-empty on failure
}

// ---------- Client ----------

type Client struct {
	HTTPClient *http.Client
}

func NewClient() *Client {
	return &Client{
		HTTPClient: &http.Client{Timeout: 300 * time.Second},
	}
}

// Complete sends a non-streaming chat completion.
func (c *Client) Complete(ctx context.Context, baseURL, apiKey, model string, messages []Message) (string, error) {
	reqBody := ChatRequest{Model: model, Messages: messages, Stream: false}
	body, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("HTTP-Referer", "https://github.com/redstone-md/graft")
	req.Header.Set("X-Title", "Fusion Orchestrator")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return "", err
	}
	if chatResp.Error != nil {
		return "", fmt.Errorf("API: %s", chatResp.Error.Message)
	}
	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("no choices")
	}

	return chatResp.Choices[0].Message.Content, nil
}

// StreamComplete sends a streaming request and writes raw SSE chunks to writer.
// Returns the full concatenated text and populates metrics if non-nil.
func (c *Client) StreamComplete(
	ctx context.Context,
	baseURL, apiKey, model string,
	messages []Message,
	w io.Writer,
	metrics *StreamMetrics,
) (string, error) {
	reqBody := ChatRequest{Model: model, Messages: messages, Stream: true}
	body, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		if metrics != nil {
			metrics.Error = err.Error()
		}
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("HTTP-Referer", "https://github.com/redstone-md/graft")
	req.Header.Set("X-Title", "Fusion Orchestrator")

	start := time.Now()
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		if metrics != nil {
			metrics.Error = err.Error()
			metrics.Duration = time.Since(start)
		}
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		errMsg := fmt.Sprintf("status %d: %s", resp.StatusCode, string(respBody))
		if metrics != nil {
			metrics.Error = errMsg
			metrics.Duration = time.Since(start)
		}
		return "", fmt.Errorf("%s", errMsg)
	}

	var full strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	firstToken := true

	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var event StreamEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		if len(event.Choices) == 0 {
			continue
		}

		delta := event.Choices[0].Delta
		hasContent := delta.Content != ""
		hasReasoning := delta.ReasoningContent != ""

		if hasContent || hasReasoning {
			if firstToken && metrics != nil {
				metrics.TTFT = time.Since(start)
				firstToken = false
			}
			if delta.Content != "" {
				full.WriteString(delta.Content)
			}
			if metrics != nil {
				metrics.TokenCount++
			}
			// Write raw SSE chunk to the adapter (preserves upstream format).
			fmt.Fprintf(w, "data: %s\n\n", data)
		}
	}

	// NOTE: [DONE] is NOT sent here — the engine's RunStream handles stream termination.

	if metrics != nil {
		metrics.Duration = time.Since(start)
		if metrics.Duration.Seconds() > 0 {
			metrics.TokensPerSec = float64(metrics.TokenCount) / metrics.Duration.Seconds()
		}
	}

	return full.String(), nil
}
