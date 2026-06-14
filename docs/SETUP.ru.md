# Установка и настройка Graft

## Требования

- Go 1.23+ (для сборки из исходников)
- Или скачайте готовый бинарник из [Releases](https://github.com/redstone-md/graft/releases)
- API-ключ одного или нескольких провайдеров (OpenRouter, OpenAI, DeepSeek и т.д.)

## Быстрый старт

### 1. Скачайте или соберите

```bash
# Из исходников
git clone https://github.com/redstone-md/graft.git
cd graft
go build -o graft ./cmd/graft/

# Или скачайте готовый бинарник
# https://github.com/redstone-md/graft/releases
```

### 2. Создайте конфиг

```bash
cp config.example.yaml config.yaml
```

### 3. Заполните конфиг

Откройте `config.yaml` и настройте:

```yaml
server:
  port: "8080"
  auth_token: "любая-строка-для-вашего-проекта"  # ваш пароль для доступа

providers:
  openrouter:
    base_url: "https://openrouter.ai/api/v1"
    api_key: "sk-or-v1-ваш-ключ"

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

### 4. Запустите

```bash
./graft -config config.yaml
```

Сервер стартует на `http://localhost:8080`.

---

## Настройка провайдеров

### OpenRouter (рекомендуется)

[openrouter.ai](https://openrouter.ai) — единый API ко всем моделям. Один ключ = доступ ко всем.

```yaml
providers:
  openrouter:
    base_url: "https://openrouter.ai/api/v1"
    api_key: "sk-or-v1-..."
```

### OpenAI напрямую

```yaml
providers:
  openai:
    base_url: "https://api.openai.com/v1"
    api_key: "sk-..."
```

### DeepSeek напрямую

```yaml
providers:
  deepseek:
    base_url: "https://api.deepseek.com/v1"
    api_key: "sk-..."
```

### Смешанные провайдеры

Можно комбинировать — одна модель у OpenRouter, другая у OpenAI:

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

## Настройка моделей

Каждая модель — это ссылка на провайдер + ID модели + размер контекста:

```yaml
models:
  deepseek-v4:
    provider: openrouter              # ключ из providers
    model: "deepseek/deepseek-v4-pro" # ID модели на провайдере
    context_window: 131072            # макс. токенов (input + output)
```

### Зачем context_window?

Graft автоматически считает **эффективный контекст** пайплайна как `min(context_window)` всех моделей. Если у вас deepseek-v4 (128K) и gemini-flash (1M), контекст будет 128K — потому что deepseek-v4 не влезет больше.

Старые сообщения автоматически обрезаются, чтобы влезть в лимит.

### Популярные модели и их контекст

| Модель | context_window |
|---|---|
| deepseek/deepseek-v4-pro | 131072 |
| google/gemini-2.5-flash | 1048576 |
| google/gemini-2.5-pro | 1048576 |
| anthropic/claude-opus-4 | 200000 |
| anthropic/claude-haiku-4.5 | 200000 |
| moonshotai/kimi-k2.6 | 131072 |

---

## Настройка профилей

Профиль — это набор моделей для пайплайна: panel → judge → final.

```yaml
profiles:
  # Сбалансированный
  default:
    panel:                    # модели которые отвечают параллельно
      - deepseek-v4
      - gemini-flash
      - kimi
    judge: claude-opus        # модель-судья (анализирует ответы панели)
    final: claude-opus        # модель-синтезатор (пишет финальный ответ)

  # Дешёвый
  cheap:
    panel:
      - gemini-flash
      - kimi
    judge: deepseek-v4
    final: deepseek-v4

  # Максимальное качество
  premium:
    panel:
      - claude-opus
      - deepseek-v4
      - gemini-flash
    judge: claude-opus
    final: claude-opus
```

### Как выбрать профиль

При запросе укажите имя профиля в поле `model`:

```json
{"model": "default", "messages": [...]}
{"model": "cheap",   "messages": [...]}
{"model": "premium", "messages": [...]}
```

---

## Auth (аутентификация)

Все запросы к `/v1/*` требуют заголовок `Authorization`:

```
Authorization: Bearer ваш-токен
```

Токен задаётся в конфиге:

```yaml
server:
  auth_token: "любая-строка"
```

Без токена сервер вернёт `401 Unauthorized`.

---

## Тестирование

### Health check

```bash
curl http://localhost:8080/health
# {"status":"ok"}
```

### Список моделей

```bash
curl http://localhost:8080/v1/models \
  -H "Authorization: Bearer ваш-токен"
```

### Запрос

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ваш-токен" \
  -d '{
    "model": "default",
    "messages": [{"role": "user", "content": "Привет! Расскажи про Go"}]
  }'
```

### Стриминг

```bash
curl -N http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ваш-токен" \
  -d '{
    "model": "default",
    "messages": [{"role": "user", "content": "Привет! Расскажи про Go"}],
    "stream": true
  }'
```

---

## Запуск как systemd-сервис

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
