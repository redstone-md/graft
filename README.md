<p align="center">
  <h1 align="center">Graft</h1>
  <p align="center">Multi-model pipeline orchestrator with OpenAI-compatible API</p>
</p>

<p align="center">
  <a href="https://github.com/redstone-md/graft/actions"><img src="https://github.com/redstone-md/graft/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/redstone-md/graft/releases"><img src="https://img.shields.io/github/v/release/redstone-md/graft" alt="Release"></a>
  <a href="https://github.com/redstone-md/graft/blob/main/LICENSE"><img src="https://img.shields.io/github/license/redstone-md/graft" alt="License"></a>
  <a href="https://pkg.go.dev/github.com/redstone-md/graft"><img src="https://pkg.go.dev/badge/github.com/redstone-md/graft.svg" alt="Go Reference"></a>
</p>

---

**Graft** — это оркестратор, который прогоняет ваш промпт через несколько LLM параллельно, затем судья-модель кросс-сравнивает ответы и выдаёт структурированный анализ, а финальная модель синтезирует лучший возможный ответ.

Это не "выбрать самое длинное". Это **анализ + слияние**.

## Как это работает

```
Пользователь → POST /v1/chat/completions
    ↓
[Panel] ──→ DeepSeek V4 ──→ ответ1 ─┐
        ──→ Gemini Flash ──→ ответ2 ─┤
        ──→ Kimi K2.6    ──→ ответ3 ─┘
                                      ↓
                        [Judge] ──→ JSON-анализ:
                                      • evaluations (оценка каждого ответа)
                                      • consensus (общие моменты)
                                      • contradictions (противоречия + кто прав)
                                      • blind_spots (чего не хватает)
                                      • recommendation (стратегия слияния)
                                      ↓
                        [Final] ──→ финальный ответ
```

### Этап 1: Panel (параллельно)

N моделей получают **полную историю диалога** и отвечают независимо друг от друга. Каждая модель работает со своим контекстом — никакого перекрёстного влияния.

### Этап 2: Judge (сравнение)

Модель-судья получает все ответы панели и **оценивает каждый** по оси:
- Фактическая корректность
- Полнота покрытия
- Глубина рассуждений

Затем строит кросс-сравнение: где согласие, где противоречия (и кто прав), какие инсайты уникальны, каких тем нет ни у кого.

### Этап 3: Final (синтез)

Модель-синтезатор берёт анализ судьи и **пишет единый ответ**, который:
- Берёт лучшее из каждого ответа панели
- Разрешает противоречия по вердикту судьи
- Покрывает blind spots из своего знания
- Исключает ошибки отмеченные судьёй

## Пример

**Вопрос:** "Мойка машин в 100м от дома — идти пешком или ехать на машине?"

**Панель:**
- DeepSeek: "Идти пешком. 100м — это минута, машина тратит топливо..."
- Gemini: "Идти пешком. Парковка, запуск двигателя..."
- Kimi: "Ехать на машине. Смысл мойки — помыть машину, а она должна быть на месте."

**Judge:**
- Противоречие: 2 против 1. Verdict: "Kimi прав — на мойку нужно приехать на машине, иначемыть нечего."
- Blind spot: "Вопрос подразумевает что машина уже дома — это не было явно сказано."

**Final:** "Ехать на машину. Мойка предназначена для мытья автомобиля — если вы придёте пешком, машину помыть не удастся. 100м — расстояние пренебрежимо малое, расход топлива несущественный."

## Быстрый старт

```bash
# Скачайте бинарник
# https://github.com/redstone-md/graft/releases

# Или соберите из исходников
git clone https://github.com/redstone-md/graft.git
cd graft
go build -o graft ./cmd/graft/

# Настройте конфиг
cp config.example.yaml config.yaml
# отредактируйте config.yaml — впишите auth_token и api_key

# Запустите
./graft -config config.yaml
```

Подробная инструкция по настройке: **[docs/SETUP.md](docs/SETUP.md)**

## Использование

### Python (OpenAI SDK)

```python
from openai import OpenAI

client = OpenAI(base_url="http://localhost:8080/v1", api_key="ваш-токен")

# Простой запрос
response = client.chat.completions.create(
    model="default",
    messages=[{"role": "user", "content": "Объясни квантовые вычисления"}]
)

# Полный диалог (agentic)
response = client.chat.completions.create(
    model="premium",
    messages=[
        {"role": "system", "content": "Ты полезный ассистент."},
        {"role": "user", "content": "Что такое Rust?"},
        {"role": "assistant", "content": "Rust — язык системного программирования..."},
        {"role": "user", "content": "Как работает система владения?"},
    ]
)

# Стриминг
stream = client.chat.completions.create(
    model="default",
    messages=[{"role": "user", "content": "Привет"}],
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
    api_key="ваш-токен",
)
```

### curl

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ваш-токен" \
  -d '{
    "model": "default",
    "messages": [{"role": "user", "content": "Привет!"}]
  }'
```

## SSE события (стриминг)

```
data: {"type":"stage","stage":"panel"}
data: {"type":"content","model":"deepseek-v4","content":"..."}
data: {"type":"content","model":"gemini-flash","content":"..."}
data: {"type":"ping"}                                    ← keepalive каждые 15с
data: {"type":"stage","stage":"judge"}
data: {"type":"content","model":"claude-opus","content":"..."}
data: {"type":"stage","stage":"final"}
data: {"type":"content","model":"claude-opus","content":"..."}
data: {"type":"result","data":{...}}
data: {"type":"done"}
```

## Endpoints

| Метод | Путь | Auth | Описание |
|---|---|---|---|
| `POST` | `/v1/chat/completions` | Bearer | OpenAI-совместимый chat completion |
| `GET` | `/v1/models` | Bearer | Список профилей и моделей |
| `GET` | `/health` | — | Health check |

## Конфигурация

```yaml
server:
  port: "8080"
  auth_token: "ваш-токен"

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

Полное описание конфигурации: **[docs/SETUP.md](docs/SETUP.md)**

## Идеи проекта

Graft создан как **building block** для агентных систем. Вот что можно построить:

- **Агент-ассистент** с multiple-perspective reasoning — каждый ответ проверяется несколькими моделями перед выдачей
- **Code review бот** — панель из coding-моделей параллельно анализирует PR, судья находит конфликты
- **Research assistant** — автоматический поиск + синтез из нескольких источников с оценкой достоверности
- **Decision support** — для критических решений когда ошибка дорога (юридические, медицинские, финансовые вопросы)
- **Multi-model fallback** — если одна модель упала, другие продолжают работу
- **Кастомные пайплайны** — создавайте профили под конкретные задачи: дешёвые для простых вопросов, премиум для сложных

## Контекст и лимиты

Graft автоматически считает эффективный контекст пайплайна:

```
effective_context = min(context_window) всех моделей в pipeline
```

Если у вас deepseek-v4 (128K) и gemini-flash (1M), контекст будет **128K** — потому что deepseek не влезет больше. Старые сообщения обрезаются автоматически.

Подробнее: **[docs/SETUP.md](docs/SETUP.md#настройка-моделей)**

## CI/CD

Каждый push/PR запускает `go vet` + `go build`. На теги `v*` автоматически создаётся релиз с бинарниками для Linux, macOS и Windows.

```bash
git tag v1.0.0
git push --tags
# → GitHub Actions соберёт бинарники и создаст Release
```

## Лицензия

[MIT](LICENSE)
