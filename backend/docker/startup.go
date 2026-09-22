// This file is `STARTUP_COMMANDS` (`DockerMachine.py:43`) and
// `SHUTDOWN_COMMANDS` (:99), plus the two `"; ".join(...).format(...)` calls
// that turn them into the single shell line each container runs.

package docker

import "strings"

// startupCommands is `STARTUP_COMMANDS`, element for element and byte for byte.
var startupCommands = []string{
	// Unmount the /etc/resolv.conf and /etc/hosts files, automatically mounted
	// by Docker inside the container, so custom user files can replace them.
	"umount /etc/resolv.conf",
	"umount /etc/hosts",

	// Copy the device folder (if present) from the hostlab directory into the
	// container root, replacing what is there.
	"if [ -d \"/hostlab/{machine_name}\" ]; then " +
		"(cd /hostlab/{machine_name} && tar c .) | (cd / && tar xhf - --no-same-owner --no-same-permissions); fi",

	// Default /etc/hosts mappings when the user configured none.
	"if [ ! -s \"/etc/hosts\" ]; then " +
		"echo '127.0.0.1 localhost' > /etc/hosts",
	"echo '::1 localhost' >> /etc/hosts",
	"echo '127.0.1.1 {machine_name}' >> /etc/hosts",
	"fi",

	// Permissions for /var/www.
	"if [ -d \"/var/www\" ]; then " +
		"chmod -R 777 /var/www/*; fi",

	// Permissions for Quagga.
	"if [ -d \"/etc/quagga\" ]; then " +
		"chown -R quagga:quagga /etc/quagga/",
	"chmod 640 /etc/quagga/*; fi",

	// Permissions for FRR.
	"if [ -d \"/etc/frr\" ]; then " +
		"chown -R frr:frr /etc/frr/",
	"chmod 640 /etc/frr/*; fi",

	// shared.startup, with command echoing enabled and output captured.
	"if [ -f \"/hostlab/shared.startup\" ]; then " +
		"chmod u+x /hostlab/shared.startup",
	"sed -i \"1s;^;set -x\\n\\n;\" /hostlab/shared.startup",
	"/hostlab/shared.startup &> /var/log/shared.log; fi",

	// {machine}.startup, likewise.
	"if [ -f \"/hostlab/{machine_name}.startup\" ]; then " +
		"chmod u+x /hostlab/{machine_name}.startup",
	"sed -i \"1s;^;set -x\\n\\n;\" /hostlab/{machine_name}.startup",
	"/hostlab/{machine_name}.startup &> /var/log/startup.log; fi",

	// The user's exec commands.
	"{machine_commands}",

	// The completion sentinel `_wait_startup_execution` polls with `cat`.
	"touch /tmp/EOS",
}

// shutdownCommands is `SHUTDOWN_COMMANDS` (`DockerMachine.py:99`), run
// synchronously and PRIVILEGED by `_delete_machine` (:1103-1109) — the one
// exec in the backend that raises privileges, where startup deliberately does
// not.
var shutdownCommands = []string{
	"if [ -f \"/hostlab/{machine_name}.shutdown\" ]; then " +
		"chmod u+x /hostlab/{machine_name}.shutdown; /hostlab/{machine_name}.shutdown; fi",

	"if [ -f \"/hostlab/shared.shutdown\" ]; then " +
		"chmod u+x /hostlab/shared.shutdown; /hostlab/shared.shutdown; fi",
}

// noopCommand is the `":"` that `machine_commands` falls back to when the
// device has no exec commands (`DockerMachine.py:548`): the shell no-op, so
// the joined line never has an empty segment.
const noopCommand = ":"

// interleaveExecCommands is `DockerMachine.start`'s rewrite of
// `machine.meta['exec_commands']` (:538-543): before each command, an echo of
// it into the startup log.
func interleaveExecCommands(commands []string) []string {
	if len(commands) == 0 {
		return nil
	}
	out := make([]string, 0, 2*len(commands))
	for _, command := range commands {
		out = append(out, "echo \""+"++ "+command+"\" &>> /var/log/startup.log")
		out = append(out, command)
	}
	return out
}

// renderStartupCommands is
// `"; ".join(STARTUP_COMMANDS).format(machine_name=…, machine_commands=…)`
// (`DockerMachine.py:546-549`).
func renderStartupCommands(machineName string, execCommands []string) string {
	machineCommands := noopCommand
	if len(execCommands) > 0 {
		machineCommands = strings.Join(execCommands, "; ")
	}

	line := strings.Join(startupCommands, "; ")
	line = strings.ReplaceAll(line, "{machine_name}", machineName)
	line = strings.ReplaceAll(line, "{machine_commands}", machineCommands)
	return line
}

// renderShutdownCommands is
// `"; ".join(SHUTDOWN_COMMANDS).format(machine_name=…)`
// (`DockerMachine.py:1097`). The name comes from the container's `name` label,
// not from a model object — by teardown time there may be no scenario left.
func renderShutdownCommands(machineName string) string {
	line := strings.Join(shutdownCommands, "; ")
	return strings.ReplaceAll(line, "{machine_name}", machineName)
}
