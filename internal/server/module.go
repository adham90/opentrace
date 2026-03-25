package server

import "github.com/go-chi/chi/v5"

// Module describes a domain package's contributions to the app.
// Each domain exports a Module var. The router iterates these
// for route mounting during startup.
type Module struct {
	// Name is a human-readable identifier (e.g., "errors", "watches").
	Name string

	// Mount registers the domain's routes on the given router.
	Mount func(r chi.Router, deps *Deps)
}
