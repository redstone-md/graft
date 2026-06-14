# Graft

Multi-model pipeline orchestrator with OpenAI-compatible API. Graft runs your prompt through a panel of models in parallel, has a judge cross-compare their answers, and synthesizes the best possible final response.

```
POST /v1/chat/completions
    ↓
model: "default" / "cheap" / "premium" / "fast"?
    ├── YES (profile) → [Panel] → [Judge] → [Final] → SSE stream
    └── NO  (model)    → proxy to provider as-is
```

## Quick Start

```bash
cp config.example.yaml config.yaml
# edit config.yaml — set auth_token, api_key for providers

go run ./cmd/graft/ -config config.yaml
```

## How it works

1. **Panel** — N models answer the user's message independently (parallel, with full conversation context)
2. **Judge** — reads the conversation + all panel answers, evaluates each on factual correctness / completeness / reasoning depth, cross-compares, and produces a structured JSON analysis (consensus, contradictions, blind spots, merge recommendation)
3. **Final** — synthesizes the best answer following the judge's analysis

This is not "pick the longest answer." It's **analyze + merge**.

## Configuration

```yaml
server:
  port: "8080"
  auth_token: "your-secret-token"  # required for /v1/*

providers:
  openrouter:
    base_url: "https://openrouter.ai/api/v1"
    api_key: "sk-or-v1-xxx"
  deepseek:
    base_url: "https://api.deepseek.com/v1"
    api_key: "sk-xxx"

models:
  deepseek-v4:
    provider: openrouter
    model: "deepseek/deepseek-v4-pro"
  gemini-flash:
    provider: openrouter
    model: "google/gemini-2.5-flash"
  claude-opus:
    provider: openrouter
    model: "anthropic/claude-opus-4"

profiles:
  default:
    panel: [deepseek-v4, gemini-flash, kimi]
    judge: claude-opus
    final: claude-opus
  cheap:
    panel: [gemini-flash, kimi]
    judge: deepseek-v4
    final: deepseek-v4
  premium:
    panel: [claude-opus, deepseek-v4, gemini-flash]
    judge: claude-opus
    final: claude-opus
```

## Usage

### Non-streaming

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer your-secret-token" \
  -d '{
    "model": "default",
    "messages": [{"role": "user", "content": "Explain quantum computing"}]
  }'
```

### Streaming (SSE)

```bash
curl -N http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer your-secret-token" \
  -d '{
    "model": "default",
    "messages": [{"role": "user", "content": "Explain quantum computing"}],
    "stream": true
  }'
```

### Agentic (full conversation)

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer your-secret-token" \
  -d '{
    "model": "default",
    "messages": [
      {"role": "system", "content": "You are a helpful assistant."},
      {"role": "user", "content": "What is Rust?"},
      {"role": "assistant", "content": "Rust is a systems programming language..."},
      {"role": "user", "content": "How does its ownership system work?"}
    ]
  }'
```

Panel models receive the full conversation and answer the latest turn with full context.

## Use with agents

### Python (OpenAI SDK)

```python
from openai import OpenAI

client = OpenAI(base_url="http://localhost:8080/v1", api_key="your-secret-token")

# Single message
response = client.chat.completions.create(
    model="default",
    messages=[{"role": "user", "content": "Explain quantum computing"}]
)

# Full conversation
response = client.chat.completions.create(
    model="default",
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
    messages=[{"role": "user", "content": "Explain quantum computing"}],
    stream=True,
)
for chunk in stream:
    if chunk.choices[0].delta.content:
        print(chunk.choices[0].delta.content, end="")
```

### LangChain

```python
from langchain_openai import ChatOpenAI

llm = ChatOpenAI(model="default", base_url="http://localhost:8080/v1", api_key="your-secret-token")
```

## SSE events

```
data: {"type":"stage","stage":"panel"}
data: {"type":"content","model":"deepseek-v4","content":"..."}
data: {"type":"content","model":"gemini-flash","content":"..."}
data: {"type":"ping"}
data: {"type":"stage","stage":"judge"}
data: {"type":"content","model":"claude-opus","content":"..."}
data: {"type":"stage","stage":"final"}
data: {"type":"content","model":"claude-opus","content":"..."}
data: {"type":"result","data":{...}}
data: {"type":"done"}
```

Keepalive pings every 15s during long operations.

## Validation

Config is validated on startup:

```
config validation failed:
  - server.auth_token: required (used to authenticate /v1 requests)
  - providers.openrouter.api_key: required
  - profiles.default.panel[2]: unknown model "bar" (available: deepseek-v4, gemini-flash, ...)
```

## Endpoints

| Method | Path | Auth | Description |
|---|---|---|---|
| `POST` | `/v1/chat/completions` | Bearer | OpenAI-compatible chat completion |
| `GET` | `/v1/models` | Bearer | List available profiles and models |
| `GET` | `/health` | — | Health check |

## License

MIT
