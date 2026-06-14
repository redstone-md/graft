package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/redstone-md/graft/internal/config"
	"github.com/redstone-md/graft/internal/llm"
)

// ---------- SSE Events ----------

type SSEEvent struct {
	Type    string      `json:"type"`
	Model   string      `json:"model,omitempty"`
	Stage   string      `json:"stage,omitempty"`
	Content string      `json:"content,omitempty"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// ---------- Request/Response ----------

type FusionRequest struct {
	Prompt       string   `json:"prompt"`
	PanelModels  []string `json:"panel_models,omitempty"`
	JudgeModel   string   `json:"judge_model,omitempty"`
	FinalModel   string   `json:"final_model,omitempty"`
	Stream       bool     `json:"stream,omitempty"`
}

type PanelResult struct {
	Model    string `json:"model"`
	Answer   string `json:"answer"`
	Duration int64  `json:"duration_ms"`
	Error    string `json:"error,omitempty"`
}

type JudgeAnalysis struct {
	Consensus      []string `json:"consensus"`
	Contradictions []string `json:"contradictions"`
	PartialCover   []string `json:"partial_coverage"`
	UniqueInsights []string `json:"unique_insights"`
	BlindSpots     []string `json:"blind_spots"`
	ConfidenceNote string   `json:"confidence_note"`
}

type FusionResult struct {
	FinalAnswer string        `json:"final_answer"`
	Panel       []PanelResult `json:"panel"`
	Judge       *JudgeAnalysis `json:"judge,omitempty"`
	JudgeRaw    string        `json:"judge_raw,omitempty"`
	DurationMs  int64         `json:"duration_ms"`
}

// ---------- Engine ----------

type Engine struct {
	cfg    *config.Config
	client *llm.Client
}

func NewEngine(cfg *config.Config) *Engine {
	return &Engine{
		cfg:    cfg,
		client: llm.NewClient(),
	}
}

// Run executes the full 3-stage pipeline (non-streaming).
func (e *Engine) Run(ctx context.Context, req FusionRequest) (*FusionResult, error) {
	start := time.Now()

	panelModels, judgeModel, finalModel, err := e.resolveRefs(req)
	if err != nil {
		return nil, err
	}

	// Stage 1: Panel
	panelResults := e.runPanel(ctx, panelModels)

	var answers []string
	for _, r := range panelResults {
		if r.Error == "" && r.Answer != "" {
			answers = append(answers, fmt.Sprintf("=== %s ===\n%s", r.Model, r.Answer))
		}
	}
	if len(answers) == 0 {
		return nil, fmt.Errorf("all panel models failed")
	}

	// Stage 2: Judge
	judgeAnalysis, judgeRaw, err := e.runJudge(ctx, req.Prompt, answers, judgeModel)
	if err != nil {
		return &FusionResult{
			FinalAnswer: panelResults[0].Answer,
			Panel:       panelResults,
			DurationMs:  time.Since(start).Milliseconds(),
		}, nil
	}

	// Stage 3: Final
	finalAnswer, err := e.runFinal(ctx, req.Prompt, judgeAnalysis, panelResults, finalModel)
	if err != nil {
		return &FusionResult{
			FinalAnswer: judgeRaw,
			Panel:       panelResults,
			Judge:       judgeAnalysis,
			DurationMs:  time.Since(start).Milliseconds(),
		}, nil
	}

	return &FusionResult{
		FinalAnswer: finalAnswer,
		Panel:       panelResults,
		Judge:       judgeAnalysis,
		DurationMs:  time.Since(start).Milliseconds(),
	}, nil
}

// RunStream executes the full pipeline and streams SSE events.
func (e *Engine) RunStream(ctx context.Context, req FusionRequest, w io.Writer, flusher http.Flusher) {
	start := time.Now()

	panelModels, judgeModel, finalModel, err := e.resolveRefs(req)
	if err != nil {
		e.sendSSE(w, flusher, SSEEvent{Type: "error", Error: err.Error()})
		return
	}

	// Keepalive ticker — sends ping every 15s during long operations.
	pingTicker := time.NewTicker(15 * time.Second)
	defer pingTicker.Stop()
	go func() {
		for range pingTicker.C {
			e.sendSSE(w, flusher, SSEEvent{Type: "ping"})
		}
	}()

	// --- Stage 1: Panel (parallel streaming) ---
	e.sendSSE(w, flusher, SSEEvent{Type: "stage", Stage: "panel"})

	panelResults := e.runPanelStream(ctx, panelModels, w, flusher)

	var answers []string
	for _, r := range panelResults {
		if r.Error == "" && r.Answer != "" {
			answers = append(answers, fmt.Sprintf("=== %s ===\n%s", r.Model, r.Answer))
		}
	}

	if len(answers) == 0 {
		e.sendSSE(w, flusher, SSEEvent{Type: "error", Error: "all panel models failed"})
		return
	}

	// --- Stage 2: Judge ---
	e.sendSSE(w, flusher, SSEEvent{Type: "stage", Stage: "judge"})

	judgeAnalysis, judgeRaw, err := e.runJudgeStream(ctx, req.Prompt, answers, judgeModel, w, flusher)
	if err != nil {
		// Judge failed — stream first panel answer as final.
		e.sendSSE(w, flusher, SSEEvent{Type: "stage", Stage: "final"})
		e.sendSSE(w, flusher, SSEEvent{Type: "content", Content: panelResults[0].Answer})
		e.sendSSE(w, flusher, SSEEvent{Type: "done"})
		return
	}

	// --- Stage 3: Final ---
	e.sendSSE(w, flusher, SSEEvent{Type: "stage", Stage: "final"})

	finalAnswer, err := e.runFinalStream(ctx, req.Prompt, judgeAnalysis, panelResults, finalModel, w, flusher)
	if err != nil {
		e.sendSSE(w, flusher, SSEEvent{Type: "content", Content: judgeRaw})
		e.sendSSE(w, flusher, SSEEvent{Type: "done"})
		return
	}

	_ = finalAnswer

	e.sendSSE(w, flusher, SSEEvent{
		Type: "result",
		Data: FusionResult{
			Panel:    panelResults,
			Judge:    judgeAnalysis,
			DurationMs: time.Since(start).Milliseconds(),
		},
	})
	e.sendSSE(w, flusher, SSEEvent{Type: "done"})
}

// ---------- Stage implementations ----------

func (e *Engine) resolveRefs(req FusionRequest) (panel []string, judge, final string, err error) {
	panel = req.PanelModels
	if len(panel) == 0 {
		// Find the "default" fusion profile.
		profile := e.defaultProfile()
		panel = profile.Panel
	}
	judge = req.JudgeModel
	if judge == "" {
		judge = e.defaultProfile().Judge
	}
	final = req.FinalModel
	if final == "" {
		final = e.defaultProfile().Final
	}

	// Validate refs exist.
	for _, ref := range panel {
		if _, _, _, err := e.cfg.ResolveModel(ref); err != nil {
			return nil, "", "", fmt.Errorf("panel model %q: %w", ref, err)
		}
	}
	if _, _, _, err := e.cfg.ResolveModel(judge); err != nil {
		return nil, "", "", fmt.Errorf("judge model: %w", err)
	}
	if _, _, _, err := e.cfg.ResolveModel(final); err != nil {
		return nil, "", "", fmt.Errorf("final model: %w", err)
	}

	return panel, judge, final, nil
}

func (e *Engine) defaultProfile() config.FusionProfile {
	for _, fp := range e.cfg.Fusion {
		return fp // first profile
	}
	return config.FusionProfile{}
}

// --- Panel ---

func (e *Engine) runPanel(ctx context.Context, models []string) []PanelResult {
	results := make([]PanelResult, len(models))
	var wg sync.WaitGroup

	for i, model := range models {
		wg.Add(1)
		go func(idx int, ref string) {
			defer wg.Done()
			results[idx] = e.callModel(ctx, ref, e.panelMessages(ctx, ref))
		}(i, model)
	}

	wg.Wait()
	return results
}

func (e *Engine) runPanelStream(ctx context.Context, models []string, w io.Writer, f http.Flusher) []PanelResult {
	results := make([]PanelResult, len(models))
	var wg sync.WaitGroup
	var mu sync.Mutex

	for i, model := range models {
		wg.Add(1)
		go func(idx int, ref string) {
			defer wg.Done()

			baseURL, apiKey, modelID, _ := e.cfg.ResolveModel(ref)
			messages := e.panelMessages(ctx, ref)

			start := time.Now()

			// Use streaming for panel models.
			full, err := e.client.StreamComplete(ctx, baseURL, apiKey, modelID, messages,
				&streamAdapter{w: w, f: f, model: ref, mu: &mu})

			mu.Lock()
			results[idx] = PanelResult{
				Model:    ref,
				Answer:   full,
				Duration: time.Since(start).Milliseconds(),
				Error:    errText(err),
			}
			mu.Unlock()
		}(i, model)
	}

	wg.Wait()
	return results
}

func (e *Engine) panelMessages(ctx context.Context, ref string) []llm.Message {
	return []llm.Message{
		{Role: "system", Content: "You are a panel member in a multi-model deliberation. Provide your best independent answer. Be thorough, accurate, and cite reasoning."},
		{Role: "user", Content: "Answer the following question thoroughly:\n\n{PROMPT}"},
	}
}

// --- Judge ---

const judgeSystemPrompt = `You are a critical analysis judge. You receive a user's question and answers from multiple AI models.

Produce a STRICT JSON analysis. Do NOT write prose. Output ONLY valid JSON:

{
  "consensus": ["point that most models agree on"],
  "contradictions": ["point where models disagree"],
  "partial_coverage": ["topic only some models covered"],
  "unique_insights": ["valuable insight from one model that others missed"],
  "blind_spots": ["important aspect nobody addressed"],
  "confidence_note": "one sentence: confidence level and why"
}

Rules:
- Be specific. Paraphrase actual responses.
- confidence_note: 0-100% and reasoning.`

func (e *Engine) runJudge(ctx context.Context, question string, answers []string, ref string) (*JudgeAnalysis, string, error) {
	messages := []llm.Message{
		{Role: "system", Content: judgeSystemPrompt},
		{Role: "user", Content: fmt.Sprintf("QUESTION:\n%s\n\n--- MODEL ANSWERS ---\n\n%s",
			question, strings.Join(answers, "\n\n"))},
	}

	raw, err := e.callModelText(ctx, ref, messages)
	if err != nil {
		return nil, "", err
	}

	jsonStr := extractJSON(raw)
	var analysis JudgeAnalysis
	if err := json.Unmarshal([]byte(jsonStr), &analysis); err != nil {
		return nil, raw, fmt.Errorf("judge non-JSON: %w", err)
	}

	return &analysis, raw, nil
}

func (e *Engine) runJudgeStream(ctx context.Context, question string, answers []string, ref string, w io.Writer, f http.Flusher) (*JudgeAnalysis, string, error) {
	baseURL, apiKey, modelID, _ := e.cfg.ResolveModel(ref)

	messages := []llm.Message{
		{Role: "system", Content: judgeSystemPrompt},
		{Role: "user", Content: fmt.Sprintf("QUESTION:\n%s\n\n--- MODEL ANSWERS ---\n\n%s",
			question, strings.Join(answers, "\n\n"))},
	}

	var mu sync.Mutex
	raw, err := e.client.StreamComplete(ctx, baseURL, apiKey, modelID, messages,
		&streamAdapter{w: w, f: f, model: ref, mu: &mu})
	if err != nil {
		return nil, "", err
	}

	jsonStr := extractJSON(raw)
	var analysis JudgeAnalysis
	if err := json.Unmarshal([]byte(jsonStr), &analysis); err != nil {
		return nil, raw, fmt.Errorf("judge non-JSON: %w", err)
	}

	return &analysis, raw, nil
}

// --- Final ---

const finalSystemPrompt = `You are an expert synthesizer. A panel of AI models answered a user's question, and a judge model analyzed their responses.

Write the BEST POSSIBLE final answer. Incorporate:
1. Points of consensus (likely correct)
2. Resolve contradictions (pick stronger argument)
3. Cover partial coverage gaps
4. Include unique insights
5. Address blind spots

Write a clear, comprehensive, human-readable response. Do NOT meta-comment about the process.`

func (e *Engine) runFinal(ctx context.Context, question string, analysis *JudgeAnalysis, panel []PanelResult, ref string) (string, error) {
	analysisJSON, _ := json.MarshalIndent(analysis, "", "  ")

	panelBrief := ""
	for _, p := range panel {
		if p.Error == "" {
			ans := p.Answer
			if len(ans) > 2000 {
				ans = ans[:2000] + "\n[...truncated...]"
			}
			panelBrief += fmt.Sprintf("\n--- %s ---\n%s\n", p.Model, ans)
		}
	}

	messages := []llm.Message{
		{Role: "system", Content: finalSystemPrompt},
		{Role: "user", Content: fmt.Sprintf(
			"USER QUESTION:\n%s\n\n--- JUDGE ANALYSIS ---\n%s\n\n--- PANEL ANSWERS ---\n%s",
			question, string(analysisJSON), panelBrief,
		)},
	}

	return e.callModelText(ctx, ref, messages)
}

func (e *Engine) runFinalStream(ctx context.Context, question string, analysis *JudgeAnalysis, panel []PanelResult, ref string, w io.Writer, f http.Flusher) (string, error) {
	baseURL, apiKey, modelID, _ := e.cfg.ResolveModel(ref)

	analysisJSON, _ := json.MarshalIndent(analysis, "", "  ")

	panelBrief := ""
	for _, p := range panel {
		if p.Error == "" {
			ans := p.Answer
			if len(ans) > 2000 {
				ans = ans[:2000] + "\n[...truncated...]"
			}
			panelBrief += fmt.Sprintf("\n--- %s ---\n%s\n", p.Model, ans)
		}
	}

	messages := []llm.Message{
		{Role: "system", Content: finalSystemPrompt},
		{Role: "user", Content: fmt.Sprintf(
			"USER QUESTION:\n%s\n\n--- JUDGE ANALYSIS ---\n%s\n\n--- PANEL ANSWERS ---\n%s",
			question, string(analysisJSON), panelBrief,
		)},
	}

	var mu sync.Mutex
	full, err := e.client.StreamComplete(ctx, baseURL, apiKey, modelID, messages,
		&streamAdapter{w: w, f: f, model: ref, mu: &mu})
	if err != nil {
		return "", err
	}

	return full, nil
}

// ---------- Helpers ----------

func (e *Engine) callModel(ctx context.Context, ref string, messages []llm.Message) PanelResult {
	start := time.Now()
	baseURL, apiKey, modelID, err := e.cfg.ResolveModel(ref)
	if err != nil {
		return PanelResult{Model: ref, Duration: time.Since(start).Milliseconds(), Error: err.Error()}
	}

	answer, err := e.client.Complete(ctx, baseURL, apiKey, modelID, messages)
	return PanelResult{
		Model:    ref,
		Answer:   answer,
		Duration: time.Since(start).Milliseconds(),
		Error:    errText(err),
	}
}

func (e *Engine) callModelText(ctx context.Context, ref string, messages []llm.Message) (string, error) {
	baseURL, apiKey, modelID, err := e.cfg.ResolveModel(ref)
	if err != nil {
		return "", err
	}
	return e.client.Complete(ctx, baseURL, apiKey, modelID, messages)
}

func (e *Engine) sendSSE(w io.Writer, f http.Flusher, event SSEEvent) {
	data, _ := json.Marshal(event)
	fmt.Fprintf(w, "data: %s\n\n", data)
	f.Flush()
}

// streamAdapter wraps io.Writer to strip per-line model prefix for panel streaming.
type streamAdapter struct {
	w     io.Writer
	f     http.Flusher
	model string
	mu    *sync.Mutex
}

func (s *streamAdapter) Write(p []byte) (int, error) {
	// The LLM client already sends raw SSE chunks.
	// We need to re-wrap them with model info.
	s.mu.Lock()
	defer s.mu.Unlock()

	// Parse the chunk to extract content.
	var event llm.StreamEvent
	if err := json.Unmarshal(p, &event); err != nil || len(event.Choices) == 0 {
		return len(p), nil // skip non-parseable
	}

	chunk := event.Choices[0].Delta.Content
	if chunk == "" {
		return len(p), nil
	}

	sseEvent := SSEEvent{
		Type:    "content",
		Model:   s.model,
		Content: chunk,
	}
	data, _ := json.Marshal(sseEvent)
	fmt.Fprintf(s.w, "data: %s\n\n", data)
	s.f.Flush()

	return len(p), nil
}

func extractJSON(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "{") {
		return text
	}

	if idx := strings.Index(text, "```json"); idx != -1 {
		start := idx + 7
		end := strings.Index(text[start:], "```")
		if end != -1 {
			return strings.TrimSpace(text[start : start+end])
		}
	}
	if idx := strings.Index(text, "```"); idx != -1 {
		start := idx + 3
		if nl := strings.IndexByte(text[start:], '\n'); nl != -1 {
			start += nl + 1
		}
		end := strings.Index(text[start:], "```")
		if end != -1 {
			return strings.TrimSpace(text[start : start+end])
		}
	}

	first := strings.IndexByte(text, '{')
	last := strings.LastIndexByte(text, '}')
	if first != -1 && last > first {
		return text[first : last+1]
	}

	return text
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
