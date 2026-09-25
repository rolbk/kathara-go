package docker

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/model"
)

// Linux permits more interface-name characters than lab.ext, but rejecting
// names outside the parser's grammar matters here: the network label can also
// be supplied by the daemon, and these operations run with host privileges.
var externalInterfaceName = regexp.MustCompile(`^[\p{L}\p{N}_]+(?:\.[0-9]+)?$`)

const vdeExtBinary = "/usr/local/bin/vde_ext"

func checkExternalHost() error {
	if runtime.GOOS != "linux" {
		return kerrors.NewOS("External collision domains available only on Linux systems.")
	}
	admin, err := util.IsAdmin()
	if err != nil {
		return err
	}
	if !admin {
		return kerrors.New(kerrors.ErrPrivilege, "You must be root in order to use external collision domains.")
	}
	return nil
}

func externalCommand(ctx context.Context, name string, args ...string) error {
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func interfaceExists(ctx context.Context, name string) (bool, error) {
	output, err := exec.CommandContext(ctx, "ip", "link", "show", "dev", name).CombinedOutput()
	if err == nil {
		return true, nil
	}
	if _, ok := err.(*exec.ExitError); ok {
		return false, nil
	}
	return false, fmt.Errorf("ip link show dev %s: %w: %s", name, err, strings.TrimSpace(string(output)))
}

// preflightExternalInterfaces runs before Docker creates a network. Missing
// host interfaces or helpers should not leave behind a labelled but unusable
// network that a later lstart would mistake for a completed deployment.
func (s *linkService) preflightExternalInterfaces(ctx context.Context, links []model.ExternalLink) error {
	if err := checkExternalHost(); err != nil {
		return err
	}
	if s.manager.settings.RemoteURL != nil {
		return kerrors.NewOS("lab.ext cannot attach host interfaces to a remote Docker daemon.")
	}
	if _, err := exec.LookPath("ip"); err != nil {
		return err
	}
	if s.manager.plugin.isVDE() {
		if _, err := exec.LookPath("nsenter"); err != nil {
			return err
		}
		if info, err := os.Stat(vdeExtBinary); err != nil || info.Mode()&0o111 == 0 {
			return kerrors.NewOS("Cannot execute `" + vdeExtBinary + "` required for external collision domains.")
		}
	}
	for _, link := range links {
		if link.VLAN < 0 || link.VLAN >= 4095 {
			return kerrors.NewValue("External VLAN ID must be in range [0, 4094].")
		}
		fullName := link.FullName()
		if !externalInterfaceName.MatchString(fullName) || !externalInterfaceName.MatchString(link.Interface) {
			return kerrors.NewSyntax("Invalid external interface name `" + fullName + "`.")
		}
		present, err := interfaceExists(ctx, link.Interface)
		if err != nil {
			return err
		}
		if !present {
			return kerrors.New(kerrors.ErrInterfaceNotFound,
				"Interface `"+link.Interface+"` not found on the host machine.")
		}
	}
	return nil
}

func (s *linkService) attachExternalInterfaces(ctx context.Context, links []model.ExternalLink, network *Network) error {
	if err := s.preflightExternalInterfaces(ctx, links); err != nil {
		return err
	}

	bridge := BridgeName(network.ID)
	for _, link := range links {
		fullName := link.FullName()
		var err error
		present := false
		if link.VLAN != 0 {
			present, err = interfaceExists(ctx, fullName)
			if err != nil {
				return err
			}
			if !present {
				if err := externalCommand(ctx, "ip", "link", "add", "link", link.Interface,
					"name", fullName, "type", "vlan", "id", strconv.Itoa(link.VLAN)); err != nil {
					return err
				}
			}
			if err := externalCommand(ctx, "ip", "link", "set", "dev", link.Interface, "up"); err != nil {
				return err
			}
		}
		if s.manager.plugin.isVDE() {
			if err := externalCommand(ctx, "ip", "link", "set", "dev", fullName, "up"); err != nil {
				return err
			}
			pid, err := s.manager.plugin.PID(ctx)
			if err != nil {
				return err
			}
			store, err := s.manager.plugin.StorePath(ctx)
			if err != nil {
				return err
			}
			switchPath := filepath.Join(store, bridge)
			pidPath := filepath.Join(switchPath, "pid_"+fullName)
			// The shell receives every variable as a positional argument. In
			// particular, no interface name or plugin path is interpolated into
			// command text executed with root privileges.
			if err := externalCommand(ctx, "nsenter", "-t", strconv.Itoa(pid), "-i", "-n", "-p", "-u", "--",
				"/bin/sh", "-c", vdeExtBinary+` -s "$1/ctl" -p "$2" "$3" >/dev/null 2>&1 &`,
				"sh", switchPath, pidPath, fullName); err != nil {
				return err
			}
		} else if s.manager.plugin.isBridge() {
			if err := externalCommand(ctx, "ip", "link", "set", "dev", fullName,
				"master", bridge, "up"); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *linkService) deleteExternalInterfaces(ctx context.Context, label string, network *Network) error {
	if err := checkExternalHost(); err != nil {
		return err
	}
	if s.manager.settings.RemoteURL != nil {
		return kerrors.NewOS("lab.ext cannot detach host interfaces from a remote Docker daemon.")
	}
	for _, fullName := range strings.Split(label, ";") {
		if !externalInterfaceName.MatchString(fullName) {
			return kerrors.NewSyntax("Invalid external interface name `" + fullName + "`.")
		}
		if s.manager.plugin.isVDE() {
			pid, err := s.manager.plugin.PID(ctx)
			if err != nil {
				return err
			}
			store, err := s.manager.plugin.StorePath(ctx)
			if err != nil {
				return err
			}
			pidPath := filepath.Join(store, BridgeName(network.ID), "pid_"+fullName)
			if err := externalCommand(ctx, "nsenter", "-t", strconv.Itoa(pid), "-i", "-n", "-p", "-u", "--",
				"/bin/sh", "-c", `if [ -f "$1" ]; then kill -2 "$(cat "$1")"; fi`, "sh", pidPath); err != nil {
				return err
			}
		}
		if strings.Contains(fullName, ".") {
			present, err := interfaceExists(ctx, fullName)
			if err != nil {
				return err
			}
			if present {
				if err := externalCommand(ctx, "ip", "link", "delete", "dev", fullName); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
