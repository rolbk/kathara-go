// This file is `cli/ui/event/register.py`: the eighteen subscriptions the CLI
// installs before dispatch and tears down on every exit path.
// Two properties of the Python are load-bearing and reproduced exactly.

package main

import (
	"context"

	"github.com/KatharaFramework/kathara-go/event"
	"github.com/KatharaFramework/kathara-go/internal/cliout"
)

// cliHandlers owns the subscriber objects so that they survive between the
// subscribe and the unsubscribe, exactly as `register.py`'s local variables do
// through the closure the dispatcher stores.
type cliHandlers struct {
	linkDeploy      *cliout.ProgressBar
	linkUndeploy    *cliout.ProgressBar
	machineDeploy   *cliout.ProgressBar
	machineUndeploy *cliout.ProgressBar
	imagePull       *cliout.ImagePullBar
}

// registerEvents is `register_cli_events()`.
func (a *app) registerEvents() {
	d := a.dispatcher
	h := &cliHandlers{
		linkDeploy:      cliout.NewProgressBar("Deploying collision domains", a.console),
		linkUndeploy:    cliout.NewProgressBar("Deleting collision domains", a.console),
		machineDeploy:   cliout.NewProgressBar("Deploying devices", a.console),
		machineUndeploy: cliout.NewProgressBar("Deleting devices", a.console),
		imagePull:       cliout.NewImagePullBar(a.console),
	}
	a.handlers = h

	// _register_link_events
	event.SubscribeWithHook(d, func(e event.LinksDeployStarted) error {
		h.linkDeploy.Init(e.Count())
		return nil
	}, h.linkDeploy.Finish)
	event.SubscribeWithHook(d, func(event.LinkDeployed) error {
		h.linkDeploy.Advance()
		return nil
	}, h.linkDeploy.Finish)
	event.SubscribeWithHook(d, func(event.LinksDeployEnded) error {
		return h.linkDeploy.Finish()
	}, h.linkDeploy.Finish)

	event.SubscribeWithHook(d, func(e event.LinksUndeployStarted) error {
		h.linkUndeploy.Init(e.Count())
		return nil
	}, h.linkUndeploy.Finish)
	event.SubscribeWithHook(d, func(event.LinkUndeployed) error {
		h.linkUndeploy.Advance()
		return nil
	}, h.linkUndeploy.Finish)
	event.SubscribeWithHook(d, func(event.LinksUndeployEnded) error {
		return h.linkUndeploy.Finish()
	}, h.linkUndeploy.Finish)

	// _register_machine_events
	event.SubscribeWithHook(d, func(e event.MachinesDeployStarted) error {
		h.machineDeploy.Init(e.Count())
		return nil
	}, h.machineDeploy.Finish)
	event.SubscribeWithHook(d, func(event.MachineDeployed) error {
		h.machineDeploy.Advance()
		return nil
	}, h.machineDeploy.Finish)
	event.SubscribeWithHook(d, func(event.MachinesDeployEnded) error {
		return h.machineDeploy.Finish()
	}, h.machineDeploy.Finish)

	event.SubscribeWithHook(d, func(e event.MachinesUndeployStarted) error {
		h.machineUndeploy.Init(e.Count())
		return nil
	}, h.machineUndeploy.Finish)
	event.SubscribeWithHook(d, func(event.MachineUndeployed) error {
		h.machineUndeploy.Advance()
		return nil
	}, h.machineUndeploy.Finish)
	event.SubscribeWithHook(d, func(event.MachinesUndeployEnded) error {
		return h.machineUndeploy.Finish()
	}, h.machineUndeploy.Finish)

	// The terminal handler is the SECOND subscriber of `machine_deployed`.
	event.Subscribe(d, a.openMachineTerminals)
	event.Subscribe(d, func(event.MachineStartupWaitStarted) error {
		a.console.PrintWaitMessage()
		return nil
	})
	event.Subscribe(d, func(event.MachineStartupWaitEnded) error {
		a.console.ClearScreen()
		return nil
	})

	event.Subscribe(d, func(e event.MachinesWithVolumes) error {
		policy := &cliout.VolumeMountPolicy{
			Policy:   a.settings.VolumeMountPolicy,
			Console:  a.console,
			Prompter: a.prompter,
		}
		return policy.Run(e)
	})

	// _register_docker_image_pull_events
	event.SubscribeWithHook(d, func(event.DockerPullStarted) error {
		h.imagePull.Init()
		return nil
	}, h.imagePull.Finish)
	event.SubscribeWithHook(d, func(e event.DockerPullProgress) error {
		return h.imagePull.Update(e.Progress)
	}, h.imagePull.Finish)
	event.SubscribeWithHook(d, func(event.DockerPullEnded) error {
		return h.imagePull.Finish()
	}, h.imagePull.Finish)

	event.Subscribe(d, func(e event.DockerImageUpdateFound) error {
		policy := &cliout.ImageUpdatePolicy{
			Policy:   a.settings.ImageUpdatePolicy,
			Console:  a.console,
			Prompter: a.prompter,
		}
		return policy.Run(e)
	})
}

// unregisterEvents is `unregister_cli_events()`, which the entrypoint calls
// before every `sys.exit` — including the Ctrl-C one — because that is what
// stops the progress bars (`src/kathara.py:61,66,75,83,87,93,100,107`).
func (a *app) unregisterEvents() {
	d := a.dispatcher
	unsubscribeAll(d)
}

// unsubscribeAll drops every subscription, in `register.py`'s own order. The
// errors it returns are the teardown hooks' — `ProgressBar.Finish` never fails,
// so they are all nil, and the signature keeps them visible rather than
// swallowing a future one.
func unsubscribeAll(d *event.Dispatcher) {
	_ = event.Unsubscribe[event.LinksDeployStarted](d)
	_ = event.Unsubscribe[event.LinkDeployed](d)
	_ = event.Unsubscribe[event.LinksDeployEnded](d)

	_ = event.Unsubscribe[event.LinksUndeployStarted](d)
	_ = event.Unsubscribe[event.LinkUndeployed](d)
	_ = event.Unsubscribe[event.LinksUndeployEnded](d)

	_ = event.Unsubscribe[event.MachinesDeployStarted](d)
	_ = event.Unsubscribe[event.MachineDeployed](d)
	_ = event.Unsubscribe[event.MachinesDeployEnded](d)

	_ = event.Unsubscribe[event.MachinesUndeployStarted](d)
	_ = event.Unsubscribe[event.MachineUndeployed](d)
	_ = event.Unsubscribe[event.MachinesUndeployEnded](d)

	// `machine_deployed` a second time: Python does this and relies on the
	// no-op (`register.py:44`).
	_ = event.Unsubscribe[event.MachineDeployed](d)
	_ = event.Unsubscribe[event.MachineStartupWaitStarted](d)
	_ = event.Unsubscribe[event.MachineStartupWaitEnded](d)

	_ = event.Unsubscribe[event.MachinesWithVolumes](d)

	_ = event.Unsubscribe[event.DockerPullStarted](d)
	_ = event.Unsubscribe[event.DockerPullProgress](d)
	_ = event.Unsubscribe[event.DockerPullEnded](d)

	_ = event.Unsubscribe[event.DockerImageUpdateFound](d)
}

// openMachineTerminals is `HandleMachineTerminal.run`: when `open_terminals` is
// set, open `machine.get_num_terms()` windows onto the device.
func (a *app) openMachineTerminals(e event.MachineDeployed) error {
	if !a.settings.OpenTerminals || a.console.Format.Machine() {
		return nil
	}
	if e.Machine == nil {
		return nil
	}
	n, err := e.Machine.GetNumTerms()
	if err != nil {
		return err
	}
	ctx := a.opCtx
	if ctx == nil {
		ctx = context.Background()
	}
	for i := 0; i < n; i++ {
		if err := a.terminalOpener(ctx, e.Machine); err != nil {
			return err
		}
	}
	return nil
}
