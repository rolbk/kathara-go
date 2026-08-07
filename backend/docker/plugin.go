// This file is `DockerPlugin.py`: making sure the Kathará network plugin is
// installed, enabled and — for the iptables flavour — pointed at the right
// `xtables.lock`.
//
// It runs from the constructor, on every launch, before anything else touches
// the daemon (OQ-16, `DockerManager.py:75-76`).

package docker

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path"
	"runtime"
	"strings"

	"github.com/docker/docker/api/types"

	"github.com/KatharaFramework/kathara-go/kerrors"
)

// The two plugin flavours (`DockerPlugin.py:14-15`). `network_plugin` in the
// settings holds one of these two names, and everything about the xtables
// handling below keys off which.
const (
	// linuxPluginName is the iptables/Linux-bridge flavour, which needs the
	// host's `xtables.lock` bind-mounted into the plugin.
	linuxPluginName = "kathara/katharanp"
	// vdePluginName is the VDE flavour, the default, which needs no such
	// thing.
	vdePluginName = "kathara/katharanp_vde"
)

// The plugin-settings vocabulary (`DockerPlugin.py:17-20`).
const (
	// hosttmpKey is the mount whose destination is the plugin's writable tmp,
	// under which the VDE switch sockets live.
	hosttmpKey = "tmp"
	// xtablesConfigurationKey is the settable mount `plugin.configure` writes,
	// as `xtables_lock.source`.
	xtablesConfigurationKey = "xtables_lock"
	// xtablesLockPath is the host path that mount points at when it is needed
	// at all.
	xtablesLockPath = "/run/xtables.lock"
)

// pluginStatePath is `DockerPlugin.PLUGIN_STATE_PATH` (`DockerPlugin.py:26`),
// the runc state file the VDE flavour reads the plugin's PID out of. The `{id}`
// is the plugin id.
const pluginStatePath = "/run/docker/runtime-runc/plugins.moby/{id}/state.json"

// pluginService is `DockerPlugin` (`DockerPlugin.py:22`).
type pluginService struct {
	manager *Manager
	// currentName is `self.current_name`: `f"{network_plugin}:{architecture}"`
	// (`DockerPlugin.py:30`), computed once in the constructor. A `null`
	// `network_plugin` renders as the literal "None" there, which is why the
	// settings field stays a pointer and the caller does the rendering
	// ([settings.Settings.NetworkPlugin]).
	currentName string
}

// isVDE is `DockerPlugin.is_vde` (`DockerPlugin.py:81`). Note what it tests:
// the CONFIGURED plugin name, not the installed one, so the two `is_*`
// predicates are settings reads and not daemon reads.
func (p *pluginService) isVDE() bool {
	return p.manager.settings.NetworkPlugin != nil && *p.manager.settings.NetworkPlugin == vdePluginName
}

// isBridge is `DockerPlugin.is_bridge` (`DockerPlugin.py:90`).
func (p *pluginService) isBridge() bool {
	return p.manager.settings.NetworkPlugin != nil && *p.manager.settings.NetworkPlugin == linuxPluginName
}

// CheckAndDownload is `check_and_download_plugin` (`DockerPlugin.py:32`), the
// whole plugin lifecycle in one method.
//
// The shape, with the remote/local fork spelled out because it is not
// symmetrical:
//
//	get the plugin
//	  found     → "upgrade" it (see below)
//	  not found → local: install it; remote: DockerPluginError "not found"
//	local  → VDE:    enable if disabled
//	         bridge: resolve the xtables mount, then
//	                   disabled → configure, enable
//	                   enabled  → compare the configured source with the
//	                              computed one; if they differ, DISABLE,
//	                              reconfigure, re-enable
//	remote → error unless already enabled
//
// # The upgrade that is not an upgrade (OQ-16)
//
// `plugin.upgrade()` at `:47` looks like an every-launch network round trip,
// and it is not one. docker-py's `Plugin.upgrade` is a GENERATOR FUNCTION — its
// body holds `yield from` — so calling it and discarding the result constructs
// a generator and runs nothing at all: no privileges query, no pull, no
// `reload`, and not even the `DockerError('Plugin must be disabled before
// upgrading.')` its first line would raise for the enabled plugin that is the
// normal case. Oracle-verified against docker-py 7.2.0:
// `inspect.isgeneratorfunction(Plugin.upgrade)` is True.
//
// So the faithful port of that line is nothing, and doing a real
// `PluginUpgrade` here would be a behaviour change — it would pull on every
// launch and fail on every enabled plugin. The line is preserved as this
// comment, which is where its whole observable effect lives.
func (p *pluginService) CheckAndDownload(ctx context.Context) error {
	slog.Debug("Checking plugin...", "plugin", p.currentName)

	plugin, _, err := p.manager.api.PluginInspectWithRaw(ctx, p.currentName)
	switch {
	case err == nil:
		// `plugin.upgrade()` — a no-op, see above.
	case isNotFound(err):
		if p.manager.settings.RemoteURL != nil {
			return kerrors.ErrPluginNotFound
		}
		slog.Info("Installing Kathara Network Plugin...", "plugin", p.currentName)
		if plugin, err = p.install(ctx); err != nil {
			return err
		}
		slog.Info("Kathara Network Plugin installed successfully!")
	default:
		return err
	}

	if p.manager.settings.RemoteURL != nil {
		if !plugin.Enabled {
			return kerrors.ErrPluginNotEnabled
		}
		return nil
	}

	switch {
	case p.isVDE() && !plugin.Enabled:
		slog.Debug("Enabling plugin...", "plugin", p.currentName)
		return p.manager.api.PluginEnable(ctx, p.currentName, types.PluginEnableOptions{Timeout: 0})

	case p.isBridge():
		mount, err := p.xtablesLockMount(ctx)
		if err != nil {
			return err
		}

		if !plugin.Enabled {
			if err := p.configureXtablesMount(ctx, mount); err != nil {
				return err
			}
			slog.Debug("Enabling plugin...", "plugin", p.currentName)
			return p.manager.api.PluginEnable(ctx, p.currentName, types.PluginEnableOptions{Timeout: 0})
		}

		// `list(filter(...)).pop()` over `Settings.Mounts`: the LAST mount
		// named `xtables_lock`, and an IndexError when there is none — a
		// plugin without that mount is not this plugin (ORDERING.tsv row 120).
		source, ok := lastMountSource(plugin.Settings.Mounts, xtablesConfigurationKey)
		if !ok {
			// `.pop()` on the empty filter result — Python's IndexError, with
			// its own message. Unlike `plugin_store_path`'s miss, this site has
			// no FileNotFoundError to raise: a plugin without an `xtables_lock`
			// mount is not the Linux-bridge plugin at all.
			return newPyIndexError("pop from empty list")
		}
		// `if mount_obj["Source"] != xtables_lock_mount` — a JSON null reads
		// as `None` in Python, and `None != ""` is TRUE, so an unset source on
		// an nf_tables host (computed mount "") is a MISMATCH and the plugin
		// is reconfigured. Folding nil into "" would have compared them equal
		// and skipped the reconfigure Python performs.
		if source != nil && *source == mount {
			return nil
		}

		if err := p.manager.api.PluginDisable(ctx, p.currentName, types.PluginDisableOptions{}); err != nil {
			return err
		}
		if err := p.configureXtablesMount(ctx, mount); err != nil {
			return err
		}
		slog.Debug("Enabling plugin...", "plugin", p.currentName)
		return p.manager.api.PluginEnable(ctx, p.currentName, types.PluginEnableOptions{Timeout: 0})
	}

	// Neither flavour: `network_plugin` names something else, both `is_*`
	// predicates are false, and Python's if/elif chain falls through doing
	// nothing. So does this.
	return nil
}

// install is `client.plugins.install(name)` (`DockerPlugin.py:51`).
//
// docker-py's collection method queries the plugin's privileges, pulls with
// them, DRAINS the progress stream, and then re-fetches the plugin
// (`models/plugins.py:188-192`). It does NOT enable: the plugin comes back
// disabled and the caller decides.
//
// The Go SDK's `PluginInstall` does the same work behind one call, but its
// goroutine ends the progress stream with `PluginEnable` unless
// `options.Disabled` is set (`docker@v28.5.2 client/plugin_install.go:210`).
// `Disabled: true` is therefore not an option here, it is the port: without it
// the plugin is enabled with its MANIFEST default `xtables_lock` source, before
// `_configure_xtables_mount` has had a chance to blank it — which on a pure
// nf_tables host (computed mount "") can fail the enable outright and make
// `New()` error where Python's first run succeeds, and when it does succeed
// turns Python's install → configure → enable into install → enable → disable
// → configure → enable.
//
// Draining the stream is still not optional: the SDK's goroutine writes into a
// pipe and only the `PluginSet`/close steps at its end release it.
func (p *pluginService) install(ctx context.Context) (*types.Plugin, error) {
	body, err := p.manager.api.PluginInstall(ctx, p.currentName, types.PluginInstallOptions{
		RemoteRef:            p.currentName,
		AcceptAllPermissions: true,
		Disabled:             true,
	})
	if err != nil {
		return nil, err
	}
	_, err = io.Copy(io.Discard, body)
	closeErr := body.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}

	// docker-py's `install` ends with `return self.get(...)`, so the caller
	// gets a freshly inspected plugin and reads `.enabled` off it.
	plugin, _, err := p.manager.api.PluginInspectWithRaw(ctx, p.currentName)
	return plugin, err
}

// lastMountSource is `list(filter(lambda x: x["Name"] == key, mounts)).pop()`
// followed by `mount_obj["Source"]` (`DockerPlugin.py:68-72`).
//
// `.pop()` takes the LAST match and raises IndexError on none. The key is
// unique in practice so first-versus-last does not matter (ORDERING.tsv row
// 120 says as much), but the last is what Python takes and the emptiness is
// reported rather than panicked.
//
// `Source` is a `*string` in the SDK because the plugin manifest allows null,
// and the pointer is handed BACK rather than flattened: Python's comparison is
// against `None`, which differs from every string INCLUDING the empty one that
// [pluginService.xtablesLockMount] computes on an nf_tables host. The second
// result is the `.pop()`'s emptiness, not the source's nullness.
func lastMountSource(mounts []types.PluginMount, key string) (*string, bool) {
	var source *string
	found := false
	for _, mount := range mounts {
		if mount.Name != key {
			continue
		}
		found = true
		source = mount.Source
	}
	return source, found
}

// configureXtablesMount is `_configure_xtables_mount`
// (`DockerPlugin.py:172`): `plugin.configure({XTABLES_CONFIGURATION_KEY +
// '.source': xtables_lock_mount})`.
//
// docker-py turns that dict into the list `["xtables_lock.source=<value>"]`
// before posting it (`APIClient.configure_plugin`), which is exactly the
// `[]string` the Go SDK's `PluginSet` takes.
func (p *pluginService) configureXtablesMount(ctx context.Context, mount string) error {
	slog.Debug("Configuring xtables.lock source...", "source", mount)
	return p.manager.api.PluginSet(ctx, p.currentName, []string{xtablesConfigurationKey + ".source=" + mount})
}

// xtablesLockMount is `_xtables_lock_mount` (`DockerPlugin.py:155`), the one
// three-way platform branch in this file:
//
//	Linux   → mount the lock unless iptables is the nf_tables backend
//	Windows → mount it only under a WSL2 (`microsoft`) kernel
//	macOS   → never
//
// "Mount it" is the path; "do not" is the empty string, which `plugin.configure`
// writes as an empty source and the plugin reads as "no bind mount".
//
// The Linux arm is why `os/Networking.get_iptables_version` had to be carved
// out of the §0.3 deferral (SYNTHESIS C-3, PACKAGE_GRAPH.md D-6): it is on the
// 1.0 path.
func (p *pluginService) xtablesLockMount(ctx context.Context) (string, error) {
	switch runtime.GOOS {
	case "linux":
		version, err := iptablesVersion()
		if err != nil {
			return "", err
		}
		if strings.Contains(version, "nf_tables") {
			return "", nil
		}
		return xtablesLockPath, nil

	case "windows":
		info, err := p.manager.api.Info(ctx)
		if err != nil {
			return "", err
		}
		if !strings.Contains(info.KernelVersion, "microsoft") {
			return "", nil
		}
		return xtablesLockPath, nil

	default:
		return "", nil
	}
}

// StorePath is `plugin_store_path` (`DockerPlugin.py:119`): the directory the
// VDE plugin keeps its switch sockets in, `<tmp mount destination>/katharanp`.
//
// Its only caller is the external-interface attach, which is DEFERRED
// (PORT_SPEC §0.3), so nothing in 1.0 reaches it. It is ported because the
// error it raises for a plugin without a `tmp` mount is a live row of
// ERROR_CODES.md (`Unable to find \`tmp\` in plugin mounts.`) and because the
// deferred feature will want it unchanged.
//
// Python scans for the FIRST mount named `tmp` and breaks, which is the
// opposite of the `.pop()` at the xtables site; both are reproduced as written.
func (p *pluginService) StorePath(ctx context.Context) (string, error) {
	plugin, _, err := p.manager.api.PluginInspectWithRaw(ctx, p.currentName)
	if err != nil {
		return "", err
	}

	for _, mount := range plugin.Settings.Mounts {
		if mount.Name == hosttmpKey {
			return path.Join(mount.Destination, "katharanp"), nil
		}
	}
	return "", kerrors.NewHostTmpNotFound(hosttmpKey)
}

// PID is `plugin_pid` (`DockerPlugin.py:110`): the plugin's init process, read
// out of the runc state file.
//
// Deferred with [pluginService.StorePath]; ported for the same reason. Python's
// `_get_plugin_state` returns `{}` when the file does not exist and
// `state['init_process_pid']` then KeyErrors, which is reproduced as a
// [model.PyRuntimeError] rather than a panic (PORT_SPEC §10).
func (p *pluginService) PID(ctx context.Context) (int, error) {
	plugin, _, err := p.manager.api.PluginInspectWithRaw(ctx, p.currentName)
	if err != nil {
		return 0, err
	}

	statePath := strings.ReplaceAll(pluginStatePath, "{id}", plugin.ID)
	raw, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, newPyKeyError("init_process_pid")
		}
		return 0, err
	}

	var state struct {
		InitProcessPID *int `json:"init_process_pid"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return 0, err
	}
	if state.InitProcessPID == nil {
		return 0, newPyKeyError("init_process_pid")
	}
	return *state.InitProcessPID, nil
}
