//go:build nok8s

// A `nok8s` binary still accepts `manager_type: kubernetes` in `kathara.conf`,
// because the frozen config schema's vocabulary is unchanged
// (`settings.AvailableManagers`); what it answers is `kathara.NewClient`'s
// `SettingsError("Manager Type not allowed.")`, which is the honest report that
// this build cannot run it.

package main

import (
	"github.com/KatharaFramework/kathara-go/backend/docker"
	"github.com/KatharaFramework/kathara-go/kathara"
)

// backendRegistry builds the single-entry table. See the `!nok8s` twin for why
// a registration failure panics.
func backendRegistry() *kathara.Registry {
	r := kathara.NewRegistry()
	if err := r.Register(docker.Backend()); err != nil {
		panic(err)
	}
	return r
}
