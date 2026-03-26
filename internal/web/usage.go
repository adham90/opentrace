package web

import (
	"net/http"

	"github.com/adham90/opentrace/pkg/store"
)

func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	usage := map[string]any{}

	// Log stats
	if s.logStore != nil {
		// Count total logs
		if row := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM logs"); row != nil {
			var count int64
			if row.Scan(&count) == nil {
				usage["logs_total"] = count
			}
		}
		// Count logs in last 24h
		if row := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM logs WHERE timestamp > datetime('now', '-1 day')"); row != nil {
			var count int64
			if row.Scan(&count) == nil {
				usage["logs_last_24h"] = count
			}
		}
		// Oldest and newest log
		if row := s.db.QueryRowContext(ctx, "SELECT MIN(timestamp), MAX(timestamp) FROM logs"); row != nil {
			var oldest, newest *string
			if row.Scan(&oldest, &newest) == nil {
				if oldest != nil {
					usage["oldest_log"] = *oldest
				}
				if newest != nil {
					usage["newest_log"] = *newest
				}
			}
		}
	}

	// DB size
	if row := s.db.QueryRowContext(ctx, "SELECT page_count * page_size FROM pragma_page_count, pragma_page_size"); row != nil {
		var size int64
		if row.Scan(&size) == nil {
			usage["db_size_bytes"] = size
		}
	}

	// User count
	if s.userStore != nil {
		if count, err := s.userStore.Count(ctx); err == nil {
			usage["users"] = count
		}
	}

	// Connector count
	if s.dsStore != nil {
		if list, err := s.dsStore.List(ctx, store.ListDataSourceParams{}); err == nil {
			usage["connectors"] = len(list)
		}
	}

	// Active watches
	if row := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM watches WHERE status = 'active'"); row != nil {
		var count int64
		if row.Scan(&count) == nil {
			usage["watchers_active"] = count
		}
	}

	// Health checks
	if row := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM healthchecks"); row != nil {
		var count int64
		if row.Scan(&count) == nil {
			usage["healthchecks"] = count
		}
	}

	writeJSON(w, http.StatusOK, usage)
}
