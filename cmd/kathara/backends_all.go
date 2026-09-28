//go:build !nok8s

// Registration order is preserved in the settings screen.

package main

import (
	"github.com/rolbk/kathara-go/backend/docker"
	"github.com/rolbk/kathara-go/backend/kubernetes"
	"github.com/rolbk/kathara-go/kathara"
)

// backendRegistry builds the table `manager_type` is resolved against.
func backendRegistry() *kathara.Registry {
	r := kathara.NewRegistry()
	for _, b := range []kathara.Backend{docker.Backend(), kubernetes.Backend()} {
		if err := r.Register(b); err != nil {
			panic(err)
		}
	}
	return r
}
