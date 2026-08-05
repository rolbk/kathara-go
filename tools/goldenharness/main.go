// Command goldenharness records and verifies the Layer A CLI golden snapshots
// described in PORT_SPEC.md §9.
//
// It never imports Kathara. It drives whatever binary KATHARA_CMD names — the
// Python 3.8.3 oracle today, the Go build tomorrow — over the scenario list in
// scenarios.yaml, and snapshots the observable state of each run as a tree of
// canonical JSON files under test/goldens/<scenario>/.
//
// Subcommands:
//
//	record   drive the binary under test and (over)write the stored snapshots
//	verify   re-record into a scratch tree and diff against the stored snapshots
//	diff     compare two snapshot trees that already exist on disk
//	list     print the resolved scenario list
//
// Every normalization applied to a recording is documented, with its
// nondeterminism source, in NORMALIZATION.md.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

const defaultKatharaCmd = "/root/kathara/pyvenv/bin/python -m kathara"

type globalOpts struct {
	manifest  string
	goldens   string
	repoRoot  string
	labsRoot  string
	scenarios stringList
	verbose   bool
	maxDiff   int
}

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			*s = append(*s, part)
		}
	}
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "goldenharness: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return errors.New("missing subcommand")
	}
	switch args[0] {
	case "record":
		return cmdRecord(args[1:])
	case "verify":
		return cmdVerify(args[1:])
	case "diff":
		return cmdDiff(args[1:])
	case "list":
		return cmdList(args[1:])
	case "-h", "--help", "help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `goldenharness <record|verify|diff|list> [flags]

  record   run the scenarios and write test/goldens/<name>/
  verify   re-record into a scratch tree and diff against test/goldens/
  diff     compare two snapshot trees already on disk
  list     print the resolved scenario list

Environment:
  KATHARA_CMD  binary under test (default `+defaultKatharaCmd+`)
  DOCKER_BIN   docker CLI to use (default "docker")
`)
}

func addGlobalFlags(fs *flag.FlagSet, o *globalOpts) {
	fs.StringVar(&o.manifest, "manifest", "", "scenario manifest (default <repo>/tools/goldenharness/scenarios.yaml)")
	fs.StringVar(&o.goldens, "goldens", "", "golden snapshot root (default <repo>/test/goldens)")
	fs.StringVar(&o.repoRoot, "repo", "", "repository root (default: nearest ancestor with go.mod)")
	fs.StringVar(&o.labsRoot, "labs-root", os.Getenv("KATHARA_LABS_ROOT"), "root of the Kathara-Labs checkout")
	fs.Var(&o.scenarios, "scenario", "scenario name pattern, repeatable or comma-separated")
	fs.BoolVar(&o.verbose, "v", false, "verbose progress on stderr")
	fs.IntVar(&o.maxDiff, "max-diff-lines", 40, "maximum diff lines printed per side per file")
}

func (o *globalOpts) resolve() error {
	if o.repoRoot == "" {
		root, err := findRepoRoot()
		if err != nil {
			return err
		}
		o.repoRoot = root
	}
	if o.manifest == "" {
		o.manifest = filepath.Join(o.repoRoot, "tools", "goldenharness", "scenarios.yaml")
	}
	if o.goldens == "" {
		o.goldens = filepath.Join(o.repoRoot, "test", "goldens")
	}
	return nil
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("could not locate repository root (no go.mod in any ancestor); pass -repo")
		}
		dir = parent
	}
}

func katharaArgv() []string {
	cmd := os.Getenv("KATHARA_CMD")
	if strings.TrimSpace(cmd) == "" {
		cmd = defaultKatharaCmd
	}
	return strings.Fields(cmd)
}

func newRunner(o *globalOpts) *Runner {
	return &Runner{
		RepoRoot:    o.repoRoot,
		KatharaArgv: katharaArgv(),
		Docker:      NewDocker(os.Getenv("DOCKER_BIN")),
		Verbose:     o.verbose,
	}
}

// signalContext cancels on SIGINT/SIGTERM so a Ctrl-C still runs the per
// scenario cleanup path instead of orphaning containers.
func signalContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-ch:
			cancel()
		case <-ctx.Done():
		}
		signal.Stop(ch)
	}()
	return ctx, cancel
}

func cmdList(args []string) error {
	var o globalOpts
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	addGlobalFlags(fs, &o)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := o.resolve(); err != nil {
		return err
	}
	m, err := LoadManifest(o.manifest, o.repoRoot, o.labsRoot)
	if err != nil {
		return err
	}
	sel, err := m.Select(o.scenarios)
	if err != nil {
		return err
	}
	for _, s := range sel {
		missing := ""
		if _, err := os.Stat(s.LabDir()); err != nil {
			missing = "  [MISSING DIR]"
		}
		fmt.Printf("%-28s %-7s %-9s %s%s\n", s.Name, s.Kind, s.Status, s.LabDir(), missing)
	}
	fmt.Printf("\n%d scenario(s)\n", len(sel))
	return nil
}

// recordInto records the selected scenarios into root/<name>/ and returns the
// per-scenario results in manifest order.
func recordInto(ctx context.Context, o *globalOpts, root string, sel []Scenario) ([]ScenarioResult, error) {
	r := newRunner(o)
	cleanupHome, err := r.SetupHome()
	if err != nil {
		return nil, err
	}
	defer cleanupHome()
	results := make([]ScenarioResult, 0, len(sel))

	for i, sc := range sel {
		if ctx.Err() != nil {
			return results, fmt.Errorf("interrupted after %d scenario(s)", i)
		}
		if o.verbose {
			fmt.Fprintf(os.Stderr, "[%d/%d] %s\n", i+1, len(sel), sc.Name)
		}
		start := time.Now()
		// Scenarios run strictly one at a time: they share one Docker daemon
		// and one lab-hash namespace.
		snap, err := r.Record(ctx, sc)
		res := ScenarioResult{Name: sc.Name, Snapshot: snap, Err: err, Duration: time.Since(start)}
		if snap != nil {
			if werr := snap.Write(filepath.Join(root, sc.Name)); werr != nil {
				if res.Err == nil {
					res.Err = werr
				} else {
					res.Err = fmt.Errorf("%w; additionally: %v", res.Err, werr)
				}
			}
		}
		results = append(results, res)
		if o.verbose {
			status := "ok"
			if res.Err != nil {
				status = "FAILED: " + res.Err.Error()
			}
			fmt.Fprintf(os.Stderr, "      %s in %s (%s)\n", sc.Name, res.Duration.Round(time.Millisecond), status)
		}
	}
	return results, nil
}

func cmdRecord(args []string) error {
	var o globalOpts
	fs := flag.NewFlagSet("record", flag.ContinueOnError)
	addGlobalFlags(fs, &o)
	keepGoing := fs.Bool("keep-going", true, "continue recording after a scenario fails")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := o.resolve(); err != nil {
		return err
	}
	m, err := LoadManifest(o.manifest, o.repoRoot, o.labsRoot)
	if err != nil {
		return err
	}
	sel, err := m.Select(o.scenarios)
	if err != nil {
		return err
	}

	ctx, cancel := signalContext()
	defer cancel()

	results, rerr := recordInto(ctx, &o, o.goldens, sel)
	var failed []string
	for _, res := range results {
		if res.Err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", res.Name, res.Err))
			if !*keepGoing {
				break
			}
		}
	}
	fmt.Printf("recorded %d/%d scenario(s) into %s\n", len(results)-len(failed), len(results), o.goldens)
	if rerr != nil {
		return rerr
	}
	if len(failed) > 0 {
		sort.Strings(failed)
		return fmt.Errorf("%d scenario(s) failed:\n  %s", len(failed), strings.Join(failed, "\n  "))
	}
	return nil
}

func cmdVerify(args []string) error {
	var o globalOpts
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	addGlobalFlags(fs, &o)
	out := fs.String("out", "", "scratch directory for the fresh recording (default: a temp dir)")
	keep := fs.Bool("keep", false, "keep the scratch recording after verifying")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := o.resolve(); err != nil {
		return err
	}
	m, err := LoadManifest(o.manifest, o.repoRoot, o.labsRoot)
	if err != nil {
		return err
	}
	sel, err := m.Select(o.scenarios)
	if err != nil {
		return err
	}

	scratch := *out
	if scratch == "" {
		scratch, err = os.MkdirTemp("", "goldenharness-verify-")
		if err != nil {
			return fmt.Errorf("create scratch dir: %w", err)
		}
		if !*keep {
			defer func() { _ = os.RemoveAll(scratch) }()
		}
	}

	ctx, cancel := signalContext()
	defer cancel()

	results, rerr := recordInto(ctx, &o, scratch, sel)

	statusOf := map[string]string{}
	for _, s := range sel {
		statusOf[s.Name] = s.Status
	}

	var report strings.Builder
	var mismatched, referenceDrift, errored []string

	for _, res := range results {
		if res.Err != nil {
			errored = append(errored, fmt.Sprintf("%s: %v", res.Name, res.Err))
		}
		d, cerr := CompareDirs(res.Name,
			filepath.Join(o.goldens, res.Name), filepath.Join(scratch, res.Name), o.maxDiff)
		if cerr != nil {
			errored = append(errored, fmt.Sprintf("%s: compare: %v", res.Name, cerr))
			continue
		}
		if d.Empty() {
			continue
		}
		PrintDiff(d, &report)
		if statusOf[res.Name] == StatusReference {
			referenceDrift = append(referenceDrift, res.Name)
		} else {
			mismatched = append(mismatched, res.Name)
		}
	}

	if report.Len() > 0 {
		fmt.Print(report.String())
	}
	fmt.Printf("\nverified %d scenario(s): %d mismatch(es), %d reference drift(s), %d error(s)\n",
		len(results), len(mismatched), len(referenceDrift), len(errored))
	if len(referenceDrift) > 0 {
		sort.Strings(referenceDrift)
		fmt.Printf("reference-status scenarios that drifted (not failures): %s\n", strings.Join(referenceDrift, ", "))
	}
	if *keep || *out != "" {
		fmt.Printf("fresh recording kept at %s\n", scratch)
	}
	if rerr != nil {
		return rerr
	}
	if len(mismatched) > 0 || len(errored) > 0 {
		sort.Strings(mismatched)
		var parts []string
		if len(mismatched) > 0 {
			parts = append(parts, "mismatched: "+strings.Join(mismatched, ", "))
		}
		if len(errored) > 0 {
			sort.Strings(errored)
			parts = append(parts, "errors:\n  "+strings.Join(errored, "\n  "))
		}
		return errors.New(strings.Join(parts, "; "))
	}
	return nil
}

func cmdDiff(args []string) error {
	var o globalOpts
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	addGlobalFlags(fs, &o)
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 2 {
		return errors.New("usage: goldenharness diff [flags] <want-dir> <got-dir>")
	}
	wantRoot, gotRoot := rest[0], rest[1]

	names := map[string]bool{}
	for _, root := range []string{wantRoot, gotRoot} {
		entries, err := os.ReadDir(root)
		if err != nil {
			return fmt.Errorf("read %s: %w", root, err)
		}
		for _, e := range entries {
			if e.IsDir() {
				names[e.Name()] = true
			}
		}
	}
	ordered := make([]string, 0, len(names))
	for n := range names {
		ordered = append(ordered, n)
	}
	sort.Strings(ordered)

	var report strings.Builder
	var mismatched []string
	for _, n := range ordered {
		if len(o.scenarios) > 0 {
			match := false
			for _, p := range o.scenarios {
				if ok, err := filepath.Match(p, n); err == nil && ok {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}
		d, err := CompareDirs(n, filepath.Join(wantRoot, n), filepath.Join(gotRoot, n), o.maxDiff)
		if err != nil {
			return err
		}
		if d.Empty() {
			continue
		}
		PrintDiff(d, &report)
		mismatched = append(mismatched, n)
	}
	if report.Len() > 0 {
		fmt.Print(report.String())
	}
	fmt.Printf("\ncompared %d scenario(s): %d mismatch(es)\n", len(ordered), len(mismatched))
	if len(mismatched) > 0 {
		return fmt.Errorf("mismatched: %s", strings.Join(mismatched, ", "))
	}
	return nil
}
