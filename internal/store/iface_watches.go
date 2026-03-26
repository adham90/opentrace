package store

import (
	"context"
	"time"
)

// WatchStore manages agent-first watches (Phase 1).
type WatchStore interface {
	Create(ctx context.Context, params CreateWatchParams) (*Watch, error)
	GetByID(ctx context.Context, id string) (*Watch, error)
	List(ctx context.Context, params ListWatchParams) ([]Watch, error)
	UpdateStatus(ctx context.Context, id string, status WatchStatus) error
	UpdateAfterCheck(ctx context.Context, id string, value float64, breaches int, nextCheck time.Time) error
	UpdateBaseline(ctx context.Context, id string, baseline *WatchBaseline) error
	Delete(ctx context.Context, id string) error
	GetDueWatches(ctx context.Context) ([]Watch, error)
	ExpireWatches(ctx context.Context) (int, error)
	CreateRun(ctx context.Context, watchID string) (*WatchRun, error)
	CompleteRun(ctx context.Context, id string, value float64, breached bool, summary string) error
	FailRun(ctx context.Context, id string, errMsg string) error
	ListRuns(ctx context.Context, watchID string, limit int) ([]WatchRun, error)
	CreateAlert(ctx context.Context, params CreateWatchAlertParams) (*WatchAlert, error)
	GetAlert(ctx context.Context, id string) (*WatchAlert, error)
	ListAlerts(ctx context.Context, watchID string, status string, limit int) ([]WatchAlert, error)
	DismissAlert(ctx context.Context, id string, reason string) error
	AcknowledgeAlert(ctx context.Context, id string) error
	CountPendingAlerts(ctx context.Context) (int, error)
}
