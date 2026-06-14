<p align="center">
  <h1 align="center">Graft</h1>
  <p align="center">Multi-model pipeline orchestrator with OpenAI-compatible API</p>
</p>

<p align="center">
  <a href="README.ru.md"><img src="https://img.shields.io/badge/🇷🇺-Русский-blue" alt="Русский"></a>
  <a href="README.md"><img src="https://img.shields.io/badge/🇬🇧-English-blue" alt="English"></a>
</p>

<p align="center">
  <a href="https://github.com/redstone-md/graft/actions"><img src="https://github.com/redstone-md/graft/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/redstone-md/graft/releases"><img src="https://img.shields.io/github/v/release/redstone-md/graft" alt="Release"></a>
  <a href="https://github.com/redstone-md/graft/blob/main/LICENSE"><img src="https://img.shields.io/github/license/redstone-md/graft" alt="License"></a>
  <a href="https://pkg.go.dev/github.com/redstone-md/graft"><img src="https://pkg.go.dev/badge/github.com/redstone-md/graft.svg" alt="Go Reference"></a>
</p>

---

**Graft** is an orchestrator that runs your prompt through multiple LLMs in parallel, then a judge model cross-compares their answers and produces a structured analysis, and a final model synthesizes the best possible response.

This is not "pick the longest answer." It's **analyze + merge**.

## How it works

```
User → POST /v1/chat/completions
    ↓
[Panel] ──→ DeepSeek V4 ──→ answer1 ─┐
        ──→ Gemini Flash ──→ answer2 ─┤
        ──→ Kimi K2.6    ──→ answer3 ─┘
                                   ↓
                     [Judge] ──→ JSON analysis:
                                   • evaluations (per-answer quality)
                                   • consensus (shared points)
                                   • contradictions (disagreements + who's right)
                                   • blind_spots (what's missing)
                                   • recommendation (merge strategy)
                                   ↓
                     [Final] ──→ final answer
```

### Stage 1: Panel (parallel)

N models receive the **full conversation history** and answer independently. No cross-contamination between models.

### Stage 2: Judge (comparison)

The judge model receives all panel answers and **evaluates each** on:
- Factual correctness
- Coverage completeness
- Reasoning depth

Then builds cross-comparison: where they agree, where they contradict (and who's right), which insights are unique, which topics nobody covered.

### Stage 3: Final (synthesis)

The synthesizer takes the judge's analysis and **writes a single answer** that:
- Takes the best from each panel answer
- Resolves contradictions per the judge's verdict
- Covers blind spots from its own knowledge
- Excludes errors flagged by the judge

## Example

**Question:** "Car wash is 100m from home — should I drive or walk?"

**Panel:**
- DeepSeek: "Walk. 100m is a minute, cars waste fuel..."
- Gemini: "Walk. Parking, starting the engine..."
- Kimi: "Drive. The point of a car wash is to wash your car — it needs to be there."

**Judge:**
- Contradiction: 2 vs 1. Verdict: "Kimi is right — you need to arrive with the car, otherwise there's nothing to wash."
- Blind spot: "The question implies the car is already at home — not explicitly stated."

**Final:** "Drive. A car wash is for washing your vehicle — walking there means arriving without your car. 100m is negligible distance, fuel consumption is insignificant."

## Quick Start

```bash
# Download binary
# https://github.com/redstone-md/graft/releases

# Or build from source
git clone https://github.com/redstone-md/graft.git
cd graft
go build -o graft ./cmd/graft/

# Configure
cp config.example.yaml config.yaml
# edit config.yaml — set auth_token and api_key

# Run
./graft -config config.yaml
```

Detailed setup guide: **[docs/SETUP.md](docs/SETUP.md)**

## Usage

### Python (OpenAI SDK)

```python
from openai import OpenAI

client = OpenAI(base_url="http://localhost:8080/v1", api_key="your-token")

# Simple request
response = client.chat.completions.create(
    model="default",
    messages=[{"role": "user", "content": "Explain quantum computing"}]
)

# Full conversation (agentic)
response = client.chat.completions.create(
    model="premium",
    messages=[
        {"role": "system", "content": "You are a helpful assistant."},
        {"role": "user", "content": "What is Rust?"},
        {"role": "assistant", "content": "Rust is a systems programming language..."},
        {"role": "user", "content": "How does ownership work?"},
    ]
)

# Streaming
stream = client.chat.completions.create(
    model="default",
    messages=[{"role": "user", "content": "Hello"}],
    stream=True,
)
for chunk in stream:
    if chunk.choices[0].delta.content:
        print(chunk.choices[0].delta.content, end="")
```

### LangChain

```python
from langchain_openai import ChatOpenAI

llm = ChatOpenAI(
    model="default",
    base_url="http://localhost:8080/v1",
    api_key="your-token",
)
```

### curl

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer your-token" \
  -d '{
    "model": "default",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

## SSE Events (streaming)

```
data: {"type":"stage","stage":"panel"}
data: {"type":"content","model":"deepseek-v4","content":"..."}
data: {"type":"content","model":"gemini-flash","content":"..."}
data: {"type":"ping"}                                    ← keepalive every 15s
data: {"type":"stage","stage":"judge"}
data: {"type":"content","model":"claude-opus","content":"..."}
data: {"type":"stage","stage":"final"}
data: {"type":"content","model":"claude-opus","content":"..."}
data: {"type":"result","data":{...}}
data: {"type":"done"}
```

## Endpoints

| Method | Path | Auth | Description |
|---|---|---|---|
| `POST` | `/v1/chat/completions` | Bearer | OpenAI-compatible chat completion |
| `GET` | `/v1/models` | Bearer | List profiles and models |
| `GET` | `/health` | — | Health check |

## Configuration

```yaml
server:
  port: "8080"
  auth_token: "your-token"

providers:
  openrouter:
    base_url: "https://openrouter.ai/api/v1"
    api_key: "sk-or-v1-..."

models:
  deepseek-v4:
    provider: openrouter
    model: "deepseek/deepseek-v4-pro"
    context_window: 131072

profiles:
  default:
    panel: [deepseek-v4, gemini-flash, kimi]
    judge: claude-opus
    final: claude-opus
```

Full configuration guide: **[docs/SETUP.md](docs/SETUP.md)**

## Project Ideas

Graft is a **building block** for agentic systems. Here's what you can build:

- **Multi-perspective agent** — every response is verified by multiple models before delivery
- **Code review bot** — panel of coding models analyzes PRs in parallel, judge finds conflicts
- **Research assistant** — automatic search + synthesis from multiple sources with credibility scoring
- **Decision support** — for critical decisions where errors are costly (legal, medical, financial)
- **Multi-model fallback** — if one model fails, others continue
- **Custom pipelines** — create profiles for specific tasks: cheap for simple questions, premium for complex

## Context and Limits

Graft automatically calculates the effective pipeline context:

```
effective_context = min(context_window) of all models in pipeline
```

If you have deepseek-v4 (128K) and gemini-flash (1M), context will be **128K** — because deepseek can't handle more. Old messages are trimmed automatically.

More details: **[docs/SETUP.md](docs/SETUP.md#model-setup)**

## CI/CD

Every push/PR runs `go vet` + `go build`. On `v*` tags, a release is automatically created with binaries for Linux, macOS, and Windows.

```bash
git tag v1.0.0
git push --tags
# → GitHub Actions builds binaries and creates a Release
```

## License

[MIT](LICENSE)
