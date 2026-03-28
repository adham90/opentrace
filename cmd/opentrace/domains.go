// This file lists all domain packages in the app.
// Add a domain: import the package, append its Module here.
// Remove a domain: delete the line and remove the import.
package main

import (
	authmod "github.com/adham90/opentrace/internal/domains/auth"
	"github.com/adham90/opentrace/internal/domains/deploys"
	"github.com/adham90/opentrace/internal/domains/events"
	"github.com/adham90/opentrace/internal/domains/servers"
	"github.com/adham90/opentrace/pkg/server"
)

var modules = []server.Module{
	authmod.Module,
	deploys.Module,
	servers.Module,
	events.Module,
}
