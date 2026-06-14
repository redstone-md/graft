# Graft
多模型管道编排器，兼容 OpenAI API

语言切换：🇬🇧-English | 🇷🇺-Русский | 🇨🇳-简体中文

**Graft** 是一个编排器，它将你的提示同时发送给多个大语言模型，然后由一个 Judge 模型交叉对比它们的回答，生成结构化的分析报告，最后由一个 Final 模型综合出最优答案。

这不是"选最长的答案"，而是**分析 + 融合**。

## 工作原理

```
用户 → POST /v1/chat/completions
    ↓
[Panel] ──→ DeepSeek V4 ──→ answer1 ─┐
        ──→ Gemini Flash ──→ answer2 ─┤
        ──→ Kimi K2.6    ──→ answer3 ─┘
                                   ↓
                     [Judge] ──→ JSON 分析报告：
                                   • evaluations（各回答的质量评估）
                                   • consensus（共识观点）
                                   • contradictions（分歧点 + 谁更合理）
                                   • blind_spots（遗漏盲区）
                                   • recommendation（融合策略）
                                   ↓
                     [Final] ──→ 最终答案
```

### 阶段一：Panel（并行生成）
N 个模型接收**完整的对话历史**，各自独立回答。模型之间不存在交叉干扰。

### 阶段二：Judge（交叉对比）
Judge 模型收到所有 Panel 的回答，从以下维度**逐项评估**：
- 事实正确性
- 内容覆盖度
- 推理深度

然后构建交叉对比矩阵：哪些观点达成一致，哪些存在矛盾（以及谁更合理），哪些洞察是独家的，哪些话题无人涉及。

### 阶段三：Final（综合输出）
Final 模型根据 Judge 的分析报告，**撰写最终答案**，要求：
- 采纳各 Panel 回答中的精华
- 按照 Judge 的裁决解决矛盾
- 结合自身知识补全盲区
- 排除被 Judge 标记的错误

## 示例
**问题：** "洗车店离家 100 米——开车还是走路去？"

**Panel：**
- DeepSeek："走路去。100 米一分钟就到，开车浪费油……"
- Gemini："走路去。停车、启动发动机……"
- Kimi："开车去。洗车店就是用来洗车的——你得把车开过去。"

**Judge：**
- 矛盾：2 对 1。裁决："Kimi 说得对——你必须把车开到洗车店，否则没什么可洗的。"
- 盲区："问题暗含了车已经在家的假设——但并未明确说明。"

**Final：** "开车去。洗车店是用来清洗车辆的——走路过去意味着没车可洗。100 米的距离可以忽略不计，油耗也微乎其微。"

## 快速开始
```bash
# 下载二进制文件
# https://github.com/redstone-md/graft/releases

# 或从源码编译
git clone https://github.com/redstone-md/graft.git
cd graft
go build -o graft ./cmd/graft/

# 配置
cp config.example.yaml config.yaml
# 编辑 config.yaml —— 设置 auth_token 和 api_key

# 运行
./graft -config config.yaml
```

详细部署指南：**[docs/SETUP.zh.md](docs/SETUP.zh.md)**

## 使用方法

### Python（OpenAI SDK）
```python
from openai import OpenAI

client = OpenAI(base_url="http://localhost:8080/v1", api_key="your-token")

# 简单请求
response = client.chat.completions.create(
    model="default",
    messages=[{"role": "user", "content": "Explain quantum computing"}]
)

# 完整对话（适用于 Agent 场景）
response = client.chat.completions.create(
    model="premium",
    messages=[
        {"role": "system", "content": "You are a helpful assistant."},
        {"role": "user", "content": "What is Rust?"},
        {"role": "assistant", "content": "Rust is a systems programming language..."},
        {"role": "user", "content": "How does ownership work?"},
    ]
)

# 流式输出
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

## SSE 事件（流式）
```
data: {"type":"stage","stage":"panel"}
data: {"type":"content","model":"deepseek-v4","content":"..."}
data: {"type":"content","model":"gemini-flash","content":"..."}
data: {"type":"ping"}                                    ← 每 15 秒一次心跳
data: {"type":"stage","stage":"judge"}
data: {"type":"content","model":"claude-opus","content":"..."}
data: {"type":"stage","stage":"final"}
data: {"type":"content","model":"claude-opus","content":"..."}
data: {"type":"result","data":{...}}
data: {"type":"done"}
```

## 接口
| 方法 | 路径 | 鉴权 | 说明 |
|---|---|---|---|
| `POST` | `/v1/chat/completions` | Bearer | 兼容 OpenAI 的聊天补全接口 |
| `GET` | `/v1/models` | Bearer | 列出配置的模型和配置文件 |
| `GET` | `/health` | — | 健康检查 |

## 配置
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

完整配置指南：**[docs/SETUP.zh.md](docs/SETUP.zh.md)**

## 项目构想
Graft 是构建 Agent 系统的**基础设施组件**。你可以用它来构建：
- **多视角 Agent** —— 每条回复都经过多个模型交叉验证后再呈现
- **代码审查机器人** —— 多个编程模型并行分析 PR，Judge 找出冲突
- **研究助手** —— 自动从多源搜索、综合信息，并进行可信度评分
- **决策支持** —— 适用于容错率低的关键决策场景（法律、医疗、金融）
- **多模型容灾** —— 某个模型不可用时，其他模型自动接管
- **自定义管道** —— 为特定任务创建配置文件：简单问题走轻量模型，复杂问题走高阶模型

## 上下文与限制
Graft 会自动计算管道的有效上下文窗口：
```
effective_context = min(context_window) 管道中所有模型的最小值
```
例如同时使用 deepseek-v4（128K）和 gemini-flash（1M），有效上下文为 **128K** —— 因为 deepseek 无法处理更多内容。超出的消息会被自动裁剪。

更多细节：**[docs/SETUP.zh.md](docs/SETUP.zh.md#模型配置)**

## CI/CD
每次 push/PR 都会运行 `go vet` + `go build`。推送 `v*` 标签时，GitHub Actions 会自动构建 Linux、macOS 和 Windows 的二进制文件并创建 Release。
```bash
git tag v1.0.0
git push --tags
# → GitHub Actions 构建二进制文件并创建 Release
```

## 许可证
[MIT](LICENSE)
