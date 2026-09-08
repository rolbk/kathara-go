package main

// A Step is one invocation of the binary under test.
type Step struct {
	// Name identifies the step in the recording and in diffs.
	Name string
	// Args is the argv after the binary itself.
	Args []string
	// Dir selects the working directory: "lab" is the scratch scenario
	// directory, "root" is the scratch root above it.
	Dir string
	// NoDocker skips the post-step Docker snapshot (pure-parsing steps).
	NoDocker bool
}

// A Flow is an ordered sequence of steps run against one scratch scenario.
type Flow struct {
	Name string
	// Fixture names the testdata scenario copied into the scratch dir, or ""
	// for a flow that needs no scenario on disk.
	Fixture string
	Steps   []Step
}

// The four flows the merge gate has no golden for. Every step that changes
// backend state is followed by a Docker snapshot, so a divergence that never
// reaches stdout (a network left behind, an interface attached with different
// driver opts) still shows up in the diff.
var flows = []Flow{
	// (1) The full vstart/vconfig/vclean sugar surface, PORT_SPEC §3.4.
	{
		Name:    "vsugar",
		Fixture: "",
		Steps: []Step{
			{Name: "01-vstart-pc1", Args: []string{"vstart", "--noterminals", "-n", "pc1", "--eth", "0:A"}},
			{Name: "02-vstart-pc2-same-cd", Args: []string{"vstart", "--noterminals", "--name", "pc2", "--eth", "0:A"}},
			{Name: "03-list", Args: []string{"list"}},
			{Name: "04-vconfig-add", Args: []string{"vconfig", "-n", "pc1", "--add", "B"}},
			{Name: "05-list", Args: []string{"list"}},
			{Name: "06-vconfig-add-mac", Args: []string{"vconfig", "-n", "pc1", "--add", "C/00:11:22:33:44:55"}},
			{Name: "07-vconfig-rm", Args: []string{"vconfig", "-n", "pc1", "--rm", "B"}},
			{Name: "08-vconfig-rm-again", Args: []string{"vconfig", "-n", "pc1", "--rm", "B"}},
			{Name: "09-vconfig-missing-device", Args: []string{"vconfig", "-n", "nope", "--add", "D"}},
			{Name: "10-vclean-pc1", Args: []string{"vclean", "-n", "pc1"}},
			{Name: "11-vclean-pc2", Args: []string{"vclean", "--name", "pc2"}},
			{Name: "12-vclean-again", Args: []string{"vclean", "-n", "pc2"}},
			{Name: "13-list", Args: []string{"list"}},
		},
	},
	// (1b) The flag/alias surface of the same three commands, without Docker:
	// every row of CLI_SURFACE.md §6/§7/§8 that can be reached by a dry run or
	// by an argparse rejection.
	{
		Name:    "vsugar-args",
		Fixture: "",
		Steps: []Step{
			{Name: "01-help-vstart", Args: []string{"vstart", "-h"}, NoDocker: true},
			{Name: "02-help-vconfig", Args: []string{"vconfig", "--help"}, NoDocker: true},
			{Name: "03-help-vclean", Args: []string{"vclean", "-h"}, NoDocker: true},
			{Name: "04-print", Args: []string{"vstart", "-n", "pc1", "--print"}, NoDocker: true},
			{Name: "05-dry-run-alias", Args: []string{"vstart", "--name", "pc1", "--dry-run"}, NoDocker: true},
			{Name: "06-print-full-surface", Args: []string{"vstart", "-n", "pc1", "--print",
				"--eth", "0:A", "1:B/00:11:22:33:44:55", "--mem", "64m", "--cpus", "0.5",
				"-i", "kathara/base", "--bridged", "--port", "8080:80/tcp",
				"--sysctl", "net.ipv4.ip_forward=1", "--env", "K=V", "--ulimit", "nofile=1024:2048",
				"--volume", "/tmp|/mnt|ro", "--shell", "/bin/sh", "--entrypoint", "/bin/true",
				"--", "-x", "-y"}, NoDocker: true},
			{Name: "07-terminals-conflict", Args: []string{"vstart", "-n", "pc1", "--terminals", "--noterminals", "--print"}, NoDocker: true},
			{Name: "08-numterms-conflict", Args: []string{"vstart", "-n", "pc1", "--num_terms", "2", "--noterminals", "--print"}, NoDocker: true},
			{Name: "09-hosthome-conflict", Args: []string{"vstart", "-n", "pc1", "-H", "--hosthome", "--print"}, NoDocker: true},
			{Name: "10-missing-name", Args: []string{"vstart", "--print"}, NoDocker: true},
			{Name: "11-bad-eth", Args: []string{"vstart", "-n", "pc1", "--eth", "A"}, NoDocker: true},
			{Name: "12-nonnumeric-eth", Args: []string{"vstart", "-n", "pc1", "--eth", "x:A"}, NoDocker: true},
			{Name: "13-bad-volume", Args: []string{"vstart", "-n", "pc1", "--volume", "/tmp|/mnt|zz"}, NoDocker: true},
			{Name: "14-vconfig-no-group", Args: []string{"vconfig", "-n", "pc1"}, NoDocker: true},
			{Name: "15-vconfig-both", Args: []string{"vconfig", "-n", "pc1", "--add", "A", "--rm", "B"}, NoDocker: true},
			{Name: "16-vconfig-empty-add", Args: []string{"vconfig", "-n", "pc1", "--add"}, NoDocker: true},
			{Name: "17-vconfig-bad-rm", Args: []string{"vconfig", "-n", "pc1", "--rm", "a b"}, NoDocker: true},
			{Name: "18-vclean-missing-name", Args: []string{"vclean"}, NoDocker: true},
			{Name: "19-vstart-unknown-flag", Args: []string{"vstart", "-n", "pc1", "--nope"}, NoDocker: true},
		},
	},
	// (1c) The device-configuration surface of `vstart` actually deployed, so
	// that PORT_SPEC §3.4's "every flag must be identical" is checked against
	// the container the daemon ended up with and not only against argparse.
	{
		Name:    "vstart-full",
		Fixture: "",
		Steps: []Step{
			{Name: "01-vstart-full", Args: []string{"vstart", "--noterminals", "-n", "pc1",
				"--eth", "0:A", "1:B/00:11:22:33:44:55",
				"--mem", "64m", "--cpus", "0.5", "-i", "kathara/base",
				"--bridged", "--port", "8080:80/tcp", "8443:443/udp",
				"--sysctl", "net.ipv4.ip_forward=1", "--env", "K=V", "L=W",
				"--ulimit", "nofile=1024:2048", "--volume", "/tmp|/mnt|ro",
				"--shell", "/bin/sh", "-e", "touch /tmp/from-exec",
				"--entrypoint", "/bin/sh", "--", "-c", "sleep 3600"}},
			{Name: "02-list", Args: []string{"list"}},
			{Name: "03-vstart-hosthome", Args: []string{"vstart", "--noterminals", "-n", "pc2", "--hosthome"}},
			{Name: "04-vstart-privileged", Args: []string{"vstart", "--noterminals", "-n", "pc3", "--privileged"}},
			{Name: "05-vstart-duplicate", Args: []string{"vstart", "--noterminals", "-n", "pc1", "--eth", "0:A"}},
			{Name: "06-vclean-all", Args: []string{"vclean", "-n", "pc1"}},
			{Name: "07-vclean-pc2", Args: []string{"vclean", "-n", "pc2"}},
			{Name: "08-vclean-pc3", Args: []string{"vclean", "-n", "pc3"}},
		},
	},
	// (5) Edge cases the other flows do not reach: the `lrestart --xterm`
	// latent bug, re-running a command that already ran, and addressing a
	// running lab device through the vlab.
	{
		Name:    "edge",
		Fixture: "lab",
		Steps: []Step{
			{Name: "01-lclean-nothing", Args: []string{"lclean"}, Dir: "lab"},
			{Name: "02-lstart", Args: []string{"lstart", "--noterminals"}, Dir: "lab"},
			{Name: "03-lstart-again", Args: []string{"lstart", "--noterminals"}, Dir: "lab"},
			{Name: "04-vconfig-on-lab-device", Args: []string{"vconfig", "-n", "pc1", "--add", "Q"}, Dir: "lab"},
			{Name: "05-vclean-lab-device", Args: []string{"vclean", "-n", "pc1"}, Dir: "lab"},
			{Name: "06-lrestart-xterm", Args: []string{"lrestart", "--xterm", "/usr/bin/xterm"}, Dir: "lab"},
			{Name: "07-lstart", Args: []string{"lstart", "--noterminals"}, Dir: "lab"},
			{Name: "08-exec-wait", Args: []string{"exec", "-d", ".", "--wait", "pc1", "echo waited"}, Dir: "lab"},
			{Name: "09-lclean-one", Args: []string{"lclean", "pc1"}, Dir: "lab"},
			{Name: "10-lclean-exclude", Args: []string{"lclean", "--exclude", "pc2"}, Dir: "lab"},
			{Name: "11-lclean", Args: []string{"lclean"}, Dir: "lab"},
			{Name: "12-lclean-missing-dir", Args: []string{"lclean", "-d", "/nonexistent-dir"}, Dir: "lab"},
		},
	},
	// (2) lstart → lconfig add/rm → lrestart → lclean over a real scenario.
	{
		Name:    "lab",
		Fixture: "lab",
		Steps: []Step{
			{Name: "01-lstart", Args: []string{"lstart", "--noterminals"}, Dir: "lab"},
			{Name: "02-list", Args: []string{"list"}, Dir: "lab"},
			{Name: "03-lconfig-add", Args: []string{"lconfig", "-d", ".", "-n", "pc1", "--add", "C"}, Dir: "lab"},
			{Name: "04-list", Args: []string{"list"}, Dir: "lab"},
			{Name: "05-lconfig-rm", Args: []string{"lconfig", "-d", ".", "-n", "pc1", "--rm", "C"}, Dir: "lab"},
			{Name: "06-lconfig-rm-unknown", Args: []string{"lconfig", "-d", ".", "-n", "pc1", "--rm", "Z"}, Dir: "lab"},
			{Name: "07-lconfig-missing-device", Args: []string{"lconfig", "-d", ".", "-n", "nope", "--add", "C"}, Dir: "lab"},
			{Name: "08-lrestart", Args: []string{"lrestart", "--noterminals"}, Dir: "lab"},
			{Name: "09-list", Args: []string{"list"}, Dir: "lab"},
			{Name: "10-lrestart-one", Args: []string{"lrestart", "--noterminals", "pc1"}, Dir: "lab"},
			{Name: "11-list", Args: []string{"list"}, Dir: "lab"},
			{Name: "12-lclean", Args: []string{"lclean"}, Dir: "lab"},
			{Name: "13-list", Args: []string{"list"}, Dir: "lab"},
		},
	},
	// (3) list / wipe with labs up.
	{
		Name:    "listwipe",
		Fixture: "lab",
		Steps: []Step{
			{Name: "01-lstart", Args: []string{"lstart", "--noterminals"}, Dir: "lab"},
			{Name: "02-vstart", Args: []string{"vstart", "--noterminals", "-n", "vpc", "--eth", "0:Z"}, Dir: "root"},
			{Name: "03-list", Args: []string{"list"}, Dir: "root"},
			{Name: "04-list-name", Args: []string{"list", "-n", "pc1"}, Dir: "root"},
			{Name: "05-list-unknown-name", Args: []string{"list", "-n", "nosuch"}, Dir: "root"},
			{Name: "06-list-all", Args: []string{"list", "-a"}, Dir: "root"},
			{Name: "07-wipe-force", Args: []string{"wipe", "-f"}, Dir: "root"},
			{Name: "08-list", Args: []string{"list"}, Dir: "root"},
			{Name: "09-lstart", Args: []string{"lstart", "--noterminals"}, Dir: "lab"},
			{Name: "10-wipe-force-all", Args: []string{"wipe", "-f", "-a"}, Dir: "root"},
			{Name: "11-list", Args: []string{"list"}, Dir: "root"},
			{Name: "12-wipe-no-force", Args: []string{"wipe"}, Dir: "root"},
		},
	},
	// (4) exec: exit-code propagation and stream routing.
	{
		Name:    "exec",
		Fixture: "lab",
		Steps: []Step{
			{Name: "01-lstart", Args: []string{"lstart", "--noterminals"}, Dir: "lab"},
			{Name: "02-exec-true", Args: []string{"exec", "-d", ".", "pc1", "true"}, Dir: "lab", NoDocker: true},
			{Name: "03-exec-false", Args: []string{"exec", "-d", ".", "pc1", "false"}, Dir: "lab", NoDocker: true},
			{Name: "04-exec-exit7", Args: []string{"exec", "-d", ".", "pc1", "sh -c 'exit 7'"}, Dir: "lab", NoDocker: true},
			{Name: "05-exec-missing-binary", Args: []string{"exec", "-d", ".", "pc1", "definitely-not-a-binary"}, Dir: "lab", NoDocker: true},
			{Name: "06-exec-stdout", Args: []string{"exec", "-d", ".", "pc1", "echo hello-stdout"}, Dir: "lab", NoDocker: true},
			{Name: "07-exec-stderr", Args: []string{"exec", "-d", ".", "pc1", "ls /definitely-not-here"}, Dir: "lab", NoDocker: true},
			{Name: "08-exec-both", Args: []string{"exec", "-d", ".", "pc1", "sh -c 'echo O; echo E >&2; exit 3'"}, Dir: "lab", NoDocker: true},
			{Name: "09-exec-no-stdout", Args: []string{"exec", "-d", ".", "--no-stdout", "pc1", "sh -c 'echo O; echo E >&2; exit 3'"}, Dir: "lab", NoDocker: true},
			{Name: "10-exec-no-stderr", Args: []string{"exec", "-d", ".", "--no-stderr", "pc1", "sh -c 'echo O; echo E >&2; exit 3'"}, Dir: "lab", NoDocker: true},
			{Name: "11-exec-no-both", Args: []string{"exec", "-d", ".", "--no-stdout", "--no-stderr", "pc1", "sh -c 'echo O; echo E >&2; exit 3'"}, Dir: "lab", NoDocker: true},
			{Name: "12-exec-multiword", Args: []string{"exec", "-d", ".", "pc1", "--", "sh", "-c", "echo M; exit 5"}, Dir: "lab", NoDocker: true},
			{Name: "13-exec-unknown-device", Args: []string{"exec", "-d", ".", "nosuchdevice", "true"}, Dir: "lab", NoDocker: true},
			{Name: "14-exec-vmachine-none", Args: []string{"exec", "-v", "vpc", "true"}, Dir: "root", NoDocker: true},
			{Name: "15-exec-no-command", Args: []string{"exec", "-d", ".", "pc1"}, Dir: "lab", NoDocker: true},
			{Name: "16-exec-dir-and-vmachine", Args: []string{"exec", "-d", ".", "-v", "pc1", "true"}, Dir: "lab", NoDocker: true},
			{Name: "17-lclean", Args: []string{"lclean"}, Dir: "lab"},
		},
	},
}
