// This file lists all domain packages in the app.
// Add a domain: import the package, append its Module here.
// Remove a domain: delete the line and remove the import.
package main

import (
	authmod "github.com/adham90/opentrace/internal/domains/auth"
	"github.com/adham90/opentrace/internal/domains/connectors"
	"github.com/adham90/opentrace/internal/domains/dashboard"
	"github.com/adham90/opentrace/internal/domains/deploys"
	"github.com/adham90/opentrace/internal/domains/errorimpact"
	"github.com/adham90/opentrace/internal/domains/errors"
	"github.com/adham90/opentrace/internal/domains/events"
	"github.com/adham90/opentrace/internal/domains/healthchecks"
	"github.com/adham90/opentrace/internal/domains/investigations"
	"github.com/adham90/opentrace/internal/domains/journeys"
	"github.com/adham90/opentrace/internal/domains/logs"
	"github.com/adham90/opentrace/internal/domains/mcpactivity"
	"github.com/adham90/opentrace/internal/domains/onboarding"
	"github.com/adham90/opentrace/internal/domains/servers"
	"github.com/adham90/opentrace/internal/domains/settings"
	"github.com/adham90/opentrace/internal/domains/tools"
	"github.com/adham90/opentrace/internal/domains/traces"
	"github.com/adham90/opentrace/internal/domains/trends"
	"github.com/adham90/opentrace/internal/domains/watches"
	"github.com/adham90/opentrace/pkg/server"
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
