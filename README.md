# mini-clawdbot

一个基于 Eino 的最小 ReAct Agent MVP。

## 功能

- 单轮 CLI 入口（一次输入 -> ReAct 工具调用 -> 一次输出）
- OpenAI 兼容模型接入（通过 Eino OpenAI 扩展）
- 内置工具：`read_file`、`list_dir`、`run_shell`

## 环境变量

- `MINI_CLAW_API_KEY`（必填）
- `MINI_CLAW_BASE_URL`（可选，OpenAI 兼容网关地址）
- `MINI_CLAW_MODEL`（可选，默认 `gpt-4o-mini`）
- `MINI_CLAW_TIMEOUT_SEC`（可选，默认 `60`）

## 运行

```bash
go run ./cmd/mini-clawdbot -m "读取 README 并总结"
```

可选参数：

- `--max-step`：ReAct 最大步数（默认 6）
- `--model`：覆盖环境变量中的模型名
