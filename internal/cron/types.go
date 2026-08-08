package cron

import "time"

// PayloadType 表示任务执行类型。
type PayloadType string

const (
	PayloadTypeAgentTurn PayloadType = "agent_turn"
	PayloadTypeShellTask PayloadType = "shell_task"
)

// Job 定义 cron 任务。
type Job struct {
	ID           string      `json:"id"`
	Name         string      `json:"name"`
	Enabled      bool        `json:"enabled"`
	EverySeconds int         `json:"every_seconds"`
	PayloadType  PayloadType `json:"payload_type"`
	SessionKey   string      `json:"session_key,omitempty"`
	Message      string      `json:"message,omitempty"`
	Command      string      `json:"command,omitempty"`
	CreatedAt    time.Time   `json:"created_at"`
	UpdatedAt    time.Time   `json:"updated_at"`
	NextRunAt    time.Time   `json:"next_run_at"`
	LastRunAt    time.Time   `json:"last_run_at"`
}

// RunRecord 表示一次任务执行记录。
type RunRecord struct {
	RunID      string    `json:"run_id"`
	JobID      string    `json:"job_id"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Status     string    `json:"status"`
	Error      string    `json:"error,omitempty"`
	Output     string    `json:"output,omitempty"`
}

// Config 定义 cron 服务配置。
type Config struct {
	RootDir      string
	PollInterval time.Duration
}

