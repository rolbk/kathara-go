package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
)

// pinnedKatharaConf is tools/goldenharness/runner.go's settings file.
const pinnedKatharaConf = `{
 "image": "kathara/base",
 "manager_type": "docker",
 "terminal": "/usr/bin/xterm",
 "open_terminals": true,
 "device_shell": "/bin/bash",
 "net_prefix": "kathara",
 "device_prefix": "kathara",
 "debug_level": "INFO",
 "print_startup_log": true,
 "enable_ipv6": false,
 "volume_mount_policy": "Always",
 "last_checked": 4102444800.0,
 "hosthome_mount": false,
 "shared_mount": true,
 "image_update_policy": "Never",
 "shared_cds": 1,
 "remote_url": null,
 "cert_path": null,
 "network_plugin": "kathara/katharanp_vde"
}
`

const stepTimeout = 180 * time.Second

// StepRecord is one recorded invocation.
type StepRecord struct {
	Name   string       `json:"name"`
	Argv   []string     `json:"argv"`
	Dir    string       `json:"dir"`
	Exit   int          `json:"exit"`
	Stdout []string     `json:"stdout"`
	Stderr []string     `json:"stderr"`
	Docker *DockerState `json:"docker,omitempty"`
}

// FlowRecord is one full pass of a flow by one implementation.
type FlowRecord struct {
	Flow  string       `json:"flow"`
	Impl  string       `json:"impl"`
	Run   int          `json:"run"`
	Steps []StepRecord `json:"steps"`
}

type impl struct {
	Name string
	Argv []string
}

func main() {
	var (
		record   = flag.Bool("record", false, "record flows")
		compare  = flag.Bool("compare", false, "compare recordings on disk")
		flowName = flag.String("flow", "", "restrict to one flow")
		outDir   = flag.String("out", "", "recording directory (default <tool>/recordings)")
		goBin    = flag.String("go-bin", "/tmp/kgo", "the Go binary under test")
		pyBin    = flag.String("py-bin", "/root/kathara/pyvenv/bin/python", "the Python interpreter of the oracle")
		scratch  = flag.String("scratch", "/tmp/cmdparity", "scratch root for scenario directories")
		runs     = flag.Int("runs", 2, "runs per implementation")
		columns  = flag.String("columns", consoleWidth, "COLUMNS the binaries under test run with")
	)
	flag.Parse()
	consoleWidth = *columns

	if !*record && !*compare {
		*record, *compare = true, true
	}
	root, err := toolRoot()
	if err != nil {
		fatal(err)
	}
	if *outDir == "" {
		*outDir = filepath.Join(root, "recordings")
	}

	selected := flows
	if *flowName != "" {
		selected = nil
		for _, f := range flows {
			if f.Name == *flowName {
				selected = append(selected, f)
			}
		}
		if len(selected) == 0 {
			fatal(fmt.Errorf("unknown flow %q", *flowName))
		}
	}

	if *record {
		impls := []impl{
			{Name: "py", Argv: []string{*pyBin, "-m", "kathara"}},
			{Name: "go", Argv: []string{*goBin}},
		}
		restore, err := pinSettings()
		if err != nil {
			fatal(err)
		}
		defer restore()
		if err := recordAll(selected, impls, *runs, root, *outDir, *scratch); err != nil {
			restore()
			fatal(err)
		}
		restore()
	}

	if *compare {
		if err := compareAll(selected, *outDir, *runs); err != nil {
			fatal(err)
		}
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "cmdparity:", err)
	os.Exit(1)
}

func toolRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for dir := wd; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "flows.go")); err == nil {
			return dir, nil
		}
		if dir == filepath.Dir(dir) {
			return wd, nil
		}
	}
}

// pinSettings installs the pinned kathara.conf at the path both implementations
// actually read and returns a restore function.
func pinSettings() (func(), error) {
	home, err := passwdHome()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(home, ".config", "kathara.conf")
	prev, readErr := os.ReadFile(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(pinnedKatharaConf), 0o644); err != nil {
		return nil, err
	}
	return func() {
		if readErr != nil {
			_ = os.Remove(path)
			return
		}
		_ = os.WriteFile(path, prev, 0o644)
	}, nil
}

// passwdHome is utils.get_current_user_home on Linux: the passwd entry, not
// $HOME.
func passwdHome() (string, error) {
	out, err := exec.Command("getent", "passwd", fmt.Sprint(os.Getuid())).Output()
	if err == nil {
		fields := strings.Split(strings.TrimSpace(string(out)), ":")
		if len(fields) >= 6 && fields[5] != "" {
			return fields[5], nil
		}
	}
	return os.UserHomeDir()
}

func recordAll(selected []Flow, impls []impl, runs int, root, outDir, scratch string) error {
	docker := NewDocker("")
	ctx := context.Background()

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	for _, f := range selected {
		for _, im := range impls {
			for run := 1; run <= runs; run++ {
				fmt.Fprintf(os.Stderr, "== %s / %s / run %d\n", f.Name, im.Name, run)
				rec, err := recordFlow(ctx, f, im, run, root, scratch, docker)
				if err != nil {
					_ = docker.ForceCleanup(ctx)
					return fmt.Errorf("%s/%s/run%d: %w", f.Name, im.Name, run, err)
				}
				path := filepath.Join(outDir, fmt.Sprintf("%s.%s.run%d.json", f.Name, im.Name, run))
				blob, err := json.MarshalIndent(rec, "", "  ")
				if err != nil {
					return err
				}
				if err := os.WriteFile(path, append(blob, '\n'), 0o644); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func recordFlow(ctx context.Context, f Flow, im impl, run int, root, scratch string, docker *Docker) (rec *FlowRecord, err error) {
	if err := docker.ForceCleanup(ctx); err != nil {
		return nil, fmt.Errorf("pre-flow cleanup: %w", err)
	}
	defer func() {
		cctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
		defer cancel()
		if cerr := docker.ForceCleanup(cctx); cerr != nil && err == nil {
			err = fmt.Errorf("post-flow cleanup: %w", cerr)
		}
	}()

	// A fixed scratch path, recreated per run, so that the path-derived lab
	// hash is identical between implementations even before normalization.
	flowRoot := filepath.Join(scratch, f.Name)
	if err := os.RemoveAll(flowRoot); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(flowRoot, 0o755); err != nil {
		return nil, err
	}
	labDir := flowRoot
	if f.Fixture != "" {
		labDir = filepath.Join(flowRoot, "lab")
		if err := copyTree(filepath.Join(root, "testdata", f.Fixture), labDir); err != nil {
			return nil, err
		}
	}
	home := filepath.Join(flowRoot, "home")
	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(home, ".config", "kathara.conf"), []byte(pinnedKatharaConf), 0o644); err != nil {
		return nil, err
	}

	n := NewNormalizer()
	realFlowRoot := realPath(flowRoot)
	realLabDir := realPath(labDir)
	n.AddLiteral(realLabDir, TokLabDir)
	n.AddLiteral(labDir, TokLabDir)
	n.AddLiteral(realFlowRoot, TokLabDir)
	n.AddLiteral(flowRoot, TokLabDir)
	n.AddLiteral(home, TokHome)
	n.AddLiteral(UserSlug(), TokUser)
	n.AddLiteral(GenerateURLSafeHash("kathara_vlab"), TokVlabHash)
	n.AddLiteral(labHash(labDir, realLabDir), TokLabHash)
	// The oracle's argv is three tokens, this implementation's one; both become <KATHARA>.
	n.AddLiteral(strings.Join(im.Argv, " "), TokKathara)
	// Every MAC the flow itself spells out is deterministic by construction.
	for _, st := range f.Steps {
		for _, a := range st.Args {
			for _, m := range reMAC.FindAllString(a, -1) {
				n.KeepMAC(m)
			}
		}
	}

	rec = &FlowRecord{Flow: f.Name, Impl: im.Name, Run: run}
	for _, st := range f.Steps {
		dir := flowRoot
		if st.Dir == "lab" {
			dir = labDir
		}
		argv := append(append([]string(nil), im.Argv...), st.Args...)
		sctx, cancel := context.WithTimeout(ctx, stepTimeout)
		res := runProcess(sctx, dir, home, argv)
		cancel()

		out := StepRecord{
			Name:   st.Name,
			Argv:   append([]string{TokKathara}, st.Args...),
			Dir:    st.Dir,
			Exit:   res.ExitCode,
			Stdout: n.Lines(res.Stdout),
			Stderr: n.Lines(res.Stderr),
		}
		if res.TimedOut {
			out.Stderr = append(out.Stderr, "<TIMEOUT>")
		}
		if !st.NoDocker {
			snap, serr := docker.Snapshot(ctx, n)
			if serr != nil {
				return nil, fmt.Errorf("snapshot after %s: %w", st.Name, serr)
			}
			out.Docker = snap
		}
		rec.Steps = append(rec.Steps, out)
	}
	return rec, nil
}

// labHash re-derives Kathara's lab hash: the urlsafe md5 of LAB_NAME when the
// scenario declares one, of the realpath otherwise.
func labHash(labDir, real string) string {
	raw, err := os.ReadFile(filepath.Join(labDir, "lab.conf"))
	if err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "LAB_NAME") {
				continue
			}
			_, v, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			v = strings.TrimSpace(strings.NewReplacer(`"`, "", "'", "").Replace(v))
			if v != "" {
				return GenerateURLSafeHash(v)
			}
		}
	}
	return GenerateURLSafeHash(real)
}

func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
}

// --- comparison ------------------------------------------------------------

func compareAll(selected []Flow, outDir string, runs int) error {
	var problems int
	for _, f := range selected {
		// Determinism: run 1 vs run 2 of the same implementation.
		for _, im := range []string{"py", "go"} {
			for r := 2; r <= runs; r++ {
				a, err := load(outDir, f.Name, im, 1)
				if err != nil {
					return err
				}
				b, err := load(outDir, f.Name, im, r)
				if err != nil {
					return err
				}
				problems += report(fmt.Sprintf("DETERMINISM %s/%s run1 vs run%d", f.Name, im, r), a, b)
			}
		}
		// Parity: python run N vs go run N.
		for r := 1; r <= runs; r++ {
			py, err := load(outDir, f.Name, "py", r)
			if err != nil {
				return err
			}
			goRec, err := load(outDir, f.Name, "go", r)
			if err != nil {
				return err
			}
			problems += report(fmt.Sprintf("PARITY %s run%d py vs go", f.Name, r), py, goRec)
		}
	}
	fmt.Printf("\n%d difference block(s)\n", problems)
	return nil
}

func load(outDir, flow, im string, run int) (*FlowRecord, error) {
	path := filepath.Join(outDir, fmt.Sprintf("%s.%s.run%d.json", flow, im, run))
	blob, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rec FlowRecord
	if err := json.Unmarshal(blob, &rec); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return &rec, nil
}

func report(label string, a, b *FlowRecord) int {
	var blocks int
	emit := func(step, field string, av, bv any) {
		blocks++
		fmt.Printf("\n### %s :: %s :: %s\n", label, step, field)
		fmt.Printf("  A(%s run%d): %s\n", a.Impl, a.Run, render(av))
		fmt.Printf("  B(%s run%d): %s\n", b.Impl, b.Run, render(bv))
	}
	n := len(a.Steps)
	if len(b.Steps) < n {
		n = len(b.Steps)
	}
	for i := 0; i < n; i++ {
		sa, sb := a.Steps[i], b.Steps[i]
		if sa.Name != sb.Name {
			emit(sa.Name, "step-name", sa.Name, sb.Name)
			continue
		}
		if sa.Exit != sb.Exit {
			emit(sa.Name, "exit", sa.Exit, sb.Exit)
		}
		if !reflect.DeepEqual(sa.Stdout, sb.Stdout) {
			emit(sa.Name, "stdout", sa.Stdout, sb.Stdout)
		}
		if !reflect.DeepEqual(sa.Stderr, sb.Stderr) {
			emit(sa.Name, "stderr", sa.Stderr, sb.Stderr)
		}
		if (sa.Docker == nil) != (sb.Docker == nil) {
			emit(sa.Name, "docker-presence", sa.Docker != nil, sb.Docker != nil)
			continue
		}
		if sa.Docker != nil {
			for _, d := range diffDocker(sa.Docker, sb.Docker) {
				emit(sa.Name, "docker:"+d.field, d.a, d.b)
			}
		}
	}
	return blocks
}

type dockerDiff struct {
	field string
	a, b  any
}

func diffDocker(a, b *DockerState) []dockerDiff {
	var out []dockerDiff
	if a.DanglingVolumes != b.DanglingVolumes {
		out = append(out, dockerDiff{"dangling_volumes", a.DanglingVolumes, b.DanglingVolumes})
	}
	an, bn := containerNames(a), containerNames(b)
	if !reflect.DeepEqual(an, bn) {
		out = append(out, dockerDiff{"container-set", an, bn})
	} else {
		for i := range a.Containers {
			ca, cb := a.Containers[i], b.Containers[i]
			for _, f := range structFields(ca, cb) {
				out = append(out, dockerDiff{"container[" + ca.Name + "]." + f.name, f.a, f.b})
			}
		}
	}
	anw, bnw := networkNames(a), networkNames(b)
	if !reflect.DeepEqual(anw, bnw) {
		out = append(out, dockerDiff{"network-set", anw, bnw})
	} else {
		for i := range a.Networks {
			na, nb := a.Networks[i], b.Networks[i]
			for _, f := range structFields(na, nb) {
				out = append(out, dockerDiff{"network[" + na.Name + "]." + f.name, f.a, f.b})
			}
		}
	}
	return out
}

type fieldDiff struct {
	name string
	a, b any
}

func structFields(a, b any) []fieldDiff {
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	t := va.Type()
	var out []fieldDiff
	for i := 0; i < t.NumField(); i++ {
		fa, fb := va.Field(i).Interface(), vb.Field(i).Interface()
		if !reflect.DeepEqual(fa, fb) {
			out = append(out, fieldDiff{t.Field(i).Name, fa, fb})
		}
	}
	return out
}

func containerNames(s *DockerState) []string {
	out := make([]string, 0, len(s.Containers))
	for _, c := range s.Containers {
		out = append(out, c.Name)
	}
	sort.Strings(out)
	return out
}

func networkNames(s *DockerState) []string {
	out := make([]string, 0, len(s.Networks))
	for _, n := range s.Networks {
		out = append(out, n.Name)
	}
	sort.Strings(out)
	return out
}

func render(v any) string {
	blob, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	s := string(blob)
	if lines, ok := v.([]string); ok {
		return "\n    " + strings.Join(lines, "\n    ")
	}
	return s
}
