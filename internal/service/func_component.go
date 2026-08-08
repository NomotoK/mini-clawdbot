package service

import "context"

// FuncComponent 用函数快速适配 Component。
type FuncComponent struct {
	NameValue string
	InitFn    func(context.Context) error
	StartFn   func(context.Context) error
	StopFn    func(context.Context) error
	HealthFn  func(context.Context) HealthStatus
}

func (f FuncComponent) Init(ctx context.Context) error {
	if f.InitFn == nil {
		return nil
	}
	return f.InitFn(ctx)
}

func (f FuncComponent) Start(ctx context.Context) error {
	if f.StartFn == nil {
		return nil
	}
	return f.StartFn(ctx)
}

func (f FuncComponent) Stop(ctx context.Context) error {
	if f.StopFn == nil {
		return nil
	}
	return f.StopFn(ctx)
}

func (f FuncComponent) Health(ctx context.Context) HealthStatus {
	if f.HealthFn != nil {
		h := f.HealthFn(ctx)
		if h.Name == "" {
			h.Name = f.NameValue
		}
		return h
	}
	return HealthStatus{Name: f.NameValue, Status: StatusLive}
}

