package main

// Flows added by the wide-verification pass. They cover the command surface
// that neither the Layer A goldens (which drive `lstart`/probe/`lclean` with a
// fixed argv per scenario) nor the original seven flows reach:
func init() { flows = append(flows, extraFlows...) }

var extraFlows = []Flow{
	// (6) Everything about lstart/lrestart/linfo/list/wipe/check/connect that
	// can be decided without a daemon: help, the top-level dispatcher, the
	// mutually-exclusive groups, `--print`, and the custom argparse types.
	{
		Name:    "cmd-args",
		Fixture: "lab",
		Steps: []Step{
			// Help for every sub-command the earlier flows do not cover.
			// vstart/vconfig/vclean are in `vsugar-args`.
			{Name: "01-help-lstart", Args: []string{"lstart", "-h"}, NoDocker: true},
			{Name: "02-help-lclean", Args: []string{"lclean", "-h"}, NoDocker: true},
			{Name: "03-help-lrestart", Args: []string{"lrestart", "-h"}, NoDocker: true},
			{Name: "04-help-linfo", Args: []string{"linfo", "-h"}, NoDocker: true},
			{Name: "05-help-lconfig", Args: []string{"lconfig", "-h"}, NoDocker: true},
			{Name: "06-help-list", Args: []string{"list", "-h"}, NoDocker: true},
			{Name: "07-help-wipe", Args: []string{"wipe", "-h"}, NoDocker: true},
			{Name: "08-help-check", Args: []string{"check", "-h"}, NoDocker: true},
			{Name: "09-help-connect", Args: []string{"connect", "-h"}, NoDocker: true},
			{Name: "10-help-exec", Args: []string{"exec", "-h"}, NoDocker: true},

			// The top-level dispatcher (kathara.py:29-84).
			{Name: "11-toplevel-none", Args: []string{}, NoDocker: true},
			{Name: "12-toplevel-help", Args: []string{"-h"}, NoDocker: true},
			{Name: "13-toplevel-long-help", Args: []string{"--help"}, NoDocker: true},
			{Name: "14-toplevel-version", Args: []string{"-v"}, NoDocker: true},
			{Name: "15-toplevel-long-version", Args: []string{"--version"}, NoDocker: true},
			{Name: "16-toplevel-unknown", Args: []string{"bogus"}, NoDocker: true},
			{Name: "17-toplevel-uppercase", Args: []string{"LSTART"}, NoDocker: true},

			// lstart's dry-run and its three tri-state MEGs.
			{Name: "18-lstart-print", Args: []string{"lstart", "--print"}, Dir: "lab", NoDocker: true},
			{Name: "19-lstart-dry-mode", Args: []string{"lstart", "--dry-mode"}, Dir: "lab", NoDocker: true},
			{Name: "20-lstart-terminals-meg", Args: []string{"lstart", "--print", "--terminals", "--noterminals"}, Dir: "lab", NoDocker: true},
			{Name: "21-lstart-hosthome-meg", Args: []string{"lstart", "--print", "-H", "--hosthome"}, Dir: "lab", NoDocker: true},
			{Name: "22-lstart-shared-meg", Args: []string{"lstart", "--print", "-S", "--shared"}, Dir: "lab", NoDocker: true},

			{Name: "23-lstart-pass-ok", Args: []string{"lstart", "--print", "-o", "mem=64m"}, Dir: "lab", NoDocker: true},
			{Name: "24-lstart-pass-malformed", Args: []string{"lstart", "--print", "-o", "bogus"}, Dir: "lab", NoDocker: true},

			{Name: "25-lstart-missing-dir", Args: []string{"lstart", "--print", "-d", "/nonexistent-dir"}, NoDocker: true},
			{Name: "26-lstart-unknown-flag", Args: []string{"lstart", "--bogus"}, Dir: "lab", NoDocker: true},

			{Name: "27-lrestart-print", Args: []string{"lrestart", "--print"}, Dir: "lab", NoDocker: true},
			{Name: "28-lrestart-terminal-emu", Args: []string{"lrestart", "--terminal-emu", "/usr/bin/xterm"}, Dir: "lab", NoDocker: true},

			// linfo: the flag shapes are observable even though this implementation
			// answers FeatureNotAvailable.
			{Name: "29-linfo-directory", Args: []string{"linfo", "-d", "."}, Dir: "lab", NoDocker: true},
			{Name: "30-linfo-conf-name", Args: []string{"linfo", "-c", "-n", "pc1"}, Dir: "lab", NoDocker: true},
			{Name: "31-linfo-watch-conf-meg", Args: []string{"linfo", "-w", "-c"}, Dir: "lab", NoDocker: true},
			{Name: "32-linfo-name-topology-meg", Args: []string{"linfo", "-n", "pc1", "-t"}, Dir: "lab", NoDocker: true},

			{Name: "33-list-name-missing-value", Args: []string{"list", "-n"}, NoDocker: true},
			{Name: "34-wipe-settings-all-meg", Args: []string{"wipe", "-s", "-a"}, NoDocker: true},
			{Name: "35-lconfig-no-group", Args: []string{"lconfig", "-n", "pc1"}, Dir: "lab", NoDocker: true},
			{Name: "36-lclean-exclude-empty", Args: []string{"lclean", "--exclude"}, Dir: "lab", NoDocker: true},
			{Name: "37-check-extra-arg", Args: []string{"check", "extra"}, NoDocker: true},
		},
	},

	// (7) The lstart flag surface actually deployed: what each flag does to
	// the containers the daemon ends up with.
	{
		Name:    "lstart-flags",
		Fixture: "lab",
		Steps: []Step{
			{Name: "01-lstart-select-pc1", Args: []string{"lstart", "--noterminals", "pc1"}, Dir: "lab"},
			{Name: "02-list", Args: []string{"list"}, Dir: "lab"},
			{Name: "03-lclean", Args: []string{"lclean"}, Dir: "lab"},
			{Name: "04-lstart-exclude-pc2", Args: []string{"lstart", "--noterminals", "--exclude", "pc2"}, Dir: "lab"},
			{Name: "05-lclean", Args: []string{"lclean"}, Dir: "lab"},
			{Name: "06-lstart-select-unknown", Args: []string{"lstart", "--noterminals", "nosuch"}, Dir: "lab"},
			{Name: "07-lstart-exclude-unknown", Args: []string{"lstart", "--noterminals", "--exclude", "nosuch"}, Dir: "lab"},
			{Name: "08-lstart-pass-mem", Args: []string{"lstart", "--noterminals", "-o", "mem=64m"}, Dir: "lab"},
			{Name: "09-lclean", Args: []string{"lclean"}, Dir: "lab"},
			{Name: "10-lstart-hosthome", Args: []string{"lstart", "--noterminals", "--hosthome"}, Dir: "lab"},
			{Name: "11-lclean", Args: []string{"lclean"}, Dir: "lab"},
			{Name: "12-lstart-no-shared", Args: []string{"lstart", "--noterminals", "--no-shared"}, Dir: "lab"},
			{Name: "13-lclean", Args: []string{"lclean"}, Dir: "lab"},
			{Name: "14-lstart-privileged", Args: []string{"lstart", "--noterminals", "--privileged"}, Dir: "lab"},
			{Name: "15-lclean", Args: []string{"lclean"}, Dir: "lab"},
			{Name: "16-lstart-list", Args: []string{"lstart", "--noterminals", "-l"}, Dir: "lab"},
			{Name: "17-lclean", Args: []string{"lclean"}, Dir: "lab"},
		},
	},

	// (8) `-F/--force-lab`: FolderParser, which no golden and no flow drives.
	{
		Name:    "folderlab",
		Fixture: "folderlab",
		Steps: []Step{
			{Name: "01-lstart-no-labconf", Args: []string{"lstart", "--noterminals"}, Dir: "lab"},
			{Name: "02-lstart-print-force", Args: []string{"lstart", "--print", "-F"}, Dir: "lab", NoDocker: true},
			{Name: "03-lstart-force", Args: []string{"lstart", "--noterminals", "-F"}, Dir: "lab"},
			{Name: "04-list", Args: []string{"list"}, Dir: "lab"},
			{Name: "05-lclean", Args: []string{"lclean"}, Dir: "lab"},
		},
	},

	{
		Name:    "checkcmd",
		Fixture: "",
		Steps: []Step{
			{Name: "01-check", Args: []string{"check"}},
			{Name: "02-check-again", Args: []string{"check"}},
		},
	},
}
