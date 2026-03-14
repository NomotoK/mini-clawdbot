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
- `MINI_CLAW_FEISHU_ENABLED`（可选，`true/false`，默认 `false`，用于选择是否启用飞书渠道）
- `MINI_CLAW_FEISHU_APP_ID`（`MINI_CLAW_FEISHU_ENABLED=true` 时必填）
- `MINI_CLAW_FEISHU_APP_SECRET`（`MINI_CLAW_FEISHU_ENABLED=true` 时必填）
- `MINI_CLAW_FEISHU_ACCOUNT_ID`（可选，默认 `default`）
- `MINI_CLAW_FEISHU_VERIFICATION_TOKEN`（可选）
- `MINI_CLAW_FEISHU_ENCRYPT_KEY`（可选）

## .env 自动加载

启动时会自动读取项目根目录下的 `.env` 文件（如果存在），并注入环境变量。

示例 `.env`：

```dotenv
MINI_CLAW_API_KEY=your_api_key
MINI_CLAW_BASE_URL=https://api.openai.com/v1
MINI_CLAW_MODEL=gpt-4o-mini
MINI_CLAW_TIMEOUT_SEC=60
MINI_CLAW_FEISHU_ENABLED=false
MINI_CLAW_FEISHU_APP_ID=
MINI_CLAW_FEISHU_APP_SECRET=
MINI_CLAW_FEISHU_ACCOUNT_ID=default
MINI_CLAW_FEISHU_VERIFICATION_TOKEN=
MINI_CLAW_FEISHU_ENCRYPT_KEY=
```

优先级说明：

- 已经在 shell 中导出且值非空的环境变量优先级更高（不会被 `.env` 覆盖）
- `.env` 会补充未设置或值为空的变量

## 运行

```bash
go run ./cmd/mini-clawdbot -m "读取 README 并总结"
```

可选参数：

- `--max-step`：ReAct 最大步数（默认 6）
- `--model`：覆盖环境变量中的模型名

## Serve 模式（飞书）

先在 `.env` 中配置飞书应用信息并开启：

```dotenv
MINI_CLAW_FEISHU_ENABLED=true
MINI_CLAW_FEISHU_APP_ID=cli_xxx
MINI_CLAW_FEISHU_APP_SECRET=xxx
MINI_CLAW_FEISHU_ACCOUNT_ID=feishu-main
```

然后启动常驻服务：

```bash
go run ./cmd/mini-clawdbot --serve
```
