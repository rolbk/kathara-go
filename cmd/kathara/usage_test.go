package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestArgparseHelpFormatterMatchesOracle pins [parser.usageAt] byte for byte
// against `argparse.HelpFormatter`.
//
// The captures in `testdata/argparse_help/` are `parser.format_help()` run on
// the real `Kathara.cli.command.*Command` objects at COLUMNS=80. The Go flag
// sets carry port additions (`--format`, `--lab-hash`, `--lab-name`,
// `--from-archive`), so the commands themselves cannot match those captures;
// what is compared here is the *formatter*, driven by parsers that replicate
// Python's declarations exactly. Every layout branch the fourteen commands can
// reach is covered: a short help column (check, wipe), the 24-column cap, an
// invocation too long for its column, a wrapped usage line, an unwrapped one, a
// required option, a required group, both `nargs` list shapes, REMAINDER,
// a four-spelling action, and a description that folds.
func TestArgparseHelpFormatterMatchesOracle(t *testing.T) {
	for _, name := range []string{"check", "wipe", "list", "linfo", "lclean", "lconfig", "vconfig", "connect", "exec", "lstart", "lrestart", "vstart", "vclean"} {
		t.Run(name, func(t *testing.T) {
			p := oracleParser(t, name)
			want, err := os.ReadFile(filepath.Join("testdata", "argparse_help", name+".txt"))
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}
			got := p.usageAt(80)
			if got != string(want) {
				t.Errorf("help mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
			}
		})
	}
}

// oracleParser builds a parser with Python's declarations for name, and nothing
// the port added.
func oracleParser(t *testing.T, name string) *parser {
	t.Helper()
	p := newParser(name)
	flags := p.Flags()
	var (
		s  string
		bl bool
		ts = func() *tristate { return &tristate{constant: true} }
		sl = func() *stringList { return &stringList{} }
	)

	switch name {
	case "check":

	case "wipe":
		flags.BoolVarP(&bl, "force", "f", false, "Force the wipe.")
		flags.BoolVarP(&bl, "settings", "s", false, "Wipe the stored settings of the current user.")
		flags.BoolVarP(&bl, "all", "a", false,
			"Wipe all Kathara devices and collision domains of all users. MUST BE ROOT FOR THIS OPTION.")
		p.exclusiveGroup("settings", "all")

	case "list":
		flags.BoolVarP(&bl, "all", "a", false,
			"Show all running Kathara devices of all users. MUST BE ROOT FOR THIS OPTION.")
		flags.BoolVarP(&bl, "watch", "w", false, "Watch mode.")
		flags.BoolVarP(&bl, "live", "l", false, "Watch mode.")
		_ = flags.MarkHidden("live")
		p.alias("live", "watch")
		p.names("watch", "-w", "-l", "--watch", "--live")
		flags.StringVarP(&s, "name", "n", "", "Show only information about a specified device.")
		p.meta("name", "DEVICE_NAME")

	case "linfo":
		flags.StringVarP(&s, "directory", "d", "", "Specify the folder containing the network scenario.")
		p.meta("directory", "DIRECTORY")
		flags.BoolVarP(&bl, "watch", "w", false,
			"Watch mode, can be used only when a network scenario is launched.")
		flags.BoolVarP(&bl, "live", "l", false,
			"Watch mode, can be used only when a network scenario is launched.")
		_ = flags.MarkHidden("live")
		p.alias("live", "watch")
		p.names("watch", "-w", "-l", "--watch", "--live")
		flags.BoolVarP(&bl, "conf", "c", false, "Read static information from lab.conf.")
		p.exclusiveGroup("watch", "conf")
		flags.StringVarP(&s, "name", "n", "", "Show only information about a specified device.")
		p.meta("name", "DEVICE_NAME")
		flags.BoolVarP(&bl, "topology", "t", false, "Get running topology info")
		p.exclusiveGroup("name", "topology")

	case "lclean":
		flags.StringVarP(&s, "directory", "d", "", "Specify the folder containing the network scenario.")
		p.meta("directory", "DIRECTORY")
		bindList(p, sl(), "exclude", "", "DEVICE_NAME", "Exclude specified devices from clean.", greedyOneOrMore)
		p.pos("DEVICE_NAME", nargsZeroOrMore, "Clean only specified devices.")

	case "lconfig", "vconfig":
		if name == "lconfig" {
			flags.StringVarP(&s, "directory", "d", "",
				"Path of the network scenario to configure, if not specified the current path is used")
			p.meta("directory", "LAB_PATH")
			flags.StringVarP(&s, "name", "n", "", "Name of the device to configure.")
		} else {
			flags.StringVarP(&s, "name", "n", "",
				"Name of the device to be connected on desired collision domains.")
		}
		p.meta("name", "DEVICE_NAME")
		p.require("name")
		bindList(p, sl(), "add", "", "CD/MAC", "Specify the collision domain to add.", greedyOneOrMore)
		bindList(p, sl(), "rm", "", "CD", "Specify the collision domain to remove.", greedyOneOrMore)
		p.requiredGroup("add", "rm")

	case "connect":
		flags.StringVarP(&s, "directory", "d", "", "Specify the folder containing the network scenario.")
		p.meta("directory", "DIRECTORY")
		flags.BoolVarP(&bl, "vmachine", "v", false, "The device has been started with vstart command.")
		p.exclusiveGroup("directory", "vmachine")
		flags.StringVar(&s, "shell", "", "Shell that should be used inside the device.")
		p.meta("shell", "SHELL")
		flags.BoolVarP(&bl, "logs", "l", false, "Print device startup logs before launching the shell.")
		p.pos("DEVICE_NAME", nargsOne, "Name of the device to connect to.")

	case "exec":
		flags.StringVarP(&s, "directory", "d", "", "Specify the folder containing the network scenario.")
		p.meta("directory", "DIRECTORY")
		flags.BoolVarP(&bl, "vmachine", "v", false, "The device has been started with vstart command.")
		p.exclusiveGroup("directory", "vmachine")
		flags.BoolVar(&bl, "no-stdout", false, "Disable stdout of the executed command.")
		flags.BoolVar(&bl, "no-stderr", false, "Disable stderr of the executed command.")
		flags.BoolVar(&bl, "wait", false, "Wait until startup commands execution finishes.")
		p.pos("DEVICE_NAME", nargsOne, "Name of the device to execute the command into.")
		p.pos("COMMAND", nargsOneOrMore, "Shell command that will be executed inside the device.")

	case "lstart", "lrestart":
		restart := name == "lrestart"
		bindConst(flags, ts(), "noterminals", "", "Start the network scenario without opening terminal windows.")
		bindConst(flags, ts(), "terminals", "", "Start the network scenario opening terminal windows.")
		p.exclusiveGroup("noterminals", "terminals")
		bindConst(flags, ts(), "privileged", "",
			"Start the devices in privileged mode. MUST BE ROOT FOR THIS OPTION.")
		flags.StringVarP(&s, "directory", "d", "", "Specify the folder containing the network scenario.")
		p.meta("directory", "DIRECTORY")
		flags.BoolVarP(&bl, "force-lab", "F", false,
			"Force the network scenario to start without a lab.conf or lab.dep file.")
		flags.BoolVarP(&bl, "list", "l", false,
			"Show information about running devices after the network scenario has been started.")
		bindList(p, sl(), "pass", "o", "METADATA",
			"Apply metadata to all devices of a network scenario during startup.", greedyZeroOrMore)
		if restart {
			flags.StringVar(&s, "xterm", "", "Set a different terminal emulator application (Unix only).")
			p.meta("xterm", "XTERM")
		} else {
			flags.StringVar(&s, "terminal-emu", "", "Set a different terminal emulator application (Unix only).")
			p.meta("terminal-emu", "TERMINAL_EMU")
			flags.BoolVar(&bl, "print", false, "Open the lab.conf file and check if it is correct (dry run).")
			flags.BoolVar(&bl, "dry-mode", false, "Open the lab.conf file and check if it is correct (dry run).")
			p.alias("dry-mode", "print")
			p.names("print", "--print", "--dry-mode")
		}
		bindConst(flags, ts(), "no-hosthome", "H", `Do not mount "/hosthome" directory inside devices.`)
		p.names("no-hosthome", "--no-hosthome", "-H")
		bindConst(flags, ts(), "hosthome", "", `Mount "/hosthome" directory inside devices.`)
		p.exclusiveGroup("no-hosthome", "hosthome")
		bindConst(flags, ts(), "no-shared", "S", `Do not mount "/shared" directory inside devices.`)
		p.names("no-shared", "--no-shared", "-S")
		bindConst(flags, ts(), "shared", "", `Mount "/shared" directory inside devices.`)
		p.exclusiveGroup("no-shared", "shared")
		if restart {
			bindList(p, sl(), "exclude", "", "DEVICE_NAME", "Exclude specified devices.", greedyOneOrMore)
			p.pos("DEVICE_NAME", nargsZeroOrMore, "Restarts only specified devices.")
		} else {
			bindList(p, sl(), "exclude", "", "DEVICE_NAME", "Exclude specified devices from startup.", greedyOneOrMore)
			p.pos("DEVICE_NAME", nargsZeroOrMore, "Launches only specified devices.")
		}

	case "vclean":
		flags.StringVarP(&s, "name", "n", "", "The name of the device to clean.")
		p.meta("name", "DEVICE_NAME")
		p.require("name")

	case "vstart":
		bindConst(flags, ts(), "noterminals", "", "Start the device without opening a terminal window.")
		bindConst(flags, ts(), "terminals", "", "Start the device opening its terminal window.")
		flags.StringVar(&s, "num_terms", "", "Choose the number of terminals to open for the device.")
		p.meta("num_terms", "NUM_TERMS")
		p.exclusiveGroup("noterminals", "terminals", "num_terms")
		bindConst(flags, ts(), "privileged", "",
			"Start the device in privileged mode. MUST BE ROOT FOR THIS OPTION.")
		flags.StringVarP(&s, "name", "n", "", "Name of the device to be started.")
		p.meta("name", "DEVICE_NAME")
		p.require("name")
		bindList(p, sl(), "eth", "", "N:CD/MAC", "Set a specific interface on a collision domain.", greedyOneOrMore)
		bindList(p, sl(), "exec", "e", "EXEC_COMMANDS",
			"Execute a specific command in the device during startup.", greedyZeroOrMore)
		flags.StringVar(&s, "mem", "", "Limit the amount of RAM available for this device.")
		p.meta("mem", "MEM")
		flags.StringVar(&s, "cpus", "", "Limit the amount of CPU available for this device.")
		p.meta("cpus", "CPUS")
		flags.StringVarP(&s, "image", "i", "", "Run this device with a specific Docker Image.")
		p.meta("image", "IMAGE")
		bindConst(flags, ts(), "no-hosthome", "H", `Do not mount "/hosthome" directory inside the device.`)
		p.names("no-hosthome", "--no-hosthome", "-H")
		bindConst(flags, ts(), "hosthome", "", `Mount "/hosthome" directory inside the device.`)
		p.exclusiveGroup("no-hosthome", "hosthome")
		flags.StringVar(&s, "terminal-emu", "", "Set a different terminal emulator application (Unix only).")
		p.meta("terminal-emu", "TERMINAL_EMU")
		flags.BoolVar(&bl, "print", false, "Check if the device parameters are correct (dry run).")
		flags.BoolVar(&bl, "dry-run", false, "Check if the device parameters are correct (dry run).")
		p.alias("dry-run", "print")
		p.names("print", "--print", "--dry-run")
		flags.BoolVar(&bl, "bridged", false, "Add a bridge interface to the device.")
		bindList(p, sl(), "port", "", "[HOST:]GUEST[/PROTOCOL]",
			"Map localhost port HOST to the internal port GUEST of the device for the specified PROTOCOL.",
			greedyOneOrMore)
		bindList(p, sl(), "sysctl", "", "SYSCTL", "Set sysctl option for the device.", greedyOneOrMore)
		bindList(p, sl(), "env", "", "ENV", "Set environment variable for the device.", greedyOneOrMore)
		bindList(p, sl(), "ulimit", "", "KEY=SOFT[:HARD]", "Set ulimit for the device.", greedyOneOrMore)
		bindList(p, sl(), "volume", "", "HOST|GUEST|[MODE]", "Specify a volume to mount.", greedyOneOrMore)
		flags.StringVar(&s, "shell", "",
			"Set the shell (sh, bash, etc.) that should be used inside the device.")
		p.meta("shell", "SHELL")
		flags.StringVar(&s, "entrypoint", "", "Specify the entrypoint command of the device.")
		p.meta("entrypoint", "ENTRYPOINT")
		p.pos("ARG", nargsRemainder, "Specify extra arguments for the entrypoint command.")

	default:
		t.Fatalf("no oracle parser for %q", name)
	}
	return p
}

// TestUsageCarriesNoSentinel is the `nargs='*'` leak: pflag's own FlagUsages
// interpolates a flag's NoOptDefVal, and [emptyListSentinel] carries two NUL
// bytes. Nothing the port prints may contain it.
func TestUsageCarriesNoSentinel(t *testing.T) {
	a := newTestApp(t)
	for name, spec := range commandTable(a.app) {
		text := spec.Cmd.usage()
		if strings.Contains(text, "\x00") || strings.Contains(text, "kathara-empty-list") {
			t.Errorf("%s help leaks the empty-list sentinel:\n%s", name, text)
		}
		for _, forbidden := range []string{"stringList", "(default ", "unset]"} {
			if strings.Contains(text, forbidden) {
				t.Errorf("%s help contains pflag noise %q:\n%s", name, forbidden, text)
			}
		}
	}
}
