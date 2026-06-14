# Fusion Orchestrator

Go + Gin prototype of multi-model fusion — OpenAI-compatible API with streaming and auth.

## Architecture

```
POST /v1/chat/completions (Authorization: Bearer <token>)
    ↓
model: "default" / "cheap" / "premium" / "fast"?
    ├── YES (fusion profile) → [Panel] → [Judge] → [Final] → SSE stream
    └── NO  (model ref)       → proxy to provider as-is
```

## Quick Start

```bash
cd fusion
cp config.example.yaml config.yaml
# edit config.yaml — set auth_token, api_key for providers

go run ./cmd/fusion/ -config config.yaml
```

## Configuration (config.yaml)

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

fusion:
  default:
    panel: [deepseek-v4, gemini-flash, kimi]
    judge: claude-opus
    final: claude-opus
  cheap:
    panel: [gemini-flash, kimi]
    judge: deepseek-v4
    final: deepseek-v4
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

SSE events (with keepalive pings every 15s):

```
data: {"type":"stage","stage":"panel"}
data: {"type":"content","model":"deepseek-v4","content":"Quantum computing is..."}
data: {"type":"content","model":"gemini-flash","content":"At its core..."}
data: {"type":"ping"}
data: {"type":"stage","stage":"judge"}
data: {"type":"content","model":"claude-opus","content":"..."}
data: {"type":"stage","stage":"final"}
data: {"type":"content","model":"claude-opus","content":"Quantum computing is a type of computation..."}
data: {"type":"result","data":{...}}
data: {"type":"done"}
```

### Direct model proxy (non-fusion)

Any model ref from `models:` section works as a pass-through:

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer your-secret-token" \
  -d '{
    "model": "deepseek-v4",
    "messages": [{"role": "user", "content": "Hello"}]
  }'
```

## Use with agents

### OpenCode

```json
{
  "provider": {
    "name": "fusion",
    "api_key": "your-secret-token",
    "base_url": "http://localhost:8080/v1"
  },
  "model": "default"
}
```

### Python (OpenAI SDK)

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://localhost:8080/v1",
    api_key="your-secret-token",
)

# Non-streaming
response = client.chat.completions.create(
    model="default",
    messages=[{"role": "user", "content": "Explain quantum computing"}]
)
print(response.choices[0].message.content)

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

llm = ChatOpenAI(
    model="default",
    base_url="http://localhost:8080/v1",
    api_key="your-secret-token",
)
```

## Validation

Config is validated on startup. Errors are clear:

```
config validation failed:
  - server.auth_token: required (used to authenticate /v1 requests)
  - providers.openrouter.api_key: required
  - models.deepseek-v4.provider: unknown provider "foo" (available: openrouter)
  - fusion.default.panel[2]: unknown model "bar" (available: deepseek-v4, gemini-flash, ...)
```

## Endpoints

| Method | Path | Auth | Description |
|---|---|---|---|
| `POST` | `/v1/chat/completions` | Bearer | OpenAI-compatible chat completion |
| `GET` | `/v1/models` | Bearer | List available models and profiles |
| `GET` | `/health` | — | Health check |
