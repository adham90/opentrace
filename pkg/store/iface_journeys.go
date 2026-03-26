package store

import (
	"context"
	"time"
)

// JourneyStore manages user sessions, journey reconstruction, path analysis, and funnels.
type JourneyStore interface {
	// Session management
	BuildSessions(ctx context.Context, since time.Time) error
	GetSession(ctx context.Context, sessionID string) (*UserSession, error)
	ListSessions(ctx context.Context, params SessionListParams) ([]UserSession, error)

	// Journey queries
	GetSessionRequests(ctx context.Context, sessionID string) ([]RequestStep, error)
	GetUserJourney(ctx context.Context, userID string, since time.Time, limit int) ([]RequestStep, error)

	// Path analysis
	CommonPaths(ctx context.Context, params PathAnalysisParams) ([]PathFrequency, error)

	// Funnels
	CreateFunnel(ctx context.Context, funnel Funnel) (*Funnel, error)
	GetFunnel(ctx context.Context, id int64) (*Funnel, error)
	ListFunnels(ctx context.Context) ([]Funnel, error)
	AnalyzeFunnel(ctx context.Context, funnelID int64, since time.Time) (*FunnelResult, error)
	DeleteFunnel(ctx context.Context, id int64) error

	// Timeline
	GetRequestTimeline(ctx context.Context, logID int64) (*RequestTimeline, error)
	GetSessionTimeline(ctx context.Context, sessionID string) ([]RequestTimeline, error)
}
