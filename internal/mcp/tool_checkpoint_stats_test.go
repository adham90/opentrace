package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/adham90/opentrace/internal/connector"
)

func TestCheckpointStatsHandler_Success(t *testing.T) {
	registry := connector.NewRegistry()

	callCount := 0
	registry.Register(&multiQueryMockQE{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		handler: func(query string) (*connector.QueryResult, error) {
			callCount++
			switch callCount {
			case 1:
				// pg_stat_bgwriter
				return &connector.QueryResult{
					Columns: []string{"checkpoints_timed", "checkpoints_req", "checkpoint_write_time", "checkpoint_sync_time",
						"buffers_checkpoint", "buffers_clean", "buffers_backend", "maxwritten_clean", "buffers_alloc", "stats_reset"},
					Rows:     [][]any{{int64(100), int64(5), 50000.0, 2000.0, int64(80000), int64(500), int64(200), int64(10), int64(90000), "2024-01-01T00:00:00Z"}},
					RowCount: 1,
				}, nil
			case 2:
				// pg_stat_wal
				return &connector.QueryResult{
					Columns:  []string{"wal_records", "wal_fpi", "wal_bytes", "wal_size", "stats_reset"},
					Rows:     [][]any{{int64(1000000), int64(5000), int64(1073741824), "1 GB", "2024-01-01T00:00:00Z"}},
					RowCount: 1,
				}, nil
			default:
				// WAL position
				return &connector.QueryResult{
					Columns:  []string{"lsn", "file"},
					Rows:     [][]any{{"0/1A000060", "00000001000000000000001A"}},
					RowCount: 1,
				}, nil
			}
		},
	})

	handler := checkpointStatsHandler(registry)
	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	if !contains(text, "checkpoints") {
		t.Errorf("expected checkpoints in output, got: %s", text)
	}
	if !contains(text, "buffers") {
		t.Errorf("expected buffers in output, got: %s", text)
	}
}

func TestCheckpointStatsHandler_HighRequestedCheckpoints(t *testing.T) {
	registry := connector.NewRegistry()

	callCount := 0
	registry.Register(&multiQueryMockQE{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		handler: func(query string) (*connector.QueryResult, error) {
			callCount++
			if callCount == 1 {
				// 80% requested checkpoints
				return &connector.QueryResult{
					Columns: []string{"checkpoints_timed", "checkpoints_req", "checkpoint_write_time", "checkpoint_sync_time",
						"buffers_checkpoint", "buffers_clean", "buffers_backend", "maxwritten_clean", "buffers_alloc", "stats_reset"},
					Rows:     [][]any{{int64(20), int64(80), 50000.0, 2000.0, int64(80000), int64(500), int64(200), int64(10), int64(90000), "2024-01-01T00:00:00Z"}},
					RowCount: 1,
				}, nil
			}
			return &connector.QueryResult{Columns: []string{"x"}, Rows: [][]any{}, RowCount: 0}, nil
		},
	})

	handler := checkpointStatsHandler(registry)
	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if !contains(text, "max_wal_size") {
		t.Errorf("expected max_wal_size warning for high requested checkpoints, got: %s", text)
	}
}

func TestCheckpointStatsHandler_NoConnector(t *testing.T) {
	registry := connector.NewRegistry()
	handler := checkpointStatsHandler(registry)

	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when no connector")
	}
}

func TestCheckpointStatsHandler_QueryError(t *testing.T) {
	registry := connector.NewRegistry()
	registry.Register(&mockQueryExecutor{
		mockDataSource: mockDataSource{connType: connector.ConnectorDatabase},
		err:            errors.New("connection lost"),
	})

	handler := checkpointStatsHandler(registry)
	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result")
	}
}
