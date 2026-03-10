package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LoadDotEnvIfExists 从指定路径加载 .env 文件（若文件不存在则忽略）。
//
// 行为规则：
// - 文件不存在：返回 nil（不视为错误）
// - 已存在且非空的系统环境变量：不覆盖（系统环境优先）
// - 支持行格式：KEY=VALUE 或 export KEY=VALUE
// - 支持注释：空行或以 # 开头的行
//
// 参数：
// - path: .env 文件路径
//
// 返回：
// - error: 文件可读但格式非法时返回错误
func LoadDotEnvIfExists(path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve dotenv path: %w", err)
	}

	file, err := os.Open(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("open dotenv file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())

		// 跳过空行和注释行。
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// 兼容 export KEY=VALUE 格式。
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}

		key, val, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("invalid .env line %d: missing '='", lineNo)
		}

		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if key == "" {
			return fmt.Errorf("invalid .env line %d: empty key", lineNo)
		}

		parsedVal, err := parseEnvValue(val)
		if err != nil {
			return fmt.Errorf("invalid .env line %d: %w", lineNo, err)
		}

		// 不覆盖已有且非空环境变量，保持 shell/export 的最高优先级。
		// 若变量已存在但为空字符串，允许 .env 回填默认值。
		if existing, exists := os.LookupEnv(key); exists && strings.TrimSpace(existing) != "" {
			continue
		}
		if err := os.Setenv(key, parsedVal); err != nil {
			return fmt.Errorf("set env %s: %w", key, err)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read dotenv file: %w", err)
	}
	return nil
}

// parseEnvValue 解析 VALUE 文本。
//
// 支持：
// - 双引号："..."（支持常见转义）
// - 单引号：'...'（原样返回）
// - 未加引号：去掉末尾注释（空格+# 之后）
func parseEnvValue(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}

	if strings.HasPrefix(raw, "\"") {
		if !strings.HasSuffix(raw, "\"") || len(raw) < 2 {
			return "", fmt.Errorf("unterminated double quote")
		}
		inner := raw[1 : len(raw)-1]
		replacer := strings.NewReplacer(`\\n`, "\n", `\\t`, "\t", `\\r`, "\r", `\\\"`, `\"`, `\\\\`, `\\`)
		return replacer.Replace(inner), nil
	}

	if strings.HasPrefix(raw, "'") {
		if !strings.HasSuffix(raw, "'") || len(raw) < 2 {
			return "", fmt.Errorf("unterminated single quote")
		}
		return raw[1 : len(raw)-1], nil
	}

	// 处理未加引号情况下的行尾注释：VALUE # comment。
	if idx := strings.Index(raw, " #"); idx >= 0 {
		raw = raw[:idx]
	}

	return strings.TrimSpace(raw), nil
}
