# Installing and Configuring Graft

## Requirements

- Go 1.23+ (for building from source)
- Or download a pre-built binary from [Releases](https://github.com/redstone-md/graft/releases)
- API key for one or more providers (OpenRouter, OpenAI, DeepSeek, etc.)

## Quick Start

### 1. Download or build

```bash
git clone https://github.com/redstone-md/graft.git
cd graft
go build -o graft ./cmd/graft/
```

### 2. Create a config

```bash
cp config.example.yaml config.yaml
```

### 3. Fill in the config

```yaml
server:
  port: "8080"
  auth_token: "your-secret-token"

providers:
  openrouter:
    base_url: "https://openrouter.ai/api/v1"
    api_key: "sk-or-v1-..."

models:
  deepseek-v4:
    provider: openrouter
    model: "deepseek/deepseek-v4-pro"
    context_window: 131072

  gemini-flash:
    provider: openrouter
    model: "google/gemini-2.5-flash"
    context_window: 1048576

  claude-opus:
    provider: openrouter
    model: "anthropic/claude-opus-4"
    context_window: 200000

profiles:
  default:
    panel:
      - deepseek-v4
      - gemini-flash
    judge: claude-opus
    final: claude-opus
```

### 4. Run

```bash
./graft -config config.yaml
```

Server starts on `http://localhost:8080`.

---

## Provider Setup

### OpenRouter (recommended)

[openrouter.ai](https://openrouter.ai) — single API for all models. One key = access to everything.

```yaml
providers:
  openrouter:
    base_url: "https://openrouter.ai/api/v1"
    api_key: "sk-or-v1-..."
```

### OpenAI directly

```yaml
providers:
  openai:
    base_url: "https://api.openai.com/v1"
    api_key: "sk-..."
```

### DeepSeek directly

```yaml
providers:
  deepseek:
    base_url: "https://api.deepseek.com/v1"
    api_key: "sk-..."
```

### Mixed providers

You can mix — one model on OpenRouter, another on OpenAI:

```yaml
providers:
  openrouter:
    base_url: "https://openrouter.ai/api/v1"
    api_key: "sk-or-v1-..."
  openai:
    base_url: "https://api.openai.com/v1"
    api_key: "sk-..."

models:
  deepseek-v4:
    provider: openrouter
    model: "deepseek/deepseek-v4-pro"
  gpt-5:
    provider: openai
    model: "gpt-5"
```

---

## Model Setup

Each model is a reference to a provider + model ID + context size:

```yaml
models:
  deepseek-v4:
    provider: openrouter
    model: "deepseek/deepseek-v4-pro"
    context_window: 131072
```

### Why context_window?

Graft automatically calculates the **effective context** of the pipeline as `min(context_window)` of all models. If you have deepseek-v4 (128K) and gemini-flash (1M), context will be 128K — because deepseek-v4 can't handle more.

Old messages are automatically trimmed to fit within the limit.

### Popular models and their context

| Model | context_window |
|---|---|
| deepseek/deepseek-v4-pro | 131072 |
| google/gemini-2.5-flash | 1048576 |
| google/gemini-2.5-pro | 1048576 |
| anthropic/claude-opus-4 | 200000 |
| anthropic/claude-haiku-4.5 | 200000 |
| moonshotai/kimi-k2.6 | 131072 |

---

## Profile Setup

A profile is a set of models for the pipeline: Panel → Judge → Final.

```yaml
profiles:
  default:
    panel:
      - deepseek-v4
      - gemini-flash
      - kimi
    judge: claude-opus
    final: claude-opus

  cheap:
    panel:
      - gemini-flash
      - kimi
    judge: deepseek-v4
    final: deepseek-v4

  premium:
    panel:
      - claude-opus
      - deepseek-v4
      - gemini-flash
    judge: claude-opus
    final: claude-opus
```

### How to select a profile

Specify the profile name in the `model` field of your request:

```json
{"model": "default", "messages": [...]}
{"model": "cheap",   "messages": [...]}
{"model": "premium", "messages": [...]}
```

---

## Authentication

All requests to `/v1/*` require the `Authorization` header:

```
Authorization: Bearer your-token
```

The token is set in the config:

```yaml
server:
  auth_token: "your-secret-token"
```

Without a token, the server returns `401 Unauthorized`.

---

## Testing

### Health check

```bash
curl http://localhost:8080/health
# {"status":"ok"}
```

### List models

```bash
curl http://localhost:8080/v1/models \
  -H "Authorization: Bearer your-token"
```

### Request

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer your-token" \
  -d '{
    "model": "default",
    "messages": [{"role": "user", "content": "Hello! Tell me about Go"}]
  }'
```

### Streaming

```bash
curl -N http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer your-token" \
  -d '{
    "model": "default",
    "messages": [{"role": "user", "content": "Hello! Tell me about Go"}],
    "stream": true
  }'
```

---

## Running as a systemd service

```ini
# /etc/systemd/system/graft.service
[Unit]
Description=Graft Multi-Model Orchestrator
After=network.target

[Service]
Type=simple
User=graft
ExecStart=/usr/local/bin/graft -config /etc/graft/config.yaml
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable graft
sudo systemctl start graft
```

---

## Docker

```dockerfile
FROM golang:1.23-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o graft ./cmd/graft/

FROM alpine:3.20
RUN apk --no-cache add ca-certificates
COPY --from=build /app/graft /usr/local/bin/graft
COPY config.example.yaml /etc/graft/config.yaml
EXPOSE 8080
ENTRYPOINT ["graft", "-config", "/etc/graft/config.yaml"]
```

```bash
docker build -t graft .
docker run -p 8080:8080 -v ./config.yaml:/etc/graft/config.yaml graft
```
