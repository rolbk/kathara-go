// This file is `DockerPlugin.py`: making sure the Kathará network plugin is
// installed, enabled and — for the iptables flavour — pointed at the right
// `xtables.lock`.
// It runs from the constructor, on every launch, before anything else touches
// the daemon (`DockerManager.py:75-76`).

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
func (p *pluginService) CheckAndDownload(ctx context.Context) error {
	// `"Checking plugin `%s`..." % self.current_name` (`DockerPlugin.py:43`).
	slog.Debug("Checking plugin `" + p.currentName + "`...")

	plugin, _, err := p.manager.api.PluginInspectWithRaw(ctx, p.currentName)
	switch {
	case err == nil:
		// `plugin.upgrade()` — a no-op, see above.
	case isNotFound(err):
		if p.manager.settings.RemoteURL != nil {
			return kerrors.ErrPluginNotFound
		}
		// `f"Installing Kathara Network Plugin ({self.current_name})..."`
		// (`DockerPlugin.py:50`) — parentheses, no backticks.
		slog.Info("Installing Kathara Network Plugin (" + p.currentName + ")...")
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
		// `"Enabling plugin `%s`..." % self.current_name` (`DockerPlugin.py:58`).
		slog.Debug("Enabling plugin `" + p.currentName + "`...")
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
			// `DockerPlugin.py:64`, same string.
			slog.Debug("Enabling plugin `" + p.currentName + "`...")
			return p.manager.api.PluginEnable(ctx, p.currentName, types.PluginEnableOptions{Timeout: 0})
		}

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
		// `DockerPlugin.py:75`, same string.
		slog.Debug("Enabling plugin `" + p.currentName + "`...")
		return p.manager.api.PluginEnable(ctx, p.currentName, types.PluginEnableOptions{Timeout: 0})
	}

	// Neither flavour: `network_plugin` names something else, both `is_*`
	// predicates are false, and Python's if/elif chain falls through doing
	// nothing. So does this.
	return nil
}

// install is `client.plugins.install(name)` (`DockerPlugin.py:51`).
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
func (p *pluginService) configureXtablesMount(ctx context.Context, mount string) error {
	// `"Configuring xtables.lock source to `%s`..." % xtables_lock_mount`
	// (`DockerPlugin.py:179`).
	slog.Debug("Configuring xtables.lock source to `" + mount + "`...")
	return p.manager.api.PluginSet(ctx, p.currentName, []string{xtablesConfigurationKey + ".source=" + mount})
}

// xtablesLockMount is `_xtables_lock_mount` (`DockerPlugin.py:155`), the one
// three-way platform branch in this file:
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
