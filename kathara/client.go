// This file is `manager/Kathara.py`, the facade — with its singleton removed
// (PORT_SPEC §0.2 #10) and its thirty delegating bodies otherwise untouched.
//
// "Untouched" is meant literally. Every one of those bodies in
// `manager/Kathara.py:63-605` is `self.manager.<name>(<the same arguments, in
// the same order>)`: no validation, no transformation, no added behaviour
// (analysis/manager-foundation.md §1.11). The argument checks that guard the
// lab-identifier triple live in the backends, eleven sites each, and moving
// them up here would be visible — `get_machine_stats` is a generator function,
// so its check does not run until the caller takes the first element, and a
// client-side check would raise it at call time instead. So this file
// delegates, and [LabRef.RequireSingle] is where a backend gets the check.

package kathara

import (
	"context"
	"errors"
	"fmt"

	"github.com/KatharaFramework/kathara-go/event"
	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/model"
	"github.com/KatharaFramework/kathara-go/settings"
)

// ErrNoSettings is [NewClient] called without a configuration.
//
// It carries no taxonomy code, so `kerrors.Code` buckets it to
// `InternalError`, and that is right: Python could not reach this state —
// `Kathara.__init__` read a `Setting` singleton that constructs itself on first
// access — so there is no Python message to reproduce and inventing an
// `InvocationError` string would add a row to the frozen ERROR_CODES.md
// catalogue for a failure only a Go embedder can cause.
var ErrNoSettings = errors.New("kathara: settings must not be nil")

// Config is what a backend needs from whoever embeds it, and it exists because
// PORT_SPEC §0.2 #10 took away the two singletons a Python manager constructor
// read: `Setting.get_instance()` and `EventDispatcher.get_instance()`.
//
// [NewClient] fills it and hands it to the [Factory].
type Config struct {
	// Settings is the loaded configuration. Never nil in a Config a
	// [NewClient] built.
	Settings *settings.Settings

	// Dispatcher is where the backend announces progress —
	// `machine_deployed`, `docker_pull_progress` and the rest of the
	// nineteen. Never nil in a Config [NewClient] built; a backend that
	// dispatches into it when nobody has subscribed is a no-op, which is what
	// Python's string-keyed dispatch did too.
	Dispatcher *event.Dispatcher

	// Defaults are the four settings values [model] falls back to when a
	// device says nothing (OQ-4). [NewClient] derives them from Settings with
	// [DefaultsFrom]; a backend that builds a [model.Lab] — which
	// `get_lab_from_api` does — must pass these to [model.NewLab] rather than
	// reaching for a global.
	Defaults model.Defaults
}

// DefaultsFrom is the OQ-4 wiring: the four `Setting.get_instance()` reads that
// `model/Machine.py` used to do (`:484` image, `:547` device_shell, `:589`
// volume_mount_policy, `:608` enable_ipv6), resolved once by the caller
// instead.
//
// `model` cannot import `settings` (PACKAGE_GRAPH.md §1.2 forbids the edge, so
// that a process can hold two scenarios with different defaults), and this
// package imports both, which makes it the one place the two schemas can be
// matched up by name.
func DefaultsFrom(s *settings.Settings) model.Defaults {
	if s == nil {
		return model.DefaultDefaults()
	}
	return model.Defaults{
		Image:             s.Image,
		DeviceShell:       s.DeviceShell,
		EnableIPv6:        s.EnableIPv6,
		VolumeMountPolicy: s.VolumeMountPolicy,
	}
}

// Client is the facade of `manager/Kathara.py`: one [Manager], selected by
// `manager_type`, with a method per interface method that forwards to it.
//
// It is a value, not a singleton. `Kathara.get_instance()` built the backend on
// first call, stored itself in a class attribute and raised
// `InstantiationError("This class is a singleton!")` if anyone constructed a
// second one, which froze the backend choice for the process; PORT_SPEC §0.2
// #10 removes all of that, and two Clients over two backends can now exist side
// by side.
//
// What survives is the timing. [NewClient] builds the backend immediately, so
// a Docker daemon that is not running, an unreadable kubeconfig or a missing
// network plugin is an error from the constructor — not from the first
// operation — exactly as `Kathara.__init__` made it
// (analysis/manager-foundation.md §7 gotcha 9).
//
// A Client adds no locking of its own. It is as safe for concurrent use as the
// [Manager] underneath it, which for both real backends means "yes": they run
// their own fan-outs.
type Client struct {
	manager Manager

	// registry is the table `manager_type` was resolved against, kept so
	// that [Client.AvailableManagers] answers from the same one that fed
	// construction — which is what Python's process-global
	// `AVAILABLE_MANAGERS` plus `ManagerFactory` amounted to. Nil for a
	// [NewClientWithManager] client, which resolved nothing; that one falls
	// back to [DefaultRegistry].
	registry *Registry
}

// clientOptions is the accumulated effect of the [ClientOption] values.
type clientOptions struct {
	registry   *Registry
	dispatcher *event.Dispatcher
	defaults   *model.Defaults
}

// ClientOption adjusts what [NewClient] builds.
type ClientOption func(*clientOptions)

// WithRegistry picks the backend table to resolve `manager_type` against.
// Defaults to [DefaultRegistry].
func WithRegistry(r *Registry) ClientOption {
	return func(o *clientOptions) { o.registry = r }
}

// WithDispatcher picks the event bus the backend announces into. Defaults to
// [event.Default], which is the closest thing to
// `EventDispatcher.get_instance()` that survives the redesign.
func WithDispatcher(d *event.Dispatcher) ClientOption {
	return func(o *clientOptions) { o.dispatcher = d }
}

// WithDefaults overrides the [model.Defaults] handed to the backend. Defaults
// to [DefaultsFrom] over the settings, which is what every non-test caller
// wants.
func WithDefaults(d model.Defaults) ClientOption {
	return func(o *clientOptions) { o.defaults = &d }
}

// NewClient is `Kathara.__init__`: read `manager_type`, resolve the backend,
// construct it.
//
// The resolution failure is the one place the error shape had to be chosen
// rather than copied. Python's is `ClassNotFoundError`, raised bare and with no
// message, which ERROR_CODES.md keeps as a RESERVED code with no Go
// representation because the only thing that ever rendered it was the unknown-
// *command* path. What a user actually sees for an unusable `manager_type` is
// `Setting._check_manager`'s `SettingsError("Manager Type not allowed.")`,
// raised from the startup check that runs before any command — so that is what
// this returns, as [ErrSettings] / `kerrors.ErrSettingsManagerType`, byte for
// byte. It is also the honest answer in the `nok8s` build, where
// `manager_type: kubernetes` passes `settings.Settings.CheckManager` (the
// frozen schema vocabulary still lists it) and then finds no backend: this
// binary does not allow it.
//
// Errors: [ErrNoSettings] for a nil configuration, [ErrSettings] when
// `manager_type` names no registered backend, and whatever the backend's
// [Factory] answers.
func NewClient(ctx context.Context, s *settings.Settings, opts ...ClientOption) (*Client, error) {
	if s == nil {
		return nil, ErrNoSettings
	}

	var o clientOptions
	for _, opt := range opts {
		opt(&o)
	}
	if o.registry == nil {
		o.registry = DefaultRegistry
	}
	if o.dispatcher == nil {
		o.dispatcher = event.Default()
	}
	if o.defaults == nil {
		d := DefaultsFrom(s)
		o.defaults = &d
	}

	backend, ok := o.registry.Lookup(s.ManagerType)
	if !ok {
		return nil, kerrors.ErrSettingsManagerType
	}

	manager, err := backend.New(ctx, Config{
		Settings:   s,
		Dispatcher: o.dispatcher,
		Defaults:   *o.defaults,
	})
	if err != nil {
		return nil, err
	}
	if manager == nil {
		return nil, fmt.Errorf("%w: %q returned no manager", ErrInvalidBackend, backend.Name)
	}

	return &Client{manager: manager, registry: o.registry}, nil
}

// NewClientWithManager wraps an already-constructed [Manager].
//
// Python has no such entry point — its facade could only build its manager by
// reflection — but Go embedders that construct a backend directly, and every
// test in this package, need one, and it is the same object graph [NewClient]
// produces. It returns nil for a nil manager, so that a mis-wired caller fails
// at its own call site rather than on the first delegation.
func NewClientWithManager(m Manager) *Client {
	if m == nil {
		return nil
	}
	return &Client{manager: m}
}

// Manager is the wrapped backend — `Kathara.manager`, the facade's one slot.
func (c *Client) Manager() Manager { return c.manager }

// AvailableManagers is `Kathara.get_available_managers_name()`: every backend
// this client could have been built over, in declared order.
//
// Python declares it static, so it does not consult the client's own backend
// and neither does this — but "static" there still meant the one process-global
// table that `__init__` had resolved against, so the two could not disagree.
// Here they can: [WithRegistry] is the way an embedder avoids package state, so
// this answers from the registry [NewClient] actually used, and falls back to
// [DefaultRegistry] only for a [NewClientWithManager] client, which resolved
// nothing. The package-level [AvailableManagers] is the static analogue.
func (c *Client) AvailableManagers() []ManagerInfo {
	if c.registry == nil {
		return AvailableManagers()
	}
	return c.registry.Available()
}

// DeployMachine is `Kathara.deploy_machine` (`manager/Kathara.py:63`).
func (c *Client) DeployMachine(ctx context.Context, machine *model.Machine) error {
	return c.manager.DeployMachine(ctx, machine)
}

// DeployLink is `Kathara.deploy_link` (`manager/Kathara.py:77`).
func (c *Client) DeployLink(ctx context.Context, link *model.Link) error {
	return c.manager.DeployLink(ctx, link)
}

// DeployLab is `Kathara.deploy_lab` (`manager/Kathara.py:91`).
func (c *Client) DeployLab(ctx context.Context, lab *model.Lab, opts DeployLabOptions) error {
	return c.manager.DeployLab(ctx, lab, opts)
}

// ConnectMachineToLink is `Kathara.connect_machine_to_link`
// (`manager/Kathara.py:109`).
func (c *Client) ConnectMachineToLink(ctx context.Context, machine *model.Machine, link *model.Link, macAddress string) error {
	return c.manager.ConnectMachineToLink(ctx, machine, link, macAddress)
}

// DisconnectMachineFromLink is `Kathara.disconnect_machine_from_link`
// (`manager/Kathara.py:127`).
func (c *Client) DisconnectMachineFromLink(ctx context.Context, machine *model.Machine, link *model.Link, keepLink bool) error {
	return c.manager.DisconnectMachineFromLink(ctx, machine, link, keepLink)
}

// UndeployMachine is `Kathara.undeploy_machine` (`manager/Kathara.py:142`).
func (c *Client) UndeployMachine(ctx context.Context, machine *model.Machine, keepLinks bool) error {
	return c.manager.UndeployMachine(ctx, machine, keepLinks)
}

// UndeployLink is `Kathara.undeploy_link` (`manager/Kathara.py:156`).
func (c *Client) UndeployLink(ctx context.Context, link *model.Link) error {
	return c.manager.UndeployLink(ctx, link)
}

// UndeployLab is `Kathara.undeploy_lab` (`manager/Kathara.py:181`).
func (c *Client) UndeployLab(ctx context.Context, ref LabRef, opts UndeployLabOptions) error {
	return c.manager.UndeployLab(ctx, ref, opts)
}

// Wipe is `Kathara.wipe` (`manager/Kathara.py:193`).
func (c *Client) Wipe(ctx context.Context, allUsers bool) error {
	return c.manager.Wipe(ctx, allUsers)
}

// ConnectTTY is `Kathara.connect_tty` (`manager/Kathara.py:221`).
func (c *Client) ConnectTTY(ctx context.Context, machineName string, ref LabRef, opts ConnectTTYOptions) (TTYSession, error) {
	return c.manager.ConnectTTY(ctx, machineName, ref, opts)
}

// ConnectTTYObj is `Kathara.connect_tty_obj` (`manager/Kathara.py:242`).
func (c *Client) ConnectTTYObj(ctx context.Context, machine *model.Machine, opts ConnectTTYOptions) (TTYSession, error) {
	return c.manager.ConnectTTYObj(ctx, machine, opts)
}

// Exec is `Kathara.exec` with `stream=False` (`manager/Kathara.py:274`).
func (c *Client) Exec(ctx context.Context, machineName string, command Command, ref LabRef, wait WaitPolicy) (stdout, stderr []byte, exitCode int, err error) {
	return c.manager.Exec(ctx, machineName, command, ref, wait)
}

// ExecObj is `Kathara.exec_obj` with `stream=False` (`manager/Kathara.py:299`).
func (c *Client) ExecObj(ctx context.Context, machine *model.Machine, command Command, wait WaitPolicy) (stdout, stderr []byte, exitCode int, err error) {
	return c.manager.ExecObj(ctx, machine, command, wait)
}

// ExecStream is `Kathara.exec` with `stream=True` (`manager/Kathara.py:274`).
func (c *Client) ExecStream(ctx context.Context, machineName string, command Command, ref LabRef, wait WaitPolicy) (ExecStream, error) {
	return c.manager.ExecStream(ctx, machineName, command, ref, wait)
}

// ExecStreamObj is `Kathara.exec_obj` with `stream=True`
// (`manager/Kathara.py:299`).
func (c *Client) ExecStreamObj(ctx context.Context, machine *model.Machine, command Command, wait WaitPolicy) (ExecStream, error) {
	return c.manager.ExecStreamObj(ctx, machine, command, wait)
}

// CopyFiles is `Kathara.copy_files` (`manager/Kathara.py:312`).
func (c *Client) CopyFiles(ctx context.Context, machine *model.Machine, files []CopyEntry) error {
	return c.manager.CopyFiles(ctx, machine, files)
}

// RetrieveFiles is `Kathara.retrieve_files` (`manager/Kathara.py:325`).
func (c *Client) RetrieveFiles(ctx context.Context, machine *model.Machine, src, dst string) error {
	return c.manager.RetrieveFiles(ctx, machine, src, dst)
}

// GetMachineAPIObject is `Kathara.get_machine_api_object`
// (`manager/Kathara.py:348`).
func (c *Client) GetMachineAPIObject(ctx context.Context, machineName string, ref LabRef, allUsers bool) (any, error) {
	return c.manager.GetMachineAPIObject(ctx, machineName, ref, allUsers)
}

// GetMachinesAPIObjects is `Kathara.get_machines_api_objects`
// (`manager/Kathara.py:369`).
func (c *Client) GetMachinesAPIObjects(ctx context.Context, ref LabRef, allUsers bool) ([]any, error) {
	return c.manager.GetMachinesAPIObjects(ctx, ref, allUsers)
}

// GetLinkAPIObject is `Kathara.get_link_api_object`
// (`manager/Kathara.py:392`).
func (c *Client) GetLinkAPIObject(ctx context.Context, linkName string, ref LabRef, allUsers bool) (any, error) {
	return c.manager.GetLinkAPIObject(ctx, linkName, ref, allUsers)
}

// GetLinksAPIObjects is `Kathara.get_links_api_objects`
// (`manager/Kathara.py:413`).
func (c *Client) GetLinksAPIObjects(ctx context.Context, ref LabRef, allUsers bool) ([]any, error) {
	return c.manager.GetLinksAPIObjects(ctx, ref, allUsers)
}

// GetLabFromAPI is `Kathara.get_lab_from_api` (`manager/Kathara.py:430`).
func (c *Client) GetLabFromAPI(ctx context.Context, labHash, labName string) (*model.Lab, error) {
	return c.manager.GetLabFromAPI(ctx, labHash, labName)
}

// UpdateLabFromAPI is `Kathara.update_lab_from_api`
// (`manager/Kathara.py:438`).
func (c *Client) UpdateLabFromAPI(ctx context.Context, lab *model.Lab) error {
	return c.manager.UpdateLabFromAPI(ctx, lab)
}

// GetMachinesStats is `Kathara.get_machines_stats`
// (`manager/Kathara.py:463`).
func (c *Client) GetMachinesStats(ctx context.Context, ref LabRef, machineName string, allUsers bool) (MachinesStatsStream, error) {
	return c.manager.GetMachinesStats(ctx, ref, machineName, allUsers)
}

// GetMachineStats is `Kathara.get_machine_stats` (`manager/Kathara.py:488`).
// It cannot fail here; see [Manager.GetMachineStats] for why.
func (c *Client) GetMachineStats(ctx context.Context, machineName string, ref LabRef, allUsers bool) MachineStatsStream {
	return c.manager.GetMachineStats(ctx, machineName, ref, allUsers)
}

// GetMachineStatsObj is `Kathara.get_machine_stats_obj`
// (`manager/Kathara.py:507`).
func (c *Client) GetMachineStatsObj(ctx context.Context, machine *model.Machine, allUsers bool) (MachineStatsStream, error) {
	return c.manager.GetMachineStatsObj(ctx, machine, allUsers)
}

// GetLinksStats is `Kathara.get_links_stats` (`manager/Kathara.py:531`).
func (c *Client) GetLinksStats(ctx context.Context, ref LabRef, linkName string, allUsers bool) (LinksStatsStream, error) {
	return c.manager.GetLinksStats(ctx, ref, linkName, allUsers)
}

// GetLinkStats is `Kathara.get_link_stats` (`manager/Kathara.py:556`). It
// cannot fail here; see [Manager.GetLinkStats].
func (c *Client) GetLinkStats(ctx context.Context, linkName string, ref LabRef, allUsers bool) LinkStatsStream {
	return c.manager.GetLinkStats(ctx, linkName, ref, allUsers)
}

// GetLinkStatsObj is `Kathara.get_link_stats_obj`
// (`manager/Kathara.py:573`).
func (c *Client) GetLinkStatsObj(ctx context.Context, link *model.Link, allUsers bool) (LinkStatsStream, error) {
	return c.manager.GetLinkStatsObj(ctx, link, allUsers)
}

// CheckImage is `Kathara.check_image` (`manager/Kathara.py:589`).
func (c *Client) CheckImage(ctx context.Context, imageName string) error {
	return c.manager.CheckImage(ctx, imageName)
}

// GetReleaseVersion is `Kathara.get_release_version`
// (`manager/Kathara.py:597`).
func (c *Client) GetReleaseVersion(ctx context.Context) (string, error) {
	return c.manager.GetReleaseVersion(ctx)
}

// GetFormattedManagerName is `Kathara.get_formatted_manager_name`
// (`manager/Kathara.py:605`) — an instance method on the facade even though
// `IManager` declares it static.
func (c *Client) GetFormattedManagerName() string {
	return c.manager.GetFormattedManagerName()
}

// Client implements the same contract it proxies, which is what
// `class Kathara(IManager)` said.
var _ Manager = (*Client)(nil)
