package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"mini-clawdbot/internal/toolruntime"
)

// BuildMVPTools 构建 Eino 可调用工具。
func BuildMVPTools(workingDir string, runtime *toolruntime.Runtime) ([]einotool.BaseTool, *toolruntime.Runtime, error) {
	wd, err := filepath.Abs(workingDir)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve working dir: %w", err)
	}

	rt := runtime
	if rt == nil {
		rt, err = toolruntime.BuildDefaultRuntime(toolruntime.DefaultRuntimeConfig{
			WorkingDir: wd,
		}, nil)
		if err != nil {
			return nil, nil, err
		}
	}

	tools := []einotool.BaseTool{
		&einoToolAdapter{
			runtime: rt,
			name:    "read_file",
			desc:    "Read file content by path",
			params: map[string]*schema.ParameterInfo{
				"path": {Type: schema.String, Desc: "Path to file", Required: true},
			},
		},
		&einoToolAdapter{
			runtime: rt,
			name:    "list_dir",
			desc:    "List directory entries by path",
			params: map[string]*schema.ParameterInfo{
				"path": {Type: schema.String, Desc: "Path to directory", Required: true},
			},
		},
		&einoToolAdapter{
			runtime: rt,
			name:    "run_shell",
			desc:    "Run shell command with security policy and sandbox",
			params: map[string]*schema.ParameterInfo{
				"command": {Type: schema.String, Desc: "Shell command", Required: true},
			},
		},
		&einoToolAdapter{
			runtime: rt,
			name:    "web_fetch",
			desc:    "Fetch URL by HTTP GET",
			params: map[string]*schema.ParameterInfo{
				"url": {Type: schema.String, Desc: "URL to fetch", Required: true},
			},
		},
		&einoToolAdapter{
			runtime: rt,
			name:    "browser_action",
			desc:    "Run browser action in host mode",
			params: map[string]*schema.ParameterInfo{
				"action": {Type: schema.String, Desc: "Browser action", Required: true},
			},
		},
	}
	return tools, rt, nil
}

type einoToolAdapter struct {
	runtime *toolruntime.Runtime
	name    string
	desc    string
	params  map[string]*schema.ParameterInfo
}

func (t *einoToolAdapter) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name:        t.name,
		Desc:        t.desc,
		ParamsOneOf: schema.NewParamsOneOfByParams(t.params),
	}, nil
}

func (t *einoToolAdapter) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...einotool.Option) (string, error) {
	args := map[string]any{}
	trimmed := strings.TrimSpace(argumentsInJSON)
	if trimmed != "" {
		if err := json.Unmarshal([]byte(trimmed), &args); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
	}

	result, err := t.runtime.Execute(ctx, t.name, args)
	if err != nil {
		if strings.TrimSpace(result.Output) != "" {
			return "", fmt.Errorf("%w: %s", err, result.Output)
		}
		return "", err
	}
	if result.Output == "" {
		return "(command completed with no output)", nil
	}
	return result.Output, nil
}

