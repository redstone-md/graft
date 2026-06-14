# 安装和配置 Graft

## 环境要求

- Go 1.23+（从源码编译时需要）
- 或直接从 [Releases](https://github.com/redstone-md/graft/releases) 下载预编译二进制文件
- 一个或多个 LLM 提供商的 API 密钥（OpenRouter、OpenAI、DeepSeek 等）

## 快速开始

### 1. 下载或编译

```bash
# 从源码编译
git clone https://github.com/redstone-md/graft.git
cd graft
go build -o graft ./cmd/graft/

# 或直接下载预编译二进制文件
# https://github.com/redstone-md/graft/releases
```

### 2. 创建配置文件

```bash
cp config.example.yaml config.yaml
```

### 3. 编辑配置

打开 `config.yaml` 并进行如下配置：

```yaml
server:
  port: "8080"
  auth_token: "your-project-token"

providers:
  openrouter:
    base_url: "https://openrouter.ai/api/v1"
    api_key: "sk-or-v1-your-key"

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

### 4. 启动

```bash
./graft -config config.yaml
```

服务将在 `http://localhost:8080` 启动。

---

## 配置 LLM 提供商

### OpenRouter（推荐）

[openrouter.ai](https://openrouter.ai) 是一个统一的 LLM API 网关，一个密钥即可访问所有模型。

```yaml
providers:
  openrouter:
    base_url: "https://openrouter.ai/api/v1"
    api_key: "sk-or-v1-..."
```

### 直接使用 OpenAI

```yaml
providers:
  openai:
    base_url: "https://api.openai.com/v1"
    api_key: "sk-..."
```

### 直接使用 DeepSeek

```yaml
providers:
  deepseek:
    base_url: "https://api.deepseek.com/v1"
    api_key: "sk-..."
```

### 混合使用多个提供商

可以同时配置多个提供商，不同模型使用不同的提供商：

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

## 配置模型

每个模型的配置需要指定所属提供商、模型 ID 以及上下文窗口大小：

```yaml
models:
  deepseek-v4:
    provider: openrouter
    model: "deepseek/deepseek-v4-pro"
    context_window: 131072
```

### context_window 的作用

Graft 会自动计算整个流水线的**有效上下文长度**，取所有模型 `context_window` 的最小值。例如，同时使用 deepseek-v4（128K）和 gemini-flash（1M），有效上下文长度为 128K，因为 deepseek-v4 的上下文窗口更小。

超出限制的历史消息会被自动截断。

### 常用模型及其上下文窗口

| 模型 | context_window |
|---|---|
| deepseek/deepseek-v4-pro | 131072 |
| google/gemini-2.5-flash | 1048576 |
| google/gemini-2.5-pro | 1048576 |
| anthropic/claude-opus-4 | 200000 |
| anthropic/claude-haiku-4.5 | 200000 |
| moonshotai/kimi-k2.6 | 131072 |

---

## 配置 Profile

Profile 定义了流水线中各阶段使用的模型组合：Panel → Judge → Final。

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

### 选择 Profile

在请求的 `model` 字段中指定 Profile 名称即可：

```json
{"model": "default", "messages": [...]}
{"model": "cheap",   "messages": [...]}
{"model": "premium", "messages": [...]}
```

---

## 认证

所有发往 `/v1/*` 的请求都需要携带 `Authorization` 请求头：

```
Authorization: Bearer your-token
```

Token 在配置文件中设置：

```yaml
server:
  auth_token: "your-token"
```

未携带有效 Token 时，服务器将返回 `401 Unauthorized`。

---

## 测试

### 健康检查

```bash
curl http://localhost:8080/health
# {"status":"ok"}
```

### 获取模型列表

```bash
curl http://localhost:8080/v1/models \
  -H "Authorization: Bearer your-token"
```

### 发送请求

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer your-token" \
  -d '{
    "model": "default",
    "messages": [{"role": "user", "content": "Hello! Tell me about Go"}]
  }'
```

### 流式响应

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

## 作为 systemd 服务运行

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
