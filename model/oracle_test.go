package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/kerrors"
)

// testdata/model_oracle.json was produced BY Kathará 3.8.3, not by this port:
//	/root/kathara/pyvenv/bin/python tools/vectorcheck/model_probe.py
// It records, for a corpus of inputs, exactly what `add_meta` stored, what each
// lazily-validated accessor returned or raised, what `check()` did with a
// `bridged_iface`, and what `Lab` computed for its hash, its dependency order
// and its two rendered forms. The hand-written tables in the other files sample
// that behaviour and explain it; this file is the differential.
// Python provides the reference behaviour for this differential test.

type oracleDocument struct {
	AddMeta      []oracleAddMeta      `json:"add_meta"`
	AddMetaTwice []oracleAddMetaTwice `json:"add_meta_twice"`
	Accessors    oracleAccessors      `json:"accessors"`
	Check        []oracleCheck        `json:"check"`
	Lab          oracleLab            `json:"lab"`
	Names        []oracleName         `json:"names"`
}

// oracleResult is the probe's `run()` envelope: either a rendered result or the
// class and message of the exception.
type oracleResult struct {
	OK           bool   `json:"ok"`
	ResultType   string `json:"result_type"`
	ResultStr    string `json:"result_str"`
	ErrorClass   string `json:"error_class"`
	ErrorMessage string `json:"error_message"`
}

type oracleAddMeta struct {
	Meta          string     `json:"meta"`
	Value         string     `json:"value"`
	OK            bool       `json:"ok"`
	ErrorClass    string     `json:"error_class"`
	ErrorMessage  string     `json:"error_message"`
	PreviousIsNil bool       `json:"previous_is_none"`
	MetaAfter     oracleMeta `json:"meta_after"`
}

type oracleAddMetaTwice struct {
	Meta          string `json:"meta"`
	First         string `json:"first"`
	Second        string `json:"second"`
	PreviousIsNil bool   `json:"previous_is_none"`
	PreviousType  string `json:"previous_type"`
	PreviousStr   string `json:"previous_str"`
}

type oracleTyped struct {
	Type string `json:"type"`
	Str  string `json:"str"`
}

type oracleSysctl struct {
	Key  string `json:"key"`
	Type string `json:"type"`
	Str  string `json:"str"`
}

type oracleEnv struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type oraclePort struct {
	Host     int    `json:"host"`
	Protocol string `json:"protocol"`
	Guest    int    `json:"guest"`
}

type oracleUlimit struct {
	Key  string `json:"key"`
	Soft string `json:"soft"`
	Hard string `json:"hard"`
}

type oracleVolume struct {
	Key       string `json:"key"`
	GuestPath string `json:"guest_path"`
	Mode      string `json:"mode"`
}

type oracleMeta struct {
	ExecCommands []string               `json:"exec_commands"`
	Sysctls      []oracleSysctl         `json:"sysctls"`
	Envs         []oracleEnv            `json:"envs"`
	Ports        []oraclePort           `json:"ports"`
	Ulimits      []oracleUlimit         `json:"ulimits"`
	Volumes      []oracleVolume         `json:"volumes"`
	Scalars      map[string]oracleTyped `json:"scalars"`
}

type oracleValueResult struct {
	oracleResult
	Value string `json:"value"`
}

type oracleAccessors struct {
	Mem []oracleValueResult `json:"mem"`
	CPU []struct {
		Value string       `json:"value"`
		One   oracleResult `json:"one"`
		Nano  oracleResult `json:"nano"`
	} `json:"cpu"`
	NumTerms []oracleValueResult `json:"num_terms"`
	IPv6     []struct {
		oracleResult
		Value string `json:"value"`
		Kind  string `json:"kind"`
	} `json:"ipv6"`
}

type oracleCheck struct {
	Interfaces       []int  `json:"interfaces"`
	BridgedIface     string `json:"bridged_iface"`
	BridgedIfaceKind string `json:"bridged_iface_kind"`
	OK               bool   `json:"ok"`
	ErrorClass       string `json:"error_class"`
	ErrorMessage     string `json:"error_message"`
	Order            []int  `json:"order"`
}

type oracleLab struct {
	Hashes []struct {
		Source string `json:"source"`
		Hash   string `json:"hash"`
	} `json:"hashes"`
	Dependencies []struct {
		Dependencies []string `json:"dependencies"`
		Order        []string `json:"order"`
	} `json:"dependencies"`
	LabStr     string `json:"lab_str"`
	MachineStr string `json:"machine_str"`
}

type oracleName struct {
	Name         string `json:"name"`
	OK           bool   `json:"ok"`
	ResultStr    string `json:"result_str"`
	ErrorClass   string `json:"error_class"`
	ErrorMessage string `json:"error_message"`
}

func loadModelOracle(t *testing.T) *oracleDocument {
	t.Helper()

	raw, err := os.ReadFile("testdata/model_oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	doc := &oracleDocument{}
	if err := json.Unmarshal(raw, doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func errorClass(err error) string {
	if err == nil {
		return ""
	}
	var runtimeErr *PyRuntimeError
	if errors.As(err, &runtimeErr) {
		return runtimeErr.Class
	}
	return kerrors.HumanLabel(kerrors.Code(err))
}

// dumpMeta is the Go side of the probe's dump_meta.
func dumpMeta(m *Machine) oracleMeta {
	out := oracleMeta{
		ExecCommands: m.Meta.ExecCommands,
		Sysctls:      []oracleSysctl{},
		Envs:         []oracleEnv{},
		Ports:        []oraclePort{},
		Ulimits:      []oracleUlimit{},
		Volumes:      []oracleVolume{},
		Scalars:      map[string]oracleTyped{},
	}
	if out.ExecCommands == nil {
		out.ExecCommands = []string{}
	}

	for _, e := range m.Meta.Sysctls.Entries() {
		out.Sysctls = append(out.Sysctls, oracleSysctl{
			Key: e.Key, Type: e.Value.pyTypeName(), Str: e.Value.String(),
		})
	}
	for _, e := range m.Meta.Envs.Entries() {
		out.Envs = append(out.Envs, oracleEnv(e))
	}
	for _, e := range m.Meta.Ports.Entries() {
		out.Ports = append(out.Ports, oraclePort{
			Host: e.Key.HostPort, Protocol: e.Key.Protocol, Guest: e.Value,
		})
	}
	for _, e := range m.Meta.Ulimits.Entries() {
		out.Ulimits = append(out.Ulimits, oracleUlimit{
			Key:  e.Key,
			Soft: strconv.FormatInt(e.Value.Soft, 10),
			Hard: strconv.FormatInt(e.Value.Hard, 10),
		})
	}
	for _, e := range m.Meta.Volumes.Entries() {
		out.Volumes = append(out.Volumes, oracleVolume{
			Key: e.Key, GuestPath: e.Value.GuestPath, Mode: e.Value.Mode,
		})
	}
	for _, e := range m.Meta.Scalars() {
		out.Scalars[e.Name] = oracleTyped{Type: e.Value.pyTypeName(), Str: e.Value.String()}
	}
	return out
}

// TestAddMetaAgainstOracle replays every add_meta case and compares the whole
// resulting meta — keys, order, values AND their Python types.
func TestAddMetaAgainstOracle(t *testing.T) {
	t.Parallel()
	doc := loadModelOracle(t)

	if len(doc.AddMeta) < 90 {
		t.Fatalf("the oracle corpus has only %d add_meta cases", len(doc.AddMeta))
	}

	for i, tc := range doc.AddMeta {
		t.Run(strconv.Itoa(i)+"_"+tc.Meta, func(t *testing.T) {
			// Volume rows carry host paths the oracle abspath'd on Linux;
			// on Windows both CPython and this port resolve them through
			// ntpath.abspath ("/h" -> "C:\h"), so the recorded outcomes
			// cannot apply. Windows runtime is out of 1.0 scope.
			if runtime.GOOS == "windows" && tc.Meta == "volume" {
				t.Skip("oracle volume rows are Linux-abspath-shaped")
			}
			lab := NewLab("test_lab", DefaultDefaults())
			machine, err := lab.NewMachine("pc1", nil)
			if err != nil {
				t.Fatalf("NewMachine: %v", err)
			}

			prev, existed, err := machine.AddMeta(tc.Meta, tc.Value)
			if !tc.OK {
				if err == nil {
					t.Fatalf("AddMeta(%q, %q) succeeded; Python raises %s: %s",
						tc.Meta, tc.Value, tc.ErrorClass, tc.ErrorMessage)
				}
				if got := errorClass(err); got != tc.ErrorClass {
					t.Errorf("AddMeta(%q, %q) class = %s, Python says %s",
						tc.Meta, tc.Value, got, tc.ErrorClass)
				}
				if err.Error() != tc.ErrorMessage {
					t.Errorf("AddMeta(%q, %q) message = %q, Python says %q",
						tc.Meta, tc.Value, err.Error(), tc.ErrorMessage)
				}
				return
			}
			if err != nil {
				t.Fatalf("AddMeta(%q, %q) = %v; Python succeeds", tc.Meta, tc.Value, err)
			}
			if existed == tc.PreviousIsNil {
				t.Errorf("previous existed = %v (%v), Python previous is None = %v",
					existed, prev, tc.PreviousIsNil)
			}

			gotJSON := mustJSON(t, dumpMeta(machine))
			wantJSON := mustJSON(t, tc.MetaAfter)
			if gotJSON != wantJSON {
				t.Errorf("AddMeta(%q, %q) left\n%s\nPython leaves\n%s",
					tc.Meta, tc.Value, gotJSON, wantJSON)
			}
		})
	}
}

func TestAddMetaTwiceAgainstOracle(t *testing.T) {
	t.Parallel()
	doc := loadModelOracle(t)

	for _, tc := range doc.AddMetaTwice {
		t.Run(tc.Meta, func(t *testing.T) {
			lab := NewLab("test_lab", DefaultDefaults())
			machine, err := lab.NewMachine("pc1", nil)
			if err != nil {
				t.Fatalf("NewMachine: %v", err)
			}

			if _, _, err := machine.AddMeta(tc.Meta, tc.First); err != nil {
				t.Fatalf("first AddMeta: %v", err)
			}
			prev, existed, err := machine.AddMeta(tc.Meta, tc.Second)
			if err != nil {
				t.Fatalf("second AddMeta: %v", err)
			}

			if existed == tc.PreviousIsNil {
				t.Fatalf("previous existed = %v, Python previous is None = %v",
					existed, tc.PreviousIsNil)
			}
			if tc.PreviousIsNil {
				return
			}
			if got := pythonStr(prev); got != tc.PreviousStr {
				t.Errorf("previous = %q, Python says %q", got, tc.PreviousStr)
			}
		})
	}
}

// pythonStr renders an add_meta return the way `str()` renders the Python
// object it stands for, so the previous value can be compared with the oracle's.
func pythonStr(value any) string {
	switch v := value.(type) {
	case nil:
		return "None"
	case string:
		return v
	case bool:
		return Bool(v).String()
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case Ulimit:
		return "{'soft': " + strconv.FormatInt(v.Soft, 10) +
			", 'hard': " + strconv.FormatInt(v.Hard, 10) + "}"
	case Volume:
		return "{'guest_path': " + util.PythonRepr(v.GuestPath) +
			", 'mode': " + util.PythonRepr(v.Mode) + "}"
	default:
		return fmt.Sprintf("%v", value)
	}
}

// TestAccessorsAgainstOracle replays the four lazily-validated accessors over
// the stored values Python was measured on.
func TestAccessorsAgainstOracle(t *testing.T) {
	t.Parallel()
	doc := loadModelOracle(t)

	t.Run("mem", func(t *testing.T) {
		t.Parallel()
		for _, tc := range doc.Accessors.Mem {
			machine := oracleMachine(t)
			machine.Meta.Mem = Str(tc.Value)

			got, err := machine.GetMem()
			if !assertSameFailure(t, "GetMem", tc.Value, err, tc.oracleResult) {
				continue
			}
			if err == nil && got != tc.ResultStr {
				t.Errorf("GetMem(%q) = %q, Python says %q", tc.Value, got, tc.ResultStr)
			}
		}
	})

	t.Run("cpu", func(t *testing.T) {
		t.Parallel()
		for _, tc := range doc.Accessors.CPU {
			for _, arm := range []struct {
				name       string
				multiplier float64
				want       oracleResult
			}{
				{name: "GetCPU", multiplier: 1, want: tc.One},
				{name: "GetCPU(1e9)", multiplier: 1e9, want: tc.Nano},
			} {
				machine := oracleMachine(t)
				machine.Meta.CPUs = Str(tc.Value)

				got, err := machine.GetCPU(arm.multiplier)
				if !assertSameFailure(t, arm.name, tc.Value, err, arm.want) {
					continue
				}
				if err != nil {
					continue
				}
				if got == nil {
					t.Errorf("%s(%q) = nil, Python says %s", arm.name, tc.Value, arm.want.ResultStr)
					continue
				}
				if beyondInt64(arm.want.ResultStr) {

					if *got != math.MaxInt64 {
						t.Errorf("%s(%q) = %d, want the documented saturation", arm.name, tc.Value, *got)
					}
					continue
				}
				if strconv.FormatInt(*got, 10) != arm.want.ResultStr {
					t.Errorf("%s(%q) = %d, Python says %s", arm.name, tc.Value, *got, arm.want.ResultStr)
				}
			}
		}
	})

	t.Run("num_terms", func(t *testing.T) {
		t.Parallel()
		for _, tc := range doc.Accessors.NumTerms {
			machine := oracleMachine(t)
			machine.Meta.NumTerms = Str(tc.Value)

			got, err := machine.GetNumTerms()
			if !assertSameFailure(t, "GetNumTerms", tc.Value, err, tc.oracleResult) {
				continue
			}
			if err != nil {
				continue
			}
			want := tc.ResultStr
			if beyondInt64(want) {

				if got != math.MaxInt {
					t.Errorf("GetNumTerms(%q) = %d, want the documented saturation", tc.Value, got)
				}
				continue
			}
			if strconv.Itoa(got) != want {
				t.Errorf("GetNumTerms(%q) = %d, Python says %s", tc.Value, got, want)
			}
		}
	})

	t.Run("ipv6", func(t *testing.T) {
		t.Parallel()
		for _, tc := range doc.Accessors.IPv6 {
			machine := oracleMachine(t)
			switch tc.Kind {
			case "bool":
				machine.Meta.IPv6 = Bool(tc.Value == "True")
			case "str":
				machine.Meta.IPv6 = Str(tc.Value)
			case "int":
				n, err := strconv.ParseInt(tc.Value, 10, 64)
				if err != nil {
					t.Fatalf("ipv6 int: %v", err)
				}
				machine.Meta.IPv6 = Int(n)
			case "float":
				f, err := strconv.ParseFloat(tc.Value, 64)
				if err != nil {
					t.Fatalf("ipv6 float: %v", err)
				}
				machine.Meta.IPv6 = Float(f)
			}

			got, err := machine.IsIPv6Enabled()
			if !assertSameFailure(t, "IsIPv6Enabled", tc.Value, err, tc.oracleResult) {
				continue
			}
			if err == nil && Bool(got).String() != tc.ResultStr {
				t.Errorf("IsIPv6Enabled(%s %s) = %v, Python says %s",
					tc.Kind, tc.Value, got, tc.ResultStr)
			}
		}
	})
}

// assertSameFailure compares the failure half of an accessor call and reports
// whether the two implementations agree so far.
func assertSameFailure(t *testing.T, op, input string, err error, want oracleResult) bool {
	t.Helper()

	if want.OK {
		if err != nil {
			t.Errorf("%s(%q) = %v; Python succeeds with %s", op, input, err, want.ResultStr)
			return false
		}
		return true
	}
	if err == nil {
		t.Errorf("%s(%q) succeeded; Python raises %s: %s", op, input, want.ErrorClass, want.ErrorMessage)
		return false
	}
	if got := errorClass(err); got != want.ErrorClass {
		t.Errorf("%s(%q) class = %s, Python says %s", op, input, got, want.ErrorClass)
	}
	if err.Error() != want.ErrorMessage {
		t.Errorf("%s(%q) message = %q, Python says %q", op, input, err.Error(), want.ErrorMessage)
	}
	return false
}

func beyondInt64(decimal string) bool {
	v, ok := new(big.Int).SetString(decimal, 10)
	return ok && !v.IsInt64()
}

func TestCheckAgainstOracle(t *testing.T) {
	t.Parallel()
	doc := loadModelOracle(t)

	for i, tc := range doc.Check {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			lab := NewLab("test_lab", DefaultDefaults())
			machine, err := lab.NewMachine("pc1", nil)
			if err != nil {
				t.Fatalf("NewMachine: %v", err)
			}
			for _, number := range tc.Interfaces {
				if _, err := lab.ConnectMachineObjToLink(machine, "cd"+strconv.Itoa(number),
					AddInterfaceOptions{Number: InterfaceNumber(number)}); err != nil {
					t.Fatalf("ConnectMachineObjToLink: %v", err)
				}
			}
			switch tc.BridgedIfaceKind {
			case "int":
				n, err := strconv.ParseInt(tc.BridgedIface, 10, 64)
				if err != nil {
					t.Fatalf("bridged_iface: %v", err)
				}
				machine.Meta.BridgedIface = Int(n)
			case "str":
				machine.Meta.BridgedIface = Str(tc.BridgedIface)
			case "float":
				f, err := strconv.ParseFloat(tc.BridgedIface, 64)
				if err != nil {
					t.Fatalf("bridged_iface: %v", err)
				}
				machine.Meta.BridgedIface = Float(f)
			case "bool":
				machine.Meta.BridgedIface = Bool(tc.BridgedIface == "True")
			case "list":
				// The probe only ever emits ["x"] for this kind; assert that so
				// a widened corpus cannot silently test the wrong value.
				if tc.BridgedIface != "['x']" {
					t.Fatalf("list bridged_iface = %q, probe promises \"['x']\"", tc.BridgedIface)
				}
				machine.Meta.BridgedIface = Strings([]string{"x"})
			}

			err = machine.Check()
			if tc.OK {
				if err != nil {
					t.Fatalf("Check() = %v; Python succeeds", err)
				}
				order := []int{}
				for _, iface := range machine.Interfaces() {
					order = append(order, iface.Number)
				}
				want := tc.Order
				if want == nil {
					want = []int{}
				}
				if !equalInts(order, want) {
					t.Errorf("interface order = %v, Python says %v", order, want)
				}
				return
			}
			if err == nil {
				t.Fatalf("Check() succeeded; Python raises %s: %s", tc.ErrorClass, tc.ErrorMessage)
			}
			if got := errorClass(err); got != tc.ErrorClass {
				t.Errorf("class = %s, Python says %s", got, tc.ErrorClass)
			}
			if err.Error() != tc.ErrorMessage {
				t.Errorf("message = %q, Python says %q", err.Error(), tc.ErrorMessage)
			}
		})
	}
}

// TestLabAgainstOracle pins the identity chain, the dependency order and the
// two rendered forms.
func TestLabAgainstOracle(t *testing.T) {
	t.Parallel()
	doc := loadModelOracle(t)

	for _, tc := range doc.Lab.Hashes {
		if got := NewLab(tc.Source, DefaultDefaults()).Hash; got != tc.Hash {
			t.Errorf("hash of %q = %q, Python says %q", tc.Source, got, tc.Hash)
		}
	}

	for _, tc := range doc.Lab.Dependencies {
		lab := NewLab("test_lab", DefaultDefaults())
		for _, name := range []string{"pc1", "pc2", "pc3", "pc4"} {
			if _, err := lab.NewMachine(name, nil); err != nil {
				t.Fatalf("NewMachine: %v", err)
			}
		}
		lab.ApplyDependencies(tc.Dependencies)
		if got := lab.MachineNames(); strings.Join(got, ",") != strings.Join(tc.Order, ",") {
			t.Errorf("ApplyDependencies(%v) = %v, Python says %v", tc.Dependencies, got, tc.Order)
		}
	}

	lab := NewLab("mylab", DefaultDefaults())
	lab.Description = "d"
	lab.Version = "1"
	lab.Author = "a"
	lab.Email = "e"
	lab.Web = "w"
	machine, _, err := lab.ConnectMachineToLink("pc1", "A", AddInterfaceOptions{MAC: "00:00:00:00:00:01"})
	if err != nil {
		t.Fatalf("ConnectMachineToLink: %v", err)
	}
	if _, _, err := lab.ConnectMachineToLink("pc1", "B", AddInterfaceOptions{}); err != nil {
		t.Fatalf("ConnectMachineToLink: %v", err)
	}
	for _, kv := range [][2]string{{"bridged", "false"}, {"sysctl", "net.a.b=1"}, {"port", "8080"}} {
		if _, _, err := machine.AddMeta(kv[0], kv[1]); err != nil {
			t.Fatalf("AddMeta: %v", err)
		}
	}

	if got := lab.String(); got != doc.Lab.LabStr {
		t.Errorf("Lab.String() = %q, Python says %q", got, doc.Lab.LabStr)
	}
	if got := machine.String(); got != doc.Lab.MachineStr {
		t.Errorf("Machine.String() = %q, Python says %q", got, doc.Lab.MachineStr)
	}
}

// TestNamesAgainstOracle pins the device-name rule, `str.strip()` included.
func TestNamesAgainstOracle(t *testing.T) {
	t.Parallel()
	doc := loadModelOracle(t)

	for _, tc := range doc.Names {
		lab := NewLab("test_lab", DefaultDefaults())
		machine, err := lab.NewMachine(tc.Name, nil)

		if !tc.OK {
			if err == nil {
				t.Errorf("NewMachine(%q) succeeded; Python raises %s", tc.Name, tc.ErrorClass)
				continue
			}
			if got := errorClass(err); got != tc.ErrorClass {
				t.Errorf("NewMachine(%q) class = %s, Python says %s", tc.Name, got, tc.ErrorClass)
			}
			if err.Error() != tc.ErrorMessage {
				t.Errorf("NewMachine(%q) message = %q, Python says %q", tc.Name, err.Error(), tc.ErrorMessage)
			}
			continue
		}
		if err != nil {
			t.Errorf("NewMachine(%q) = %v; Python accepts it", tc.Name, err)
			continue
		}
		if machine.Name != tc.ResultStr {
			t.Errorf("NewMachine(%q).Name = %q, Python says %q", tc.Name, machine.Name, tc.ResultStr)
		}
	}
}

// oracleMachine is the probe's fixture: a device named pc1, whose name the
// accessor error messages interpolate.
func oracleMachine(t *testing.T) *Machine {
	t.Helper()

	lab := NewLab("test_lab", DefaultDefaults())
	machine, err := lab.NewMachine("pc1", nil)
	if err != nil {
		t.Fatalf("NewMachine: %v", err)
	}
	return machine
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()

	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
