package engine

import (
	"bytes"
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

type GraftRequest struct {
	Messages     []llm.Message `json:"messages"`                // full conversation history
	Profile      string        `json:"profile,omitempty"`       // pipeline profile name (default: "default")
	PanelModels  []string      `json:"panel_models,omitempty"`  // override profile panel
	JudgeModel   string        `json:"judge_model,omitempty"`   // override profile judge
	FinalModel   string        `json:"final_model,omitempty"`   // override profile final
	Stream       bool          `json:"stream,omitempty"`
}

// ModelMetrics holds per-model call telemetry.
type ModelMetrics struct {
	Model        string  `json:"model"`
	Stage        string  `json:"stage"`                    // panel | judge | final
	TTFTMs       int64   `json:"ttft_ms"`                  // time to first token (ms)
	DurationMs   int64   `json:"duration_ms"`              // total call duration (ms)
	TokenCount   int     `json:"token_count"`              // chunks/tokens received
	TokensPerSec float64 `json:"tokens_per_sec"`           // token throughput
	Error        string  `json:"error,omitempty"`           // non-empty on failure
}

// PipelineLog is the strictly-typed structured log entry for a full pipeline run.
type PipelineLog struct {
	Timestamp    string          `json:"timestamp"`
	Method       string          `json:"method"`
	Path         string          `json:"path"`
	StatusCode   int             `json:"status_code"`
	DurationMs   int64           `json:"duration_ms"`
	Profile      string          `json:"profile"`
	ContextLimit int             `json:"context_limit"`
	Models       []ModelMetrics  `json:"models"`
	Error        string          `json:"error,omitempty"`
}

type PanelResult struct {
	Model    string        `json:"model"`
	Answer   string        `json:"answer"`
	Duration int64         `json:"duration_ms"`
	Error    string        `json:"error,omitempty"`
	Metrics  *ModelMetrics `json:"metrics,omitempty"`
}

type ModelEvaluation struct {
	Model          string   `json:"model"`
	Summary        string   `json:"summary"`
	Strengths      []string `json:"strengths"`
	Weaknesses     []string `json:"weaknesses"`
	FactualCorrect *bool    `json:"factual_correct"`
	Completeness   string   `json:"completeness"`
	ReasoningDepth string   `json:"reasoning_depth"`
}

type JudgeAnalysis struct {
	Evaluations    []ModelEvaluation `json:"evaluations"`
	Consensus      []string          `json:"consensus"`
	Contradictions []Contradiction   `json:"contradictions"`
	PartialCover   []string          `json:"partial_coverage"`
	UniqueInsights []UniqueInsight   `json:"unique_insights"`
	BlindSpots     []string          `json:"blind_spots"`
	Recommendation string            `json:"recommendation"`
	ConfidenceNote string            `json:"confidence_note"`
}

type Contradiction struct {
	Point   string `json:"point"`
	ModelA  string `json:"model_a"`
	ModelB  string `json:"model_b"`
	Verdict string `json:"verdict"`
}

type UniqueInsight struct {
	Model  string `json:"model"`
	Insight string `json:"insight"`
}

type GraftResult struct {
	FinalAnswer   string          `json:"final_answer"`
	Panel         []PanelResult   `json:"panel"`
	Judge         *JudgeAnalysis  `json:"judge,omitempty"`
	JudgeRaw      string          `json:"judge_raw,omitempty"`
	DurationMs    int64           `json:"duration_ms"`
	ContextWindow int             `json:"context_window,omitempty"` // effective limit (0 = unlimited)
	Metrics       []ModelMetrics  `json:"metrics,omitempty"`        // per-model telemetry
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
func (e *Engine) Run(ctx context.Context, req GraftRequest) (*GraftResult, error) {
	start := time.Now()

	panelModels, judgeModel, finalModel, err := e.resolveRefs(req)
	if err != nil {
		return nil, err
	}

	// Calculate effective context limit (smallest model in pipeline).
	ctxLimit := effectiveContextForPipeline(e.cfg, panelModels, judgeModel, finalModel)

	// Truncate conversation for each stage.
	// Panel: system prompt (~200 tokens) + user messages. Reserve 2000 for response.
	panelConversation := truncateConversation(req.Messages, ctxLimit, 2000)
	// Judge: system prompt (~400 tokens) + panel answers + conversation. Reserve 1500 for response.
	judgeConversation := truncateConversation(req.Messages, ctxLimit, 4000)
	// Final: system prompt (~300 tokens) + judge analysis + panel answers. Reserve 2000 for response.
	finalConversation := truncateConversation(req.Messages, ctxLimit, 3500)

	// Stage 1: Panel
	panelResults := e.runPanel(ctx, panelConversation, panelModels)

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
	judgeAnalysis, judgeRaw, err := e.runJudge(ctx, judgeConversation, answers, judgeModel)
	if err != nil {
		return &GraftResult{
			FinalAnswer:   panelResults[0].Answer,
			Panel:         panelResults,
			DurationMs:    time.Since(start).Milliseconds(),
			ContextWindow: ctxLimit,
		}, nil
	}

	// Stage 3: Final
	finalAnswer, err := e.runFinal(ctx, finalConversation, judgeAnalysis, panelResults, finalModel)
	if err != nil {
		return &GraftResult{
			FinalAnswer:   judgeRaw,
			Panel:         panelResults,
			Judge:         judgeAnalysis,
			DurationMs:    time.Since(start).Milliseconds(),
			ContextWindow: ctxLimit,
		}, nil
	}

	return &GraftResult{
		FinalAnswer:   finalAnswer,
		Panel:         panelResults,
		Judge:         judgeAnalysis,
		DurationMs:    time.Since(start).Milliseconds(),
		ContextWindow: ctxLimit,
	}, nil
}

// StreamResult bundles streaming return values.
type StreamResult struct {
	Metrics       []ModelMetrics
	ContextWindow int
}

// RunStream executes the full pipeline and streams SSE events.
// Only OpenAI-compatible data events are written to the wire:
//   - data: {"choices":[{"delta":{"content":"..."}}]}  (content chunks)
//   - data: {"error":{...}}                              (errors)
//   - data: [DONE]                                       (stream end)
//
// Internal events (stage transitions, pings, result metadata) are logged
// via structured PipelineLog but NOT sent to the client.
func (e *Engine) RunStream(ctx context.Context, req GraftRequest, w io.Writer, flusher http.Flusher) StreamResult {
	panelModels, judgeModel, finalModel, err := e.resolveRefs(req)
	if err != nil {
		e.sendSSEError(w, flusher, err.Error())
		e.sendSSEDone(w, flusher)
		return StreamResult{}
	}

	ctxLimit := effectiveContextForPipeline(e.cfg, panelModels, judgeModel, finalModel)
	panelConversation := truncateConversation(req.Messages, ctxLimit, 2000)
	judgeConversation := truncateConversation(req.Messages, ctxLimit, 4000)
	finalConversation := truncateConversation(req.Messages, ctxLimit, 3500)

	// Keepalive — empty data lines every 15s to prevent proxy/client timeouts.
	pingTicker := time.NewTicker(15 * time.Second)
	defer pingTicker.Stop()
	go func() {
		for range pingTicker.C {
			e.sendSSEKeepalive(w, flusher)
		}
	}()

	// --- Stage 1: Panel ---
	panelResults, panelMetrics := e.runPanelStream(ctx, panelConversation, panelModels)

	var answers []string
	for _, r := range panelResults {
		if r.Error == "" && r.Answer != "" {
			answers = append(answers, fmt.Sprintf("=== %s ===\n%s", r.Model, r.Answer))
		}
	}
	if len(answers) == 0 {
		e.sendSSEError(w, flusher, "all panel models failed")
		e.sendSSEDone(w, flusher)
		return StreamResult{Metrics: panelMetrics, ContextWindow: ctxLimit}
	}

	// --- Stage 2: Judge ---
	judgeAnalysis, judgeRaw, judgeMetrics, err := e.runJudgeStream(ctx, judgeConversation, answers, judgeModel)
	allMetrics := append(panelMetrics, judgeMetrics)
	if err != nil {
		// Judge failed — fall back to best panel answer.
		e.sendSSEContent(w, flusher, panelResults[0].Answer)
		e.sendSSEDone(w, flusher)
		return StreamResult{Metrics: allMetrics, ContextWindow: ctxLimit}
	}

	// --- Stage 3: Final ---
	finalAnswer, finalMetrics, err := e.runFinalStream(ctx, finalConversation, judgeAnalysis, panelResults, finalModel, w, flusher)
	allMetrics = append(allMetrics, finalMetrics)
	if err != nil {
		// Final failed — fall back to judge raw output.
		e.sendSSEContent(w, flusher, judgeRaw)
		e.sendSSEDone(w, flusher)
		return StreamResult{Metrics: allMetrics, ContextWindow: ctxLimit}
	}
	_ = finalAnswer

	// Pipeline complete — final answer already streamed by runFinalStream.
	// Send [DONE] to signal end of stream.
	e.sendSSEDone(w, flusher)

	return StreamResult{Metrics: allMetrics, ContextWindow: ctxLimit}
}

// ---------- Ref resolution ----------

func (e *Engine) resolveRefs(req GraftRequest) (panel []string, judge, final string, err error) {
	// Pick profile.
	profile := e.defaultProfile()
	if req.Profile != "" {
		p, ok := e.cfg.Profiles[req.Profile]
		if !ok {
			return nil, "", "", fmt.Errorf("unknown profile: %q", req.Profile)
		}
		profile = p
	}

	panel = req.PanelModels
	if len(panel) == 0 {
		panel = profile.Panel
	}
	judge = req.JudgeModel
	if judge == "" {
		judge = profile.Judge
	}
	final = req.FinalModel
	if final == "" {
		final = profile.Final
	}

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

func (e *Engine) defaultProfile() config.PipelineProfile {
	for _, fp := range e.cfg.Profiles {
		return fp
	}
	return config.PipelineProfile{}
}

// ---------- Panel ----------

const panelSystemPrompt = `You are an independent expert panel member. Your job is to provide YOUR BEST answer — not to agree with anyone.

Rules:
- Think independently. Do not hedge or caveat unnecessarily.
- State facts with confidence when you know them. Say "I don't know" when you don't.
- If the question has a clear answer, give it. If it's debatable, present the strongest position with evidence.
- Be specific. Vague answers are useless.
- Include reasoning, not just conclusions. Show your work.`

func (e *Engine) runPanel(ctx context.Context, messages []llm.Message, models []string) []PanelResult {
	results := make([]PanelResult, len(models))
	var wg sync.WaitGroup

	for i, model := range models {
		wg.Add(1)
		go func(idx int, ref string) {
			defer wg.Done()
			panelMsgs := buildPanelMessages(messages)
			results[idx] = e.callModel(ctx, ref, panelMsgs)
		}(i, model)
	}

	wg.Wait()
	return results
}

func (e *Engine) runPanelStream(ctx context.Context, messages []llm.Message, models []string) ([]PanelResult, []ModelMetrics) {
	results := make([]PanelResult, len(models))
	metrics := make([]ModelMetrics, len(models))
	var wg sync.WaitGroup
	var mu sync.Mutex

	for i, model := range models {
		wg.Add(1)
		go func(idx int, ref string) {
			defer wg.Done()

			baseURL, apiKey, modelID, _ := e.cfg.ResolveModel(ref)
			panelMsgs := buildPanelMessages(messages)

			sm := &llm.StreamMetrics{}
			start := time.Now()
			// Panel results are NOT streamed to the client — use io.Discard.
			full, err := e.client.StreamComplete(ctx, baseURL, apiKey, modelID, panelMsgs,
				io.Discard, sm)

			m := ModelMetrics{
				Model:        ref,
				Stage:        "panel",
				TTFTMs:       sm.TTFT.Milliseconds(),
				DurationMs:   sm.Duration.Milliseconds(),
				TokenCount:   sm.TokenCount,
				TokensPerSec: sm.TokensPerSec,
			}
			if err != nil {
				m.Error = err.Error()
			}

			mu.Lock()
			results[idx] = PanelResult{
				Model:    ref,
				Answer:   full,
				Duration: time.Since(start).Milliseconds(),
				Error:    errText(err),
				Metrics:  &m,
			}
			metrics[idx] = m
			mu.Unlock()
		}(i, model)
	}

	wg.Wait()
	return results, metrics
}

// buildPanelMessages prepends the panel system prompt to the conversation.
func buildPanelMessages(conversation []llm.Message) []llm.Message {
	msgs := make([]llm.Message, 0, len(conversation)+1)
	msgs = append(msgs, llm.Message{Role: "system", Content: panelSystemPrompt})
	msgs = append(msgs, conversation...)
	return msgs
}

// ---------- Judge ----------

const judgeSystemPrompt = `You are a critical analysis judge. Your job is NOT to pick a winner. It is to ANALYZE and SYNTHESIZE.

You will receive a conversation history and the last user message, followed by answers from multiple AI models that independently attempted to answer the user's latest question.

## Step 1: Evaluate each answer independently

For EACH model answer, assess:
- **Factual correctness**: Are there errors, hallucinations, or wrong claims?
- **Completeness**: Does it cover all aspects of the question? (full / partial / shallow)
- **Reasoning depth**: Does it explain WHY, or just state WHAT? (deep / adequate / surface)
- **Strengths**: What does this answer do well?
- **Weaknesses**: What does it get wrong, miss, or underweight?

Be harsh. A confident wrong answer is worse than an honest "I don't know."

## Step 2: Cross-compare

Now compare the answers against each other:
- **Consensus**: What do most models agree on? (these are likely correct — but verify reasoning)
- **Contradictions**: Where do models DISAGREE? For each contradiction, state both positions and which is stronger and WHY (evidence, logic, sources)
- **Partial coverage**: Important topics only SOME models addressed
- **Unique insights**: Valuable points from ONE model that others missed entirely
- **Blind spots**: Important aspects that NONE of the models addressed

## Step 3: Recommend a merge strategy

Write a one-paragraph recommendation for the final synthesizer: which answer(s) to anchor on, which parts to merge, which claims to discard, and what to add from scratch.

Output STRICT JSON only. No prose outside the JSON block:

{
  "evaluations": [
    {
      "model": "model-name",
      "summary": "one-line quality verdict",
      "strengths": ["what this answer does well"],
      "weaknesses": ["what this answer gets wrong or misses"],
      "factual_correct": true,
      "completeness": "full|partial|shallow",
      "reasoning_depth": "deep|adequate|surface"
    }
  ],
  "consensus": ["point that most models agree on with shared reasoning"],
  "contradictions": [
    {
      "point": "what is disagreed on",
      "model_a": "model X says...",
      "model_b": "model Y says...",
      "verdict": "X is stronger because..."
    }
  ],
  "partial_coverage": ["topic only some models covered, noting which ones"],
  "unique_insights": [
    {
      "model": "model-name",
      "insight": "valuable point others missed"
    }
  ],
  "blind_spots": ["important aspect nobody addressed"],
  "recommendation": "one paragraph: which answers to anchor on, what to merge, what to discard, what to add",
  "confidence_note": "0-100%: how confident are you in this analysis and why"
}`

func (e *Engine) runJudge(ctx context.Context, conversation []llm.Message, answers []string, ref string) (*JudgeAnalysis, string, error) {
	messages := buildJudgeMessages(conversation, answers)

	raw, err := e.callModelText(ctx, ref, messages)
	if err != nil {
		return nil, "", err
	}

	return parseJudgeAnalysis(raw)
}

func (e *Engine) runJudgeStream(ctx context.Context, conversation []llm.Message, answers []string, ref string) (*JudgeAnalysis, string, ModelMetrics, error) {
	baseURL, apiKey, modelID, _ := e.cfg.ResolveModel(ref)
	messages := buildJudgeMessages(conversation, answers)

	sm := &llm.StreamMetrics{}
	// Judge results are NOT streamed to the client — use io.Discard.
	raw, err := e.client.StreamComplete(ctx, baseURL, apiKey, modelID, messages,
		io.Discard, sm)

	m := ModelMetrics{
		Model:        ref,
		Stage:        "judge",
		TTFTMs:       sm.TTFT.Milliseconds(),
		DurationMs:   sm.Duration.Milliseconds(),
		TokenCount:   sm.TokenCount,
		TokensPerSec: sm.TokensPerSec,
	}
	if err != nil {
		m.Error = err.Error()
	}

	analysis, _, parseErr := parseJudgeAnalysis(raw)
	if parseErr != nil {
		return nil, raw, m, parseErr
	}

	return analysis, raw, m, nil
}

func buildJudgeMessages(conversation []llm.Message, answers []string) []llm.Message {
	// Summarize conversation context for the judge.
	convoBlock := formatConversation(conversation)

	return []llm.Message{
		{Role: "system", Content: judgeSystemPrompt},
		{Role: "user", Content: fmt.Sprintf(
			"CONVERSATION HISTORY:\n%s\n\n--- PANEL ANSWERS ---\n\n%s",
			convoBlock, strings.Join(answers, "\n\n"),
		)},
	}
}

func formatConversation(messages []llm.Message) string {
	var b strings.Builder
	for _, m := range messages {
		role := strings.ToUpper(m.Role)
		fmt.Fprintf(&b, "[%s]\n%s\n\n", role, m.Content)
	}
	return b.String()
}

func parseJudgeAnalysis(raw string) (*JudgeAnalysis, string, error) {
	jsonStr := extractJSON(raw)
	var analysis JudgeAnalysis
	if err := json.Unmarshal([]byte(jsonStr), &analysis); err != nil {
		return nil, raw, fmt.Errorf("judge non-JSON: %w", err)
	}
	return &analysis, raw, nil
}

// ---------- Final ----------

const finalSystemPrompt = `You are an expert synthesizer. A panel of AI models answered a user's question, and a judge model produced a detailed cross-comparison with per-model evaluations, contradiction analysis, and a merge recommendation.

Your job: write the BEST POSSIBLE final answer by following the judge's analysis.

## How to synthesize

1. **Anchor on the strongest answer(s)** — the judge identified which models have the best factual accuracy, completeness, and reasoning depth. Start there.

2. **Merge strengths** — if Model A has great structure but Model B has unique insights, combine them. Don't pick one — take the best parts of both.

3. **Resolve contradictions** — the judge analyzed each disagreement and identified which position is stronger. Follow the verdict. If the judge says "Model X is stronger because of Y", use X's position and cite Y's reasoning.

4. **Fill coverage gaps** — the judge listed partial_coverage and blind_spots. These are YOUR job to address. Use your own knowledge to cover what the panel missed.

5. **Include unique insights** — the judge identified valuable points from individual models that others missed. These are gold — include them with attribution if natural.

6. **Discard errors** — if the judge flagged factual_correct=false or weaknesses, do NOT repeat those claims. Correct them.

## Rules

- Write as if you are answering the question directly. No meta-commentary about the process.
- If the judge's recommendation says "anchor on Model A, merge insights from Model C, discard Model B", follow that strategy.
- Be comprehensive but not verbose. Quality over length.
- If you must speculate beyond what the panel covered, make it clear.`

func (e *Engine) runFinal(ctx context.Context, conversation []llm.Message, analysis *JudgeAnalysis, panel []PanelResult, ref string) (string, error) {
	analysisJSON, _ := json.MarshalIndent(analysis, "", "  ")

	panelBrief := formatPanelBrief(panel)

	messages := []llm.Message{
		{Role: "system", Content: finalSystemPrompt},
		{Role: "user", Content: fmt.Sprintf(
			"CONVERSATION HISTORY:\n%s\n\n--- JUDGE ANALYSIS ---\n%s\n\n--- PANEL ANSWERS ---\n%s",
			formatConversation(conversation), string(analysisJSON), panelBrief,
		)},
	}

	return e.callModelText(ctx, ref, messages)
}

func (e *Engine) runFinalStream(ctx context.Context, conversation []llm.Message, analysis *JudgeAnalysis, panel []PanelResult, ref string, w io.Writer, f http.Flusher) (string, ModelMetrics, error) {
	baseURL, apiKey, modelID, _ := e.cfg.ResolveModel(ref)

	analysisJSON, _ := json.MarshalIndent(analysis, "", "  ")
	panelBrief := formatPanelBrief(panel)

	messages := []llm.Message{
		{Role: "system", Content: finalSystemPrompt},
		{Role: "user", Content: fmt.Sprintf(
			"CONVERSATION HISTORY:\n%s\n\n--- JUDGE ANALYSIS ---\n%s\n\n--- PANEL ANSWERS ---\n%s",
			formatConversation(conversation), string(analysisJSON), panelBrief,
		)},
	}

	sm := &llm.StreamMetrics{}
	var mu sync.Mutex
	full, err := e.client.StreamComplete(ctx, baseURL, apiKey, modelID, messages,
		&streamAdapter{w: w, f: f, model: ref, mu: &mu}, sm)

	m := ModelMetrics{
		Model:        ref,
		Stage:        "final",
		TTFTMs:       sm.TTFT.Milliseconds(),
		DurationMs:   sm.Duration.Milliseconds(),
		TokenCount:   sm.TokenCount,
		TokensPerSec: sm.TokensPerSec,
	}
	if err != nil {
		m.Error = err.Error()
	}

	return full, m, err
}

func formatPanelBrief(panel []PanelResult) string {
	var b strings.Builder
	for _, p := range panel {
		if p.Error == "" {
			ans := p.Answer
			if len(ans) > 2000 {
				ans = ans[:2000] + "\n[...truncated...]"
			}
			fmt.Fprintf(&b, "\n--- %s ---\n%s\n", p.Model, ans)
		}
	}
	return b.String()
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

// sendSSE writes a line to the SSE stream. Only call this for OpenAI-compatible events.
// Non-OpenAI events (stage, ping, result, done) must NOT be written to the wire —
// clients (Vercel AI SDK) use fetch+ReadableStream, not EventSource, and parse ALL lines.
func (e *Engine) sendSSE(w io.Writer, f http.Flusher, event SSEEvent) {
	data, _ := json.Marshal(event)
	fmt.Fprintf(w, "data: %s\n\n", data)
	f.Flush()
}

// sendSSEContent sends an OpenAI-compatible content chunk.
func (e *Engine) sendSSEContent(w io.Writer, f http.Flusher, content string) {
	chunk := map[string]interface{}{
		"choices": []map[string]interface{}{
			{"delta": map[string]string{"content": content}},
		},
	}
	data, _ := json.Marshal(chunk)
	fmt.Fprintf(w, "data: %s\n\n", data)
	f.Flush()
}

// sendSSEDone sends the OpenAI [DONE] signal.
func (e *Engine) sendSSEDone(w io.Writer, f http.Flusher) {
	fmt.Fprintf(w, "data: [DONE]\n\n")
	f.Flush()
}

// sendSSEError sends an OpenAI-compatible error chunk.
func (e *Engine) sendSSEError(w io.Writer, f http.Flusher, msg string) {
	errObj := map[string]interface{}{
		"error": map[string]string{"message": msg, "type": "server_error"},
	}
	data, _ := json.Marshal(errObj)
	fmt.Fprintf(w, "data: %s\n\n", data)
	f.Flush()
}

// sendSSEKeepalive is intentionally empty — the pipeline is sequential (panel→judge→final)
// and takes seconds, so the connection is never idle during processing.
// Sending any data line (even empty) breaks clients that parse all `data:` lines as JSON.
func (e *Engine) sendSSEKeepalive(w io.Writer, f http.Flusher) {}

type streamAdapter struct {
	w     io.Writer
	f     http.Flusher
	model string
	mu    *sync.Mutex
}

func (s *streamAdapter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var event llm.StreamEvent
	if err := json.Unmarshal(bytes.TrimRight(p, "\n"), &event); err != nil || len(event.Choices) == 0 {
		return len(p), nil
	}

	chunk := event.Choices[0].Delta.Content
	if chunk == "" {
		return len(p), nil
	}

	// Send OpenAI-compatible SSE chunk so clients can parse it.
	oc := map[string]interface{}{
		"choices": []map[string]interface{}{
			{"delta": map[string]string{"content": chunk}},
		},
	}
	data, _ := json.Marshal(oc)
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

// ---------- Context window management ----------

// estimateTokens rough-estimates token count (~4 chars per token).
func estimateTokens(text string) int {
	if len(text) == 0 {
		return 0
	}
	return len(text) / 4
}

// conversationTokens estimates total tokens in a message array.
func conversationTokens(messages []llm.Message) int {
	total := 0
	for _, m := range messages {
		total += estimateTokens(m.Content)
		total += 4 // role + formatting overhead
	}
	return total
}

// truncateConversation trims old messages to fit within a token budget.
// Strategy: keep system prompt (first message if role=system), keep the last N messages
// that fit, drop oldest middle messages.
// headroom is reserved for the model's response and the system prompt.
func truncateConversation(messages []llm.Message, contextWindow, headroom int) []llm.Message {
	if contextWindow <= 0 || len(messages) == 0 {
		return messages
	}

	budget := contextWindow - headroom
	if budget <= 0 {
		// Context is too small — return only the last message.
		if len(messages) > 0 {
			return messages[len(messages)-1:]
		}
		return messages
	}

	// Separate system prompt from the rest.
	systemIdx := -1
	if messages[0].Role == "system" {
		systemIdx = 0
	}

	var systemMsg llm.Message
	var rest []llm.Message
	if systemIdx >= 0 {
		systemMsg = messages[0]
		rest = messages[1:]
	} else {
		rest = messages
	}

	systemTokens := estimateTokens(systemMsg.Content) + 4
	remaining := budget - systemTokens
	if remaining <= 0 {
		// System prompt alone exceeds budget — return only last message.
		return messages[len(messages)-1:]
	}

	// Greedily include messages from the end (most recent first).
	selected := make([]llm.Message, 0, len(rest))
	totalTokens := 0
	for i := len(rest) - 1; i >= 0; i-- {
		msgTokens := estimateTokens(rest[i].Content) + 4
		if totalTokens+msgTokens > remaining {
			break
		}
		totalTokens += msgTokens
		selected = append(selected, rest[i])
	}

	// Reverse selected to restore chronological order.
	for i, j := 0, len(selected)-1; i < j; i, j = i+1, j-1 {
		selected[i], selected[j] = selected[j], selected[i]
	}

	// Reconstruct: system + selected messages.
	result := make([]llm.Message, 0, 1+len(selected))
	if systemIdx >= 0 {
		result = append(result, systemMsg)
	}
	result = append(result, selected...)

	return result
}

// effectiveContextForPipeline returns the smallest context_window among all models
// in a pipeline stage. Returns 0 if any model has unknown context.
func effectiveContextForPipeline(cfg *config.Config, panelRefs []string, judgeRef, finalRef string) int {
	allRefs := make([]string, 0, len(panelRefs)+2)
	allRefs = append(allRefs, panelRefs...)
	allRefs = append(allRefs, judgeRef, finalRef)
	return cfg.MinContextWindow(allRefs)
}
