package api

import (
	"database/sql"

	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/pkg/store"
)

// serverOpt configures a test Server.
type serverOpt func(*Server)

// newTestServer builds a minimal Server for unit tests.
// Only the fields set by opts are populated; everything else is nil/zero.
func newTestServer(opts ...serverOpt) *Server {
	s := &Server{Router: chi.NewRouter()}
	for _, o := range opts {
		o(s)
	}
	return s
}

func withDB(db *sql.DB) serverOpt {
	return func(s *Server) { s.db = db }
}

func withCfg(cfg *config.Config) serverOpt {
	return func(s *Server) { s.cfg = cfg }
}

func withUserStore(us store.UserStore) serverOpt {
	return func(s *Server) { s.userStore = us }
}

func withSettingsStore(ss store.SettingsStore) serverOpt {
	return func(s *Server) { s.settingsStore = ss }
}

func withLogStore(ls store.LogStore) serverOpt {
	return func(s *Server) { s.logStore = ls }
}

func withDSStore(ds store.DataSourceStore) serverOpt {
	return func(s *Server) { s.dsStore = ds }
}

func withRegistry(r *connector.Registry) serverOpt {
	return func(s *Server) { s.registry = r }
}

func withAuditStore(as store.AuditStore) serverOpt {
	return func(s *Server) { s.auditStore = as }
}
