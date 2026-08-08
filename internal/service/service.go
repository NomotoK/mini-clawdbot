package service

import "context"

// Status 表示服务健康状态。
type Status string

const (
	StatusReady    Status = "ready"
	StatusLive     Status = "live"
	StatusDegraded Status = "degraded"
	StatusStopped  Status = "stopped"
)

// HealthStatus 是单个服务或聚合服务的健康信息。
type HealthStatus struct {
	Name    string         `json:"name"`
	Status  Status         `json:"status"`
	Details map[string]any `json:"details,omitempty"`
}

// Component 定义统一生命周期接口。
type Component interface {
	Init(ctx context.Context) error
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Health(ctx context.Context) HealthStatus
}

// NamedComponent 绑定组件名称与实例。
type NamedComponent struct {
	Name      string
	Component Component
}

