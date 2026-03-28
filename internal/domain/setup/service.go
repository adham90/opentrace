package setup

import (
	"context"
	"fmt"

	"github.com/adham90/opentrace/internal/domain"
	"github.com/adham90/opentrace/pkg/store"
)

// Service provides first-run onboarding status and system detection.
type Service struct {
	logs        LogCounter
	users       UserCounter
	dataSources DataSourceLister
}

// NewService creates a setup service.
func NewService(logs LogCounter, users UserCounter, dataSources DataSourceLister) *Service {
	return &Service{logs: logs, users: users, dataSources: dataSources}
}

// StatusResult summarizes the system's setup progress.
type StatusResult struct {
	HasUsers       bool `json:"has_users"`
	HasLogs        bool `json:"has_logs"`
	HasDataSources bool `json:"has_data_sources"`
	IsComplete     bool `json:"is_complete"`
}

// Status checks whether the system has been set up (users, logs, data sources).
func (s *Service) Status(ctx context.Context) (*StatusResult, error) {
	result := &StatusResult{}

	if s.users != nil {
		count, err := s.users.Count(ctx)
		if err != nil {
			return nil, fmt.Errorf("checking users: %w", err)
		}
		result.HasUsers = count > 0
	}

	if s.logs != nil {
		levels, err := s.logs.CountByLevel(ctx, store.LogCountParams{})
		if err != nil {
			return nil, fmt.Errorf("checking logs: %w", err)
		}
		for _, count := range levels {
			if count > 0 {
				result.HasLogs = true
				break
			}
		}
	}

	if s.dataSources != nil {
		sources, err := s.dataSources.List(ctx, store.ListDataSourceParams{})
		if err != nil {
			return nil, fmt.Errorf("checking data sources: %w", err)
		}
		result.HasDataSources = len(sources) > 0
	}

	result.IsComplete = result.HasUsers && result.HasLogs
	return result, nil
}

// DetectResult holds detected system information.
type DetectResult struct {
	UserCount       int `json:"user_count"`
	DataSourceCount int `json:"data_source_count"`
}

// Detect gathers system configuration details.
func (s *Service) Detect(ctx context.Context) (*DetectResult, error) {
	result := &DetectResult{}

	if s.users != nil {
		count, err := s.users.Count(ctx)
		if err != nil {
			return nil, fmt.Errorf("detecting users: %w", err)
		}
		result.UserCount = count
	}

	if s.dataSources != nil {
		sources, err := s.dataSources.List(ctx, store.ListDataSourceParams{})
		if err != nil {
			return nil, fmt.Errorf("detecting data sources: %w", err)
		}
		result.DataSourceCount = len(sources)
	}

	return result, nil
}

// Guide returns a list of next steps for the user to complete setup.
func (s *Service) Guide(ctx context.Context) ([]string, error) {
	status, err := s.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("setup guide: %w", err)
	}

	var steps []string
	if !status.HasUsers {
		steps = append(steps, "Create an admin user account")
	}
	if !status.HasLogs {
		steps = append(steps, "Send your first log entry via the API or Ruby gem")
	}
	if !status.HasDataSources {
		steps = append(steps, "Connect an external database for query introspection")
	}
	if len(steps) == 0 {
		steps = append(steps, "Setup is complete — start exploring your logs")
	}
	return steps, nil
}

// Verify checks that all required dependencies are configured.
func (s *Service) Verify() error {
	if s.users == nil && s.logs == nil && s.dataSources == nil {
		return fmt.Errorf("setup verify: %w", domain.ErrNotConfigured)
	}
	return nil
}
