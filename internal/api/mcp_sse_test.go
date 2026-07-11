package api

import (
	"context"
	"net/http/httptest"
	"testing"

	srvpkg "github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestMCPServerForRole_FailsClosed guards the privilege-escalation regression:
// the HTTP/SSE transports used to hand every authenticated user the admin
// server. The selector must return the admin server only for admins and fall
// back to the read-only member server for members and anonymous requests.
func TestMCPServerForRole_FailsClosed(t *testing.T) {
	admin := mcp.NewServer(&mcp.Implementation{Name: "admin", Version: "1"}, nil)
	member := mcp.NewServer(&mcp.Implementation{Name: "member", Version: "1"}, nil)
	sel := mcpServerForRole(admin, member)

	adminReq := httptest.NewRequest("POST", "/mcp", nil).WithContext(
		srvpkg.WithUser(context.Background(), &store.User{Role: store.RoleAdmin}))
	if got := sel(adminReq); got != admin {
		t.Error("admin user should select the admin server")
	}

	memberReq := httptest.NewRequest("POST", "/mcp", nil).WithContext(
		srvpkg.WithUser(context.Background(), &store.User{Role: store.RoleMember}))
	if got := sel(memberReq); got != member {
		t.Error("member user should select the member server")
	}

	anonReq := httptest.NewRequest("POST", "/mcp", nil)
	if got := sel(anonReq); got != member {
		t.Error("request with no authenticated user must fail closed to the member server")
	}
}
