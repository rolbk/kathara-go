//go:build !nok8s

// This file is the whole of PORT_SPEC §0.2 #7's "explicit registry" at the
// consumer end: the two backends, named in the order `AVAILABLE_MANAGERS`
// declares them (SYNTHESIS.md C-5), with no `init()` anywhere in the chain.
//
// The order is a contract, not a detail: it is what
// `Kathara.get_available_managers_name()` returned, and the settings screen
// shows the user that list.

package main

import (
	"github.com/KatharaFramework/kathara-go/backend/docker"
	"github.com/KatharaFramework/kathara-go/backend/kubernetes"
	"github.com/KatharaFramework/kathara-go/kathara"
)

// backendRegistry builds the table `manager_type` is resolved against.
//
// A registration failure is a build's mistake and not a user's — a duplicate
// name or a nil constructor — and there is nothing a user could do about it, so
// it panics here rather than being threaded through every command's error
// return. PORT_SPEC §10 forbids a panic on a *Python-reachable* path; this one
// is reachable only by editing this file.
func backendRegistry() *kathara.Registry {
	r := kathara.NewRegistry()
	for _, b := range []kathara.Backend{docker.Backend(), kubernetes.Backend()} {
		if err := r.Register(b); err != nil {
			panic(err)
		}
	}
	return r
}
