// Package telemetry provides self-monitoring for OpenTrace.
// It tracks startup timing, store query latency, and background goroutine health.
package telemetry

import (
	"log/slog"
	"sync"
	"time"
)

// Startup tracks initialization timing for each subsystem.
type Startup struct {
	mu     sync.Mutex
	stages []Stage
	start  time.Time
}

// Stage records a single initialization step.
type Stage struct {
	Name     string        `json:"name"`
	Duration time.Duration `json:"duration_ms"`
}

// NewStartup begins tracking startup time.
func NewStartup() *Startup {
	return &Startup{start: time.Now()}
}

// Record records the duration of a startup stage.
func (s *Startup) Record(name string, start time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := time.Since(start)
	s.stages = append(s.stages, Stage{Name: name, Duration: d})
	slog.Info("startup stage complete", "stage", name, "duration", d.Round(time.Millisecond))
}

// Total returns the total startup duration.
func (s *Startup) Total() time.Duration {
	return time.Since(s.start)
}

// Stages returns all recorded stages.
func (s *Startup) Stages() []Stage {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Stage, len(s.stages))
	copy(out, s.stages)
	return out
}

// Summary returns a map suitable for JSON serialization.
func (s *Startup) Summary() map[string]any {
	stages := s.Stages()
	stageList := make([]map[string]any, len(stages))
	for i, st := range stages {
		stageList[i] = map[string]any{
			"name":        st.Name,
			"duration_ms": st.Duration.Milliseconds(),
		}
	}
	return map[string]any{
		"total_ms": s.Total().Milliseconds(),
		"stages":   stageList,
	}
}
