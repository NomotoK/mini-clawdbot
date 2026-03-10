package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"mini-clawdbot/internal/app"
)

// main 是 CLI 程序入口。
//
// 执行流程：
// 1. 解析命令行参数（用户输入、最大步数、模型覆盖）
// 2. 校验必填参数 -m/--message
// 3. 初始化应用依赖（配置、模型、工具、ReAct Agent）
// 4. 执行单轮 RunOnce 并输出最终结果
//
// 退出码约定：
// - 0：成功
// - 1：运行失败（配置/模型/执行错误）
// - 2：参数错误（缺少必填 message）
func main() {
	// message: 本轮用户输入文本。
	// maxStep: ReAct 图执行的最大步数上限。
	// model: 可选模型覆盖值，优先级高于环境变量 MINI_CLAW_MODEL。
	var (
		message string
		maxStep int
		model   string
	)

	// 注册命令行参数。
	flag.StringVar(&message, "message", "", "User message to run one ReAct turn")
	flag.StringVar(&message, "m", "", "User message to run one ReAct turn (shorthand)")
	flag.IntVar(&maxStep, "max-step", 6, "Maximum ReAct steps")
	flag.StringVar(&model, "model", "", "Override MINI_CLAW_MODEL")
	flag.Parse()

	// message 是必须参数；为空时输出帮助并终止。
	if strings.TrimSpace(message) == "" {
		fmt.Fprintln(os.Stderr, "missing required -m/--message")
		flag.Usage()
		os.Exit(2)
	}

	// MVP 用 Background context 即可；后续可扩展为超时/取消上下文。
	ctx := context.Background()

	// 创建应用对象，内部会构建配置、模型、工具和 ReAct 运行器。
	application, err := app.New(ctx, app.Options{MaxStep: maxStep, ModelOverride: model})
	if err != nil {
		reportError(err)
		os.Exit(1)
	}

	// 执行单轮请求。
	out, err := application.RunOnce(ctx, message)
	if err != nil {
		reportError(err)
		os.Exit(1)
	}

	// 成功时仅输出最终文本，便于脚本管道处理。
	fmt.Println(out)
}

// reportError 负责统一错误输出和最小化故障排查提示。
//
// 参数：
// - err: 需要展示给用户的错误。
//
// 返回值：
// - 无（直接输出到 stderr）。
func reportError(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)

	// help 场景不追加额外提示，避免噪音。
	if errors.Is(err, flag.ErrHelp) {
		return
	}

	// 针对最常见配置错误提供可执行建议。
	if strings.Contains(err.Error(), "MINI_CLAW_API_KEY") {
		fmt.Fprintln(os.Stderr, "hint: export MINI_CLAW_API_KEY first")
	}
}
