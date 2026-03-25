// This file lists all domain modules in the app.
// Add a domain: import the package, append its Module here.
// Remove a domain: delete the line and remove the import.
package main

import (
	authmod "github.com/adham90/opentrace/internal/modules/auth"
	"github.com/adham90/opentrace/internal/modules/connectors"
	"github.com/adham90/opentrace/internal/modules/dashboard"
	"github.com/adham90/opentrace/internal/modules/deploys"
	"github.com/adham90/opentrace/internal/modules/errorimpact"
	"github.com/adham90/opentrace/internal/modules/errors"
	"github.com/adham90/opentrace/internal/modules/events"
	"github.com/adham90/opentrace/internal/modules/healthchecks"
	"github.com/adham90/opentrace/internal/modules/investigations"
	"github.com/adham90/opentrace/internal/modules/journeys"
	"github.com/adham90/opentrace/internal/modules/logs"
	"github.com/adham90/opentrace/internal/modules/mcpactivity"
	"github.com/adham90/opentrace/internal/modules/onboarding"
	"github.com/adham90/opentrace/internal/modules/servers"
	"github.com/adham90/opentrace/internal/modules/settings"
	"github.com/adham90/opentrace/internal/modules/tools"
	"github.com/adham90/opentrace/internal/modules/traces"
	"github.com/adham90/opentrace/internal/modules/trends"
	"github.com/adham90/opentrace/internal/modules/watches"
	"github.com/adham90/opentrace/internal/server"
)

var modules = []server.Module{
	authmod.Module,
	onboarding.Module,
	dashboard.Module,
	errors.Module,
	traces.Module,
	deploys.Module,
	mcpactivity.Module,
	errorimpact.Module,
	healthchecks.Module,
	investigations.Module,
	journeys.Module,
	trends.Module,
	servers.Module,
	events.Module,
	watches.Module,
	settings.Module,
	logs.Module,
	connectors.Module,
	tools.Module,
}
