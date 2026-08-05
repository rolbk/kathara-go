package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// cleanupBudget is the wall clock granted to lclean and to the forced cleanup
// that follows a failed or timed-out scenario. It is independent of the
// scenario timeout so that a hung lstart still gets torn down.
const cleanupBudget = 150 * time.Second

// TokKathara stands in for the binary under test so that a snapshot recorded
// from the Python oracle can be replayed against the Go build unchanged.
const TokKathara = "<KATHARA>"

// Runner records one scenario at a time.
type Runner struct {
	RepoRoot    string
	KatharaArgv []string
	Docker      *Docker
	Verbose     bool
	// HomeDir is the harness-owned HOME the binary under test runs with. It
	// contains the pinned .config/kathara.conf, so a recording can depend
	// neither on the operator's mutable settings nor on the release / image
	// update checks those settings would allow to fire.
	HomeDir string
}

// pinnedKatharaConf is the settings file the binary under test sees. It is the
// stock 3.8.3 default configuration with two deliberate changes:
//   - image_update_policy "Never": under "Prompt" every lstart asks Docker Hub
//     for each image's digest, and an upstream push turns the run into a
//     confirmation prompt. The recording must not depend on registry state.
//   - last_checked far in the future: Setting.check() otherwise phones GitHub
//     for a release check once a week and prints a three-line banner when a
//     newer version exists, then rewrites the settings file.
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

// SetupHome creates the harness-owned HOME and writes the pinned settings
// file. The caller removes the directory when the harness exits.
func (r *Runner) SetupHome() (cleanup func(), err error) {
	home, err := os.MkdirTemp("", "goldenharness-home-")
	if err != nil {
		return nil, fmt.Errorf("create harness home: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(home) }
	confDir := filepath.Join(home, ".config")
	if err := os.MkdirAll(confDir, 0o755); err != nil {
		cleanup()
		return nil, fmt.Errorf("create %s: %w", confDir, err)
	}
	conf := filepath.Join(confDir, "kathara.conf")
	if err := os.WriteFile(conf, []byte(pinnedKatharaConf), 0o644); err != nil {
		cleanup()
		return nil, fmt.Errorf("write %s: %w", conf, err)
	}
	r.HomeDir = home
	return cleanup, nil
}

// ScenarioResult is the outcome of recording one scenario.
type ScenarioResult struct {
	Name     string
	Snapshot *Snapshot
	Err      error
	Duration time.Duration
}

// Record drives one scenario end to end and returns its snapshot. Cleanup runs
// on every path, including timeout and panic-free error returns.
func (r *Runner) Record(parent context.Context, sc Scenario) (snap *Snapshot, err error) {
	labDir := sc.LabDir()
	labDirReal := realPath(labDir)

	// Pre-flight: the host must be free of Kathara state before recording.
	// Anything left over would be attributed to this scenario.
	if err := r.assertPreflightClean(parent); err != nil {
		return nil, err
	}

	sharedPath := filepath.Join(labDir, "shared")
	sharedExistedBefore := pathExists(sharedPath)
	if err := resetFiles(labDir, sc.ResetFiles); err != nil {
		return nil, err
	}
	createdHostDirs, err := ensureHostDirs(sc.HostDirs)
	if err != nil {
		return nil, err
	}

	norm := NewNormalizer()
	norm.AddLiteral(labDirReal, TokLabDir)
	norm.AddLiteral(labDir, TokLabDir)
	if home, herr := os.UserHomeDir(); herr == nil {
		norm.AddLiteral(home, TokHome)
	}
	if r.HomeDir != "" {
		// The harness-owned HOME the binary under test runs with; tokenized
		// so its random temp-dir suffix can never reach a snapshot.
		norm.AddLiteral(r.HomeDir, TokHome)
	}

	expectedHash, hashSource := expectedLabHash(labDir, labDirReal)
	expectedUser := expectedUserSlug()
	norm.AddLiteral(expectedHash, TokLabHash)
	norm.AddLiteral(expectedUser, TokUser)

	type rawStep struct {
		step string
		args []string
		res  CmdResult
	}
	var steps []rawStep

	defer func() {
		// Always leave the host clean, whatever happened above.
		cctx, cancel := context.WithTimeout(context.Background(), cleanupBudget)
		defer cancel()
		if cerr := r.Docker.ForceCleanup(cctx); cerr != nil && err == nil {
			err = fmt.Errorf("post-scenario cleanup: %w", cerr)
		}
		if !sharedExistedBefore && pathExists(sharedPath) {
			if rerr := os.RemoveAll(sharedPath); rerr != nil && err == nil {
				err = fmt.Errorf("remove harness-created %s: %w", sharedPath, rerr)
			}
		}
		for _, dir := range createdHostDirs {
			if rerr := os.RemoveAll(dir); rerr != nil && err == nil {
				err = fmt.Errorf("remove harness-created %s: %w", dir, rerr)
			}
		}
	}()

	ctx, cancel := context.WithTimeout(parent, time.Duration(sc.TimeoutSecs())*time.Second)
	defer cancel()

	// Step 1: start the network scenario.
	startArgs := []string{"lstart", "--noterminals"}
	if sc.Kind == KindDry {
		startArgs = append(startArgs, "--print")
	}
	startArgs = append(startArgs, "-d", labDir)
	startArgs = append(startArgs, sc.Flags...)
	startRes := r.runKathara(ctx, startArgs)
	steps = append(steps, rawStep{step: "lstart", args: startArgs, res: startRes})
	if startRes.Err != nil {
		return nil, fmt.Errorf("scenario %s: lstart could not be executed: %w", sc.Name, startRes.Err)
	}

	// Step 1b: let the lab settle, for the scenarios that declare they need it.
	// Everything observed below — docker inspect and every in-container probe —
	// is therefore taken from the same side of the delay.
	if d := sc.SettleDelay(); d > 0 && startRes.Err == nil && !startRes.TimedOut {
		if r.Verbose {
			fmt.Fprintf(os.Stderr, "      %s settling for %s\n", sc.Name, d)
		}
		timer := time.NewTimer(d)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
		}
	}

	snap = &Snapshot{}
	var assertionFailures []string

	// Step 2: inspect the deployed state.
	var containerIDByDevice map[string]string
	if sc.Kind == KindDeploy && !startRes.TimedOut {
		ids, derr := r.Docker.IDs(ctx)
		if derr != nil {
			return nil, fmt.Errorf("scenario %s: list containers: %w", sc.Name, derr)
		}
		netIDs, derr := r.Docker.NetworkIDs(ctx)
		if derr != nil {
			return nil, fmt.Errorf("scenario %s: list networks: %w", sc.Name, derr)
		}
		rawNets, derr := r.Docker.InspectNetworks(ctx, netIDs)
		if derr != nil {
			return nil, fmt.Errorf("scenario %s: inspect networks: %w", sc.Name, derr)
		}
		rawCts, derr := r.Docker.InspectContainers(ctx, ids)
		if derr != nil {
			return nil, fmt.Errorf("scenario %s: inspect containers: %w", sc.Name, derr)
		}

		// Register observed identities before rendering any text.
		katharaNets := make(map[string]bool, len(rawNets))
		for _, nrec := range rawNets {
			katharaNets[nrec.Name] = true
		}
		containerIDByDevice = make(map[string]string, len(rawCts))
		observedHashes := map[string]bool{}
		for _, c := range rawCts {
			norm.AddLiteral(c.ID, TokContainerID)
			// A `bridged` device is also attached to Docker's default bridge
			// and gets an address from the daemon-wide allocation pool, which
			// depends on every container that ever ran on the host. Tokenize
			// exactly the observed address so that the lab's own addresses in
			// `ip addr` and `ip route` stay byte-exact.
			for _, netName := range sortedNetworkNames(c) {
				if katharaNets[netName] {
					continue
				}
				ep := c.NetworkSettings.Networks[netName]
				norm.AddLiteral(ep.IPAddress, TokDockerIP)
				norm.AddLiteral(ep.GlobalIPv6Address, TokDockerIP6)
			}
			if len(c.ID) >= 12 {
				norm.AddLiteral(c.ID[:12], TokShortID)
			}
			if h := c.Config.Labels["lab_hash"]; h != "" {
				observedHashes[h] = true
				norm.AddLiteral(h, TokLabHash)
			}
			if u := c.Config.Labels["user"]; u != "" {
				norm.AddLiteral(u, TokUser)
			}
			if name := c.Config.Labels["name"]; name != "" {
				containerIDByDevice[name] = c.ID
			}
		}

		snap.Networks, _ = buildNetworkRecords(rawNets, norm)
		linkByNetwork := map[string]string{}
		for _, nrec := range rawNets {
			linkByNetwork[nrec.Name] = nrec.Labels["name"]
		}
		var failures []string
		snap.Containers, failures = buildContainerRecords(rawCts, linkByNetwork, norm)
		assertionFailures = append(assertionFailures, failures...)

		// Derived-identifier assertions.
		if len(observedHashes) > 1 {
			assertionFailures = append(assertionFailures,
				fmt.Sprintf("containers carry %d distinct lab_hash labels", len(observedHashes)))
		}
		hashOK := observedHashes[expectedHash]
		if len(observedHashes) > 0 && !hashOK {
			assertionFailures = append(assertionFailures,
				fmt.Sprintf("lab_hash label does not match the %s-derived hash", hashSource))
		}
		userOK := true
		namesOK := true
		for _, c := range rawCts {
			if c.Config.Labels["user"] != expectedUser {
				userOK = false
			}
			want := fmt.Sprintf("kathara_%s_%s_%s",
				c.Config.Labels["user"], c.Config.Labels["name"], c.Config.Labels["lab_hash"])
			if strings.TrimPrefix(c.Name, "/") != want {
				namesOK = false
			}
		}
		netNamesOK := true
		for _, nrec := range rawNets {
			want := fmt.Sprintf("kathara_%s_%s_%s",
				expectedUser, nrec.Labels["name"], expectedHash)
			if nrec.Name != want {
				netNamesOK = false
			}
		}
		if !userOK {
			assertionFailures = append(assertionFailures, "user label does not match the derived user slug")
		}
		if !namesOK {
			assertionFailures = append(assertionFailures, "container names do not follow prefix_user_device_labhash")
		}
		if !netNamesOK {
			assertionFailures = append(assertionFailures, "network names do not follow prefix_user_cd_labhash")
		}

		snap.Scenario.LabHashDerivedOK = hashOK
		snap.Scenario.UserSlugDerivedOK = userOK
		snap.Scenario.ContainerNamesOK = namesOK
		snap.Scenario.NetworkNamesOK = netNamesOK

		// Step 3: in-container probes.
		if len(sc.ProbeSet()) > 0 {
			prober := NewProber(r.Docker, norm)
			devices := make([]string, 0, len(containerIDByDevice))
			for dev := range containerIDByDevice {
				devices = append(devices, dev)
			}
			sort.Strings(devices)
			for _, dev := range devices {
				snap.Probes = append(snap.Probes, prober.Probe(ctx, dev, containerIDByDevice[dev], &sc))
			}
		}
	}

	explicitMACs := norm.ExplicitMACs()
	if sc.ExpectExplicitMAC && len(explicitMACs) == 0 {
		assertionFailures = append(assertionFailures,
			"scenario declares expect_explicit_mac but no endpoint carries a kathara.mac_addr driver opt")
	}

	// Step 4: tear down. Always attempted, on a budget of its own.
	cleanCtx, cleanCancel := context.WithTimeout(context.Background(), cleanupBudget)
	defer cleanCancel()
	cleanArgs := []string{"lclean", "-d", labDir}
	cleanRes := r.runKathara(cleanCtx, cleanArgs)
	steps = append(steps, rawStep{step: "lclean", args: cleanArgs, res: cleanRes})

	// Step 5: post-teardown state.
	leftIDs, lerr := r.Docker.IDs(cleanCtx)
	if lerr != nil {
		return nil, fmt.Errorf("scenario %s: post-clean container list: %w", sc.Name, lerr)
	}
	leftNetIDs, lerr := r.Docker.NetworkIDs(cleanCtx)
	if lerr != nil {
		return nil, fmt.Errorf("scenario %s: post-clean network list: %w", sc.Name, lerr)
	}
	// Cleanliness is decided by the id lists, which are error-checked above; a
	// failing inspect must not be able to make a dirty host look clean.
	snap.Teardown.Clean = len(leftIDs) == 0 && len(leftNetIDs) == 0
	snap.Teardown.KatharaContainers = []string{}
	snap.Teardown.KatharaNetworks = []string{}
	if !snap.Teardown.Clean {
		leftCts, cerr := r.Docker.InspectContainers(cleanCtx, leftIDs)
		if cerr != nil {
			return nil, fmt.Errorf("scenario %s: post-clean container inspect: %w", sc.Name, cerr)
		}
		leftNets, cerr := r.Docker.InspectNetworks(cleanCtx, leftNetIDs)
		if cerr != nil {
			return nil, fmt.Errorf("scenario %s: post-clean network inspect: %w", sc.Name, cerr)
		}
		for _, name := range containerNames(leftCts) {
			snap.Teardown.KatharaContainers = append(snap.Teardown.KatharaContainers, norm.Text(name))
		}
		for _, nrec := range leftNets {
			snap.Teardown.KatharaNetworks = append(snap.Teardown.KatharaNetworks, norm.Text(nrec.Name))
		}
		sort.Strings(snap.Teardown.KatharaNetworks)
		assertionFailures = append(assertionFailures, "lclean left Kathara containers or networks behind")
	}

	for _, rel := range sc.HostFiles {
		snap.Teardown.HostFiles = append(snap.Teardown.HostFiles, hostFileRecord(labDir, rel, norm))
	}

	// Step 6: render every captured stream through the normalizer, now that
	// every volatile identity is known.
	for _, st := range steps {
		argv := make([]string, 0, len(st.args)+1)
		argv = append(argv, TokKathara)
		for _, a := range st.args {
			argv = append(argv, norm.Text(a))
		}
		snap.Commands = append(snap.Commands, CommandRecord{
			Step:     st.step,
			Argv:     argv,
			ExitCode: st.res.ExitCode,
			TimedOut: st.res.TimedOut,
			Stdout:   norm.Lines(st.res.Stdout),
			Stderr:   norm.Lines(st.res.Stderr),
		})
	}

	sort.Strings(assertionFailures)
	snap.Scenario = ScenarioRecord{
		SchemaVersion:     SchemaVersion,
		Name:              sc.Name,
		Kind:              sc.Kind,
		Status:            sc.Status,
		Note:              sc.Note,
		LabDir:            TokLabDir,
		Flags:             nonNilStrings(sc.Flags),
		Probes:            nonNilStrings(sc.ProbeSet()),
		FSRoots:           sc.FSRootSet(),
		DeviceFiles:       sc.DeviceFiles,
		HostFiles:         sc.HostFiles,
		HostDirs:          sc.HostDirs,
		LabHashSource:     hashSource,
		LabHashDerivedOK:  snap.Scenario.LabHashDerivedOK,
		UserSlugDerivedOK: snap.Scenario.UserSlugDerivedOK,
		ContainerNamesOK:  snap.Scenario.ContainerNamesOK,
		NetworkNamesOK:    snap.Scenario.NetworkNamesOK,
		ExplicitMACCount:  len(explicitMACs),
		ExpectExplicitMAC: sc.ExpectExplicitMAC,
		AssertionFailures: assertionFailures,
	}
	if sc.Kind != KindDeploy {
		// Derived-name assertions are meaningless when nothing was deployed.
		snap.Scenario.LabHashDerivedOK = false
		snap.Scenario.UserSlugDerivedOK = false
		snap.Scenario.ContainerNamesOK = false
		snap.Scenario.NetworkNamesOK = false
	}

	if len(assertionFailures) > 0 {
		return snap, fmt.Errorf("scenario %s: %d assertion failure(s): %s",
			sc.Name, len(assertionFailures), strings.Join(assertionFailures, "; "))
	}
	return snap, nil
}

func (r *Runner) runKathara(ctx context.Context, args []string) CmdResult {
	argv := append(append([]string(nil), r.KatharaArgv...), args...)
	var extraEnv []string
	if r.HomeDir != "" {
		// Point the binary under test at the harness-owned settings file.
		// os/exec keeps the last value for a duplicated key.
		extraEnv = []string{"HOME=" + r.HomeDir}
	}
	return runProcess(ctx, r.RepoRoot, extraEnv, argv)
}

// assertPreflightClean fails the run if Kathara state is present, after trying
// once to remove it. Foreign (non-Kathara) containers are only warned about:
// they are not this harness's to remove, but they can still poison a recording
// by occupying a published host port or shifting bridge-IP allocation.
func (r *Runner) assertPreflightClean(ctx context.Context) error {
	if all, aerr := r.Docker.AllContainerIDs(ctx); aerr == nil && len(all) > 0 {
		if kat, kerr := r.Docker.IDs(ctx); kerr == nil && len(all) > len(kat) {
			fmt.Fprintf(os.Stderr,
				"goldenharness: warning: %d non-Kathara container(s) present on this host; "+
					"published ports or bridge addressing may collide with the recording\n",
				len(all)-len(kat))
		}
	}
	ids, err := r.Docker.IDs(ctx)
	if err != nil {
		return fmt.Errorf("pre-flight container list: %w", err)
	}
	nets, err := r.Docker.NetworkIDs(ctx)
	if err != nil {
		return fmt.Errorf("pre-flight network list: %w", err)
	}
	if len(ids) == 0 && len(nets) == 0 {
		return nil
	}
	if err := r.Docker.ForceCleanup(ctx); err != nil {
		return fmt.Errorf("pre-flight cleanup: %w", err)
	}
	ids, err = r.Docker.IDs(ctx)
	if err != nil {
		return fmt.Errorf("pre-flight recheck containers: %w", err)
	}
	nets, err = r.Docker.NetworkIDs(ctx)
	if err != nil {
		return fmt.Errorf("pre-flight recheck networks: %w", err)
	}
	if len(ids) > 0 || len(nets) > 0 {
		return fmt.Errorf("pre-flight: host still has %d Kathara container(s) and %d network(s)", len(ids), len(nets))
	}
	return nil
}

var reLabName = regexp.MustCompile(`(?m)^LAB_NAME=(.*)$`)

// expectedLabHash re-derives the lab hash the way Kathara does: the urlsafe
// md5 of LAB_NAME when the lab declares one, of the realpath otherwise.
func expectedLabHash(labDir, labDirReal string) (hash, source string) {
	raw, err := os.ReadFile(filepath.Join(labDir, "lab.conf"))
	if err == nil {
		if m := reLabName.FindStringSubmatch(string(raw)); m != nil {
			name := strings.TrimSpace(strings.NewReplacer(`"`, "", "'", "").Replace(m[1]))
			if name != "" {
				return GenerateURLSafeHash(name), "lab_name"
			}
		}
	}
	return GenerateURLSafeHash(labDirReal), "path"
}

// expectedUserSlug re-derives Kathara's per-user container-name component.
func expectedUserSlug() string {
	name := os.Getenv("USER")
	if u, err := user.Current(); err == nil && u.Username != "" {
		name = u.Username
	}
	host, err := os.Hostname()
	if err != nil {
		host = ""
	}
	return Slug(name + "-" + GenerateURLSafeHash(host))
}

func hostFileRecord(labDir, rel string, n *Normalizer) HostFileRecord {
	rec := HostFileRecord{Path: rel}
	data, err := os.ReadFile(filepath.Join(labDir, rel))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			rec.Content = []string{"<READ_ERROR> " + n.Text(err.Error())}
		}
		return rec
	}
	sum := sha256.Sum256(data)
	rec.Exists = true
	rec.SHA256 = hex.EncodeToString(sum[:])
	rec.Content = n.FileLines(string(data))
	return rec
}

// ensureHostDirs creates the host directories a scenario's `volume` options
// mount and returns the ones that did not already exist, so the caller can
// remove exactly those again.
func ensureHostDirs(dirs []string) ([]string, error) {
	var created []string
	for _, dir := range dirs {
		if pathExists(dir) {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return created, fmt.Errorf("create host dir %s: %w", dir, err)
		}
		created = append(created, dir)
	}
	return created, nil
}

func resetFiles(labDir string, rels []string) error {
	for _, rel := range rels {
		if filepath.IsAbs(rel) || strings.Contains(rel, "..") {
			return fmt.Errorf("reset_files entry %q must be a lab-dir-relative path with no parent traversal", rel)
		}
		if err := os.RemoveAll(filepath.Join(labDir, rel)); err != nil {
			return fmt.Errorf("reset %s: %w", rel, err)
		}
	}
	return nil
}

// sortedNetworkNames gives the endpoint map a deterministic traversal order.
func sortedNetworkNames(c rawContainer) []string {
	out := make([]string, 0, len(c.NetworkSettings.Networks))
	for name := range c.NetworkSettings.Networks {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func realPath(p string) string {
	if rp, err := filepath.EvalSymlinks(p); err == nil {
		return rp
	}
	return p
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func nonNilStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
