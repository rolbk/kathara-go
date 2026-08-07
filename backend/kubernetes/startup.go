// This file is `STARTUP_COMMANDS` and `SHUTDOWN_COMMANDS`
// (`KubernetesMachine.py:46,121`) plus the two things that fill their holes: the
// `str.format` pass over the JOINED template and the `sysctl -w -q %s=%d`
// rendering of the merged sysctl dict.
//
// The strings are the container contract. They are what actually unpacks the
// hostlab archive, runs `<device>.startup` and touches the `/tmp/EOS` sentinel
// that `kathara connect` waits for on the Docker side, and a single changed
// character is a device that boots differently. PORT_SPEC §0.4 freezes them,
// and startup_test.go pins the joined result against the oracle.

package kubernetes

import (
	"strconv"
	"strings"

	"github.com/KatharaFramework/kathara-go/model"
)

// startupCommands is `STARTUP_COMMANDS` (`KubernetesMachine.py:46-119`),
// element for element.
//
// Python writes several of them as adjacent string literals, which CPython
// concatenates at compile time — `"if [ -f … ]; then " "base64 -d …"` is ONE
// element, not two — so the joined output has no `; ` between those halves. The
// elements below are the concatenated forms.
//
// Three of them are placeholders that [formatCommands] fills:
// `{sysctl_commands}`, `{machine_name}` (six occurrences) and
// `{machine_commands}`.
//
// Raw string literals are used wherever a backslash appears, because the `sed`
// lines carry a LITERAL backslash-n — Python's `"…set -x\\n\\n;…"` is the six
// characters `\n\n`, not two newlines — and a Go interpreted literal would turn
// them into real newlines and break the `sed` script.
var startupCommands = []string{
	// If execution flag file is found, abort (this means that postStart has
	// been called again). If not, flag the startup execution with a file.
	`if [ -f "/tmp/post_start" ]; then exit; else touch /tmp/post_start; fi`,

	`{sysctl_commands}`,

	// Removes /etc/bind already existing configuration from k8s internal DNS.
	`rm -Rf /etc/bind/*`,

	// Unmount the /etc/resolv.conf and /etc/hosts files, automatically mounted
	// by Kubernetes inside the container, so custom user files can overwrite
	// them.
	`umount /etc/resolv.conf`,
	`umount /etc/hosts`,

	// Parse hostlab.b64 (if present) and extract it into /.
	`if [ -f "/tmp/kathara/hostlab.b64" ]; then base64 -d /tmp/kathara/hostlab.b64 > /hostlab.tar.gz`,
	`tar xmfz /hostlab.tar.gz -C /; rm -f hostlab.tar.gz`,
	`fi`,

	// Copy the machine folder (if present) from the hostlab directory into the
	// root folder of the container.
	`if [ -d "/hostlab/{machine_name}" ]; then (cd /hostlab/{machine_name} && tar c .) | (cd / && tar xhf - --no-same-owner --no-same-permissions); fi`,

	// If /etc/hosts is not configured by the user, add the default mappings.
	`if [ ! -s "/etc/hosts" ]; then echo '127.0.0.1 localhost' > /etc/hosts`,
	`echo '::1 localhost' >> /etc/hosts`,
	`echo '127.0.1.1 {machine_name}' >> /etc/hosts`,
	`fi`,

	// Give proper permissions to /var/www.
	`if [ -d "/var/www" ]; then chmod -R 777 /var/www/*; fi`,

	// Give proper permissions to Quagga files (if present).
	`if [ -d "/etc/quagga" ]; then chown -R quagga:quagga /etc/quagga/`,
	`chmod 640 /etc/quagga/*; fi`,

	// Give proper permissions to FRR files (if present).
	`if [ -d "/etc/frr" ]; then chown -R frr:frr /etc/frr/`,
	`chmod 640 /etc/frr/*; fi`,

	// If shared.startup file is present, make it executable, prepend `set -x`
	// so the log carries the commands, and run it with output redirected.
	`if [ -f "/hostlab/shared.startup" ]; then chmod u+x /hostlab/shared.startup`,
	`sed -i "1s;^;set -x\n\n;" /hostlab/shared.startup`,
	`/hostlab/shared.startup &> /var/log/shared.log; fi`,

	// The same for <device>.startup.
	`if [ -f "/hostlab/{machine_name}.startup" ]; then chmod u+x /hostlab/{machine_name}.startup`,
	`sed -i "1s;^;set -x\n\n;" /hostlab/{machine_name}.startup`,
	`/hostlab/{machine_name}.startup &> /var/log/startup.log; fi`,

	// Remove the Kubernetes default gateway, which points at eth0 and causes
	// problems sometimes.
	`ip route del default dev eth0 || true`,

	// Placeholder for user commands.
	`{machine_commands}`,

	`touch /tmp/EOS`,
}

// shutdownCommands is `SHUTDOWN_COMMANDS` (`KubernetesMachine.py:121-131`),
// which `_delete_machine` execs in the pod before deleting the Deployment.
//
// The order is the reverse of the startup one: the device's own shutdown script
// runs BEFORE the shared one.
var shutdownCommands = []string{
	`if [ -f "/hostlab/{machine_name}.shutdown" ]; then chmod u+x /hostlab/{machine_name}.shutdown; /hostlab/{machine_name}.shutdown; fi`,
	`if [ -f "/hostlab/shared.shutdown" ]; then chmod u+x /hostlab/shared.shutdown; /hostlab/shared.shutdown; fi`,
}

// StartupCommandsString is
// `"; ".join(STARTUP_COMMANDS).format(machine_name=…, sysctl_commands=…,
// machine_commands=…)` (`KubernetesMachine.py:447-448`).
//
// The join happens BEFORE the format, which is what makes a literal `{` added
// to any element in the future a formatting error rather than a shell character
// (k8s-backend.md G27). The substituted values are not re-scanned, so a user's
// `exec` command containing braces is safe — [formatCommands] is a single pass
// for exactly that reason.
func StartupCommandsString(machineName, sysctlCommands, machineCommands string) string {
	return formatCommands(strings.Join(startupCommands, "; "), map[string]string{
		"machine_name":     machineName,
		"sysctl_commands":  sysctlCommands,
		"machine_commands": machineCommands,
	})
}

// ShutdownCommandsString is
// `"; ".join(SHUTDOWN_COMMANDS).format(machine_name=…)`
// (`KubernetesMachine.py:681`).
func ShutdownCommandsString(machineName string) string {
	return formatCommands(strings.Join(shutdownCommands, "; "), map[string]string{
		"machine_name": machineName,
	})
}

// formatCommands is `str.format(**fields)` restricted to the shape these two
// templates use: bare `{name}` replacement fields, no conversions, no format
// specs, and `{{`/`}}` as the escapes for a literal brace.
//
// It is a SINGLE pass. Python's `format` never re-examines what it substituted,
// so a device named `{machine_name}` — which nothing forbids, `model` validates
// device names against a charset that excludes braces, but `sysctl_commands`
// and `machine_commands` are user text — cannot cause a second substitution.
// A `strings.NewReplacer` would have the same property; a chain of
// `strings.ReplaceAll` calls would NOT, which is why this is spelled out.
//
// An unknown field name is left ALONE rather than dropped. Python raises
// `KeyError` there, and there is no such input: the templates are constants and
// every field they name is supplied. Leaving the text in place keeps a future
// editing mistake visible in the script instead of silently deleting a command.
func formatCommands(template string, fields map[string]string) string {
	var b strings.Builder
	b.Grow(len(template))

	for i := 0; i < len(template); {
		c := template[i]
		if c != '{' && c != '}' {
			b.WriteByte(c)
			i++
			continue
		}
		// `{{` and `}}` are the escapes for a literal brace.
		if i+1 < len(template) && template[i+1] == c {
			b.WriteByte(c)
			i += 2
			continue
		}
		if c == '}' {
			b.WriteByte(c)
			i++
			continue
		}

		end := strings.IndexByte(template[i:], '}')
		if end < 0 {
			b.WriteString(template[i:])
			break
		}
		name := template[i+1 : i+end]
		if value, ok := fields[name]; ok {
			b.WriteString(value)
		} else {
			b.WriteString(template[i : i+end+1])
		}
		i += end + 1
	}
	return b.String()
}

// SysctlCommands is
// `"; ".join(["sysctl -w -q %s=%d" % item for item in machine.meta["sysctls"].items()])`
// (`KubernetesMachine.py:444`).
//
// The `%d` is the whole of the subtlety (k8s-backend.md G9). `Machine.add_meta`
// stores a sysctl value as an int when `str.isnumeric()` accepts it and as a
// string otherwise (`model/Machine.py`), so `pc1[sysctl]=net.a.b=abc` reaches
// here as a str and CPython raises
// `TypeError: %d format: a real number is required, not str` — a crash on a
// path a lab.conf can produce. It is reproduced as a returned
// [model.PyRuntimeError] rather than being papered over by formatting the
// string, which would deploy a device Python refuses to deploy.
//
// A bool renders as 1/0 and a float truncates toward zero, which is what `%d`
// does; neither is reachable through `add_meta`, and both are here so that an
// API caller who reaches into [model.Meta] directly gets Python's answer.
//
// The iteration order is the merged dict's — the defaults in their literal
// order, then the device's own keys in insertion order (k8s-backend.md O4) —
// which [OrderedMap] preserves and a Go map would not.
func SysctlCommands(sysctls *model.OrderedMap[string, model.Scalar]) (string, error) {
	if sysctls == nil {
		return "", nil
	}

	parts := make([]string, 0, sysctls.Len())
	for _, entry := range sysctls.Entries() {
		rendered, err := formatSysctlValue(entry.Value)
		if err != nil {
			return "", err
		}
		parts = append(parts, "sysctl -w -q "+entry.Key+"="+rendered)
	}
	return strings.Join(parts, "; "), nil
}

// formatSysctlValue is CPython's `%d` on one sysctl value.
func formatSysctlValue(value model.Scalar) (string, error) {
	switch value.Kind() {
	case model.KindInt:
		v, _ := value.Value().(int64)
		return strconv.FormatInt(v, 10), nil
	case model.KindBool:
		if value.Truthy() {
			return "1", nil
		}
		return "0", nil
	case model.KindFloat:
		v, _ := value.Value().(float64)
		return strconv.FormatInt(int64(v), 10), nil
	case model.KindString:
		return "", newPyTypeError("%d format: a real number is required, not str")
	case model.KindStrings:
		return "", newPyTypeError("%d format: a real number is required, not list")
	default:
		// `KindAbsent` cannot come out of `Entries()`: a key that is in the map
		// has a value. NoneType is what Python would name if it could.
		return "", newPyTypeError("%d format: a real number is required, not NoneType")
	}
}
