package labfile

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/model"
)

// `testdata/vectors` is the shared truth of the Go parsers and the Python
// client's: `tools/vectorcheck/check_python.py` replays every vector against
// real Kathará 3.8.3 and this replays the same vectors, through the same
// serialisation, against `labfile`. Neither implementation gets to win an
// argument with a vector — the schema and compatibility notes are in
// `testdata/vectors/README.md`.
// The corpus is the proof of this package. A behaviour that is not pinned here
// is not implemented.

// vectorCount is the size of the frozen corpus. Asserting it stops a vector
// from being silently skipped by a discovery regression or a stray rename.
const vectorCount = 142

// vectorsRoot is where the corpus lives, relative to this package.
const vectorsRoot = "testdata/vectors"

// metadataFields are the five `LAB_*` scalars whose Python `None` and `""` the
// port cannot tell apart.
var metadataFields = []string{"description", "version", "author", "email", "web"}

// vectorSpec is `vector.json`.
type vectorSpec struct {
	Parser         string   `json:"parser"`
	Description    string   `json:"description"`
	ConfName       string   `json:"conf_name"`
	Options        []string `json:"options"`
	Dirs           []string `json:"dirs"`
	OrderSensitive *bool    `json:"order_sensitive"`
	Notes          string   `json:"notes"`
}

func (v vectorSpec) confName() string {
	if v.ConfName == "" {
		return DefaultConfName
	}
	return v.ConfName
}

// orderSensitive defaults to true; only the FolderParser glob-order cases,
// whose order Python leaves to the filesystem, turn it off (compatibility note
// 21 in the vector README).
func (v vectorSpec) orderSensitive() bool { return v.OrderSensitive == nil || *v.OrderSensitive }

func TestVectors(t *testing.T) {
	dirs := discoverVectors(t)
	if len(dirs) != vectorCount {
		t.Fatalf("discovered %d vectors under %s, want %d", len(dirs), vectorsRoot, vectorCount)
	}

	warnings := installWarningCollector(t)

	for _, dir := range dirs {
		id, err := filepath.Rel(vectorsRoot, dir)
		if err != nil {
			t.Fatalf("vector id for %s: %v", dir, err)
		}

		t.Run(filepath.ToSlash(id), func(t *testing.T) {
			// Linux-oracle-shaped vectors that cannot apply on Windows, where
			// CPython itself behaves differently: directory-open error shapes
			// differ, and volume host paths pass through ntpath.abspath
			// ("/h" -> "C:\h") in both implementations. Windows runtime is
			// not covered until a Windows oracle recording is available.
			if runtime.GOOS == "windows" {
				for _, linuxShaped := range []string{
					"conf_name_is_directory",
					"lab_dep_is_directory",
					"meta_all_options",
					"volume_empty_segments",
					"duplicate_typed_meta_warnings",
				} {
					if strings.HasSuffix(id, linuxShaped) {
						t.Skip("Linux-oracle-shaped (directory-open or abspath'd volume paths)")
					}
				}
			}
			var spec vectorSpec
			readJSON(t, filepath.Join(dir, "vector.json"), &spec)

			var expected map[string]any
			readJSON(t, filepath.Join(dir, "expected.json"), &expected)

			warnings.reset()
			actual := runVector(t, dir, spec, warnings)

			want := normalize(t, expected, spec.orderSensitive())
			got := normalize(t, actual, spec.orderSensitive())

			if !reflect.DeepEqual(want, got) {
				t.Errorf("%s\n--- expected\n%s\n--- got\n%s", spec.Description, canon(t, want), canon(t, got))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Driving the parsers
// ---------------------------------------------------------------------------

// runVector materialises the vector's lab directory into a fresh temporary
// one, drives the entry point the vector names and returns the result in the
// vector JSON shape.
func runVector(t *testing.T, dir string, spec vectorSpec, warnings *warningCollector) map[string]any {
	t.Helper()

	tmp := t.TempDir()
	if spec.Parser != "options" {
		materialize(t, dir, spec, tmp)
	}

	actual := map[string]any{}
	var failure error

	switch spec.Parser {
	case "lab":
		lab, err := ParseLab(tmp, spec.confName(), model.DefaultDefaults())
		failure = err
		if err == nil {
			actual["lab"] = serializeLab(lab)
		}

	case "dep":
		deps, err := ParseDep(tmp)
		failure = err
		if err == nil {
			actual["dep"] = deps
		}

	case "folder":
		lab, err := ParseFolder(tmp, model.DefaultDefaults())
		failure = err
		if err == nil {
			actual["lab"] = serializeLab(lab)
		}

	case "options":
		options, err := ParseOptions(spec.Options)
		failure = err
		if err == nil {
			actual["options"] = serializeOptions(options)
		}

	case "lab+dep":
		// The `lstart` sequence: parse, then read the dependencies, then apply
		// them — but only when the list is truthy, so a comments-only lab.dep
		// leaves has_dependencies false.
		failure = func() error {
			lab, err := ParseLab(tmp, spec.confName(), model.DefaultDefaults())
			if err != nil {
				return err
			}
			deps, err := ParseDep(tmp)
			if err != nil {
				return err
			}
			actual["dep"] = deps
			if len(deps) > 0 {
				lab.ApplyDependencies(deps)
			}
			actual["lab"] = serializeLab(lab)
			return nil
		}()

	default:
		t.Fatalf("unknown parser kind %q", spec.Parser)
	}

	if failure != nil {
		actual = map[string]any{"error": map[string]any{
			"class":   pythonClass(failure),
			"message": failure.Error(),
		}}
	}

	if msgs := warnings.take(); len(msgs) > 0 {
		actual["warnings"] = msgs
	}
	return actual
}

// materialize copies `input/` into dest and then creates the declared
// directories, in that order — git cannot track an empty directory, so the
// FolderParser layouts and the "the config file is a directory" cases are
// declared in `vector.json` instead.
func materialize(t *testing.T, dir string, spec vectorSpec, dest string) {
	t.Helper()

	src := filepath.Join(dir, "input")
	entries, err := os.ReadDir(src)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reading %s: %v", src, err)
	}
	for _, entry := range entries {
		if entry.Name() == ".gitkeep" {
			continue
		}
		content, err := os.ReadFile(filepath.Join(src, entry.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", entry.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(dest, entry.Name()), content, 0o644); err != nil {
			t.Fatalf("writing %s: %v", entry.Name(), err)
		}
	}

	for _, name := range spec.Dirs {
		if err := os.MkdirAll(filepath.Join(dest, name), 0o755); err != nil {
			t.Fatalf("creating %s: %v", name, err)
		}
	}
}

// pythonClass is the vector's `error.class`, i.e. Python's
// `type(e).__name__`.
func pythonClass(err error) string {
	var runtime *model.PyRuntimeError
	if errors.As(err, &runtime) {
		return runtime.Class
	}

	var named interface{ PythonClass() string }
	if errors.As(err, &named) {
		return named.PythonClass()
	}

	return kerrors.HumanLabel(kerrors.Code(err))
}

// ---------------------------------------------------------------------------
// The serialiser — the Go half of tools/vectorcheck/check_python.py's
// ser_lab/ser_machine/ser_meta. One implementation, used by every vector kind.
// ---------------------------------------------------------------------------

func serializeLab(lab *model.Lab) map[string]any {
	order := lab.MachineNames()

	machines := map[string]any{}
	for i, machine := range lab.Machines() {
		machines[order[i]] = serializeMachine(lab, order[i], machine)
	}

	links := map[string]any{}
	for _, link := range lab.Links() {
		// Python builds this by walking `lab.machines` and filtering, so the
		// order is the SCENARIO's device order — which apply_dependencies
		// rewrites — and not the link's own.
		attached := []any{}
		for _, name := range order {
			if link.HasMachine(name) {
				attached = append(attached, name)
			}
		}
		links[link.Name] = map[string]any{"machines": attached}
	}

	out := map[string]any{
		"name":                    nil,
		"hash":                    nil,
		"description":             lab.Description,
		"version":                 lab.Version,
		"author":                  lab.Author,
		"email":                   lab.Email,
		"web":                     lab.Web,
		"machines":                machines,
		"machine_order":           toAnySlice(order),
		"links":                   links,
		"link_order":              toAnySlice(lab.LinkNames()),
		"general_options":         serializeScalars(lab.GeneralOptions()),
		"global_machine_metadata": serializeScalars(lab.GlobalMachineMetadatas()),
		"has_dependencies":        lab.HasDependencies,
	}
	if lab.HasName() {
		// The hash is derived from the lab PATH when there is no LAB_NAME,
		// which makes it temporary-directory dependent; it is only comparable
		// when a name was parsed.
		out["name"] = lab.Name()
		out["hash"] = lab.Hash
	}
	return out
}

func serializeMachine(lab *model.Lab, name string, machine *model.Machine) map[string]any {
	interfaces := map[string]any{}
	for _, iface := range machine.Interfaces() {
		entry := map[string]any{"cd": nil, "mac": nil}
		// A tombstone is Python's None slot: both fields stay null. It is
		// unreachable from a parse, and cheap to keep faithful.
		if !iface.IsTombstone() {
			entry["cd"] = iface.Link.Name
			if iface.MAC != "" {
				entry["mac"] = iface.MAC
			}
		}
		interfaces[strconv.Itoa(iface.Number)] = entry
	}

	out := map[string]any{
		"interfaces": interfaces,
		"meta":       serializeMeta(&machine.Meta),
		// `Machine.fs is not None`: a same-named directory existed when the
		// device was constructed. It is the one filesystem fact a parse result
		// carries.
		"has_dir":      machine.FS != nil,
		"startup_file": nil,
	}
	if startup := name + ".startup"; exists(lab, startup) {
		out["startup_file"] = startup
	}
	return out
}

func serializeMeta(meta *model.Meta) map[string]any {
	sysctls := map[string]any{}
	for _, e := range meta.Sysctls.Entries() {
		sysctls[e.Key] = e.Value.Value()
	}

	envs := map[string]any{}
	for _, e := range meta.Envs.Entries() {
		envs[e.Key] = e.Value
	}

	ports := map[string]any{}
	for _, e := range meta.Ports.Entries() {
		// Python keys this by the tuple (host_port, protocol); the vector
		// flattens it to "<host_port>/<protocol>".
		ports[strconv.Itoa(e.Key.HostPort)+"/"+e.Key.Protocol] = e.Value
	}

	ulimits := map[string]any{}
	for _, e := range meta.Ulimits.Entries() {
		ulimits[e.Key] = map[string]any{"soft": e.Value.Soft, "hard": e.Value.Hard}
	}

	volumes := map[string]any{}
	for _, e := range meta.Volumes.Entries() {
		volumes[e.Key] = map[string]any{"guest_path": e.Value.GuestPath, "mode": e.Value.Mode}
	}

	// Everything that is not one of the six containers, in its parsed type:
	// `privileged` and `bridged` are real bools because add_meta runs them
	// through strtobool, and every other lab.conf meta is a string — `ipv6`,
	// `mem`, `cpus` and `num_terms` included (compatibility note 7 in the
	// vector README).
	extra := map[string]any{}
	for _, e := range meta.Scalars() {
		extra[e.Name] = e.Value.Value()
	}

	return map[string]any{
		"exec_commands": toAnySlice(meta.ExecCommands),
		"sysctls":       sysctls,
		"envs":          envs,
		"ports":         ports,
		"ulimits":       ulimits,
		"volumes":       volumes,
		"extra":         extra,
	}
}

func serializeScalars(entries []model.Entry[string, model.Scalar]) map[string]any {
	out := map[string]any{}
	for _, e := range entries {
		out[e.Key] = e.Value.Value()
	}
	return out
}

func serializeOptions(options *model.OrderedMap[string, string]) map[string]any {
	out := map[string]any{}
	for _, e := range options.Entries() {
		out[e.Key] = e.Value
	}
	return out
}

// exists reports whether the scenario filesystem holds name, which is
// `lab.fs.exists(...)`.
func exists(lab *model.Lab, name string) bool {
	root, ok := lab.FSPath()
	if !ok {
		return false
	}
	_, err := os.Stat(filepath.Join(root, name))
	return err == nil
}

func toAnySlice[T any](in []T) []any {
	out := make([]any, 0, len(in))
	for _, v := range in {
		out = append(out, v)
	}
	return out
}

// ---------------------------------------------------------------------------
// Comparison
// ---------------------------------------------------------------------------

// normalize puts a document through JSON so both sides carry the same dynamic
// types, fills in the defaults the corpus leaves implicit and applies the two
// sanctioned foldings.
func normalize(t *testing.T, doc map[string]any, orderSensitive bool) map[string]any {
	t.Helper()

	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encoding document: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatalf("decoding document: %v", err)
	}

	if _, ok := out["warnings"]; !ok {
		out["warnings"] = []any{}
	}

	lab, _ := out["lab"].(map[string]any)
	if lab == nil {
		return out
	}

	for _, field := range metadataFields {
		if lab[field] == nil {
			lab[field] = ""
		}
	}

	if !orderSensitive {
		sortStrings(t, lab, "machine_order")
		sortStrings(t, lab, "link_order")
		links, _ := lab["links"].(map[string]any)
		for _, link := range links {
			if entry, ok := link.(map[string]any); ok {
				sortStrings(t, entry, "machines")
			}
		}
	}

	return out
}

func sortStrings(t *testing.T, holder map[string]any, key string) {
	t.Helper()

	raw, ok := holder[key].([]any)
	if !ok {
		return
	}
	values := make([]string, 0, len(raw))
	for _, v := range raw {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("%s holds a non-string %v", key, v)
		}
		values = append(values, s)
	}
	slices.Sort(values)
	holder[key] = toAnySlice(values)
}

// canon renders a document the way the corpus stores one: sorted keys, two
// spaces, literal non-ASCII.
func canon(t *testing.T, doc any) string {
	t.Helper()

	var buf strings.Builder
	encoder := json.NewEncoder(&buf)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(doc); err != nil {
		t.Fatalf("rendering document: %v", err)
	}
	return buf.String()
}

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

// discoverVectors returns every directory holding a `vector.json`, sorted, and
// does not descend into one.
func discoverVectors(t *testing.T) []string {
	t.Helper()

	var out []string
	err := filepath.WalkDir(vectorsRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		if _, err := os.Stat(filepath.Join(path, "vector.json")); err == nil {
			out = append(out, path)
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", vectorsRoot, err)
	}

	slices.Sort(out)
	return out
}

func readJSON(t *testing.T, path string, into any) {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if err := json.Unmarshal(content, into); err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}
}

// ---------------------------------------------------------------------------
// Warnings
// ---------------------------------------------------------------------------

// warningCollector is the Go analogue of the Python runner's logging handler:
// it records `logging.warning` calls, in order, so a vector can assert them.
type warningCollector struct {
	mu       sync.Mutex
	messages []any
}

func (c *warningCollector) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelWarn
}

func (c *warningCollector) Handle(_ context.Context, record slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages = append(c.messages, record.Message)
	return nil
}

func (c *warningCollector) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *warningCollector) WithGroup(string) slog.Handler      { return c }

func (c *warningCollector) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages = nil
}

func (c *warningCollector) take() []any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.messages)
}

// installWarningCollector redirects the default logger for the duration of the
// test and restores it afterwards.
func installWarningCollector(t *testing.T) *warningCollector {
	t.Helper()

	collector := &warningCollector{}
	previous := slog.Default()
	slog.SetDefault(slog.New(collector))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return collector
}
