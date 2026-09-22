package event

import (
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/KatharaFramework/kathara-go/model"
)

// ---------------------------------------------------------------------------
// The catalog, checked against the Python sources
// ---------------------------------------------------------------------------

// catalogFile is testdata/catalog.json, produced by ast-walking every
// `EventDispatcher.dispatch/register/unregister` call in Kathara 3.8.3. It is
// the generated answer to "which events exist and what does each dispatch site
// pass", so this package cannot drift from 3.8.3 by adding, dropping or
// renaming one.
type catalogFile struct {
	Note   string `json:"note"`
	Events []struct {
		Name   string   `json:"name"`
		Kwargs []string `json:"kwargs"`
		Sites  []struct {
			Site   string   `json:"site"`
			Kwargs []string `json:"kwargs"`
		} `json:"sites"`
	} `json:"events"`
	Registrations []struct {
		Event  string `json:"event"`
		Site   string `json:"site"`
		Obj    string `json:"obj"`
		Method string `json:"method"`
	} `json:"cli_registrations"`
	Unregistrations []struct {
		Event string `json:"event"`
		Site  string `json:"site"`
	} `json:"cli_unregistrations"`
}

// catalogEntry maps an event type to its name and payload fields.
type catalogEntry struct {
	name   Name
	nameOf func() Name
	// kwargs are the keyword arguments the dispatch sites pass, sorted, as
	// testdata/catalog.json lists them.
	kwargs []string
	// fields names the Go field each kwarg became, in the same order. It is
	// documentation the test carries rather than an assertion — the compiler
	// checks the fields, only the mapping needs writing down.
	fields []string
}

func catalog() []catalogEntry {
	return []catalogEntry{
		{NameDockerImageUpdateFound, NameOf[DockerImageUpdateFound],
			[]string{"docker_image", "image_name"}, []string{"Image", "ImageName"}},
		{NameDockerPullEnded, NameOf[DockerPullEnded], nil, nil},
		{NameDockerPullProgress, NameOf[DockerPullProgress],
			[]string{"progress"}, []string{"Progress"}},
		{NameDockerPullStarted, NameOf[DockerPullStarted], nil, nil},
		{NameLinkDeployed, NameOf[LinkDeployed], []string{"item"}, []string{"Link"}},
		{NameLinkUndeployed, NameOf[LinkUndeployed], []string{"item"}, []string{"Network"}},
		{NameLinksDeployEnded, NameOf[LinksDeployEnded], nil, nil},
		{NameLinksDeployStarted, NameOf[LinksDeployStarted], []string{"items"}, []string{"Links"}},
		{NameLinksUndeployEnded, NameOf[LinksUndeployEnded], nil, nil},
		{NameLinksUndeployStarted, NameOf[LinksUndeployStarted], []string{"items"}, []string{"Networks"}},
		// Two backends, two payload shapes for one kwarg: Docker sends the
		// device object, Kubernetes the device name.
		{NameMachineDeployed, NameOf[MachineDeployed], []string{"item"}, []string{"Machine", "Name"}},
		{NameMachineStartupWaitEnded, NameOf[MachineStartupWaitEnded], nil, nil},
		{NameMachineStartupWaitStarted, NameOf[MachineStartupWaitStarted], nil, nil},
		{NameMachineUndeployed, NameOf[MachineUndeployed], []string{"item"}, []string{"Container", "Name"}},
		{NameMachinesDeployEnded, NameOf[MachinesDeployEnded], nil, nil},
		{NameMachinesDeployStarted, NameOf[MachinesDeployStarted], []string{"items"}, []string{"Machines"}},
		{NameMachinesUndeployEnded, NameOf[MachinesUndeployEnded], nil, nil},
		{NameMachinesUndeployStarted, NameOf[MachinesUndeployStarted],
			[]string{"items"}, []string{"Containers", "Names"}},
		{NameMachinesWithVolumes, NameOf[MachinesWithVolumes],
			[]string{"lab", "machines_with_volumes"}, []string{"Lab", "Machines"}},
	}
}

func loadCatalog(t *testing.T) catalogFile {
	t.Helper()
	data, err := os.ReadFile("testdata/catalog.json")
	if err != nil {
		t.Fatalf("read catalog: %v", err)
	}
	var file catalogFile
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("parse catalog: %v", err)
	}
	if len(file.Events) == 0 {
		t.Fatal("no events in testdata/catalog.json")
	}
	return file
}

// TestCatalogMatchesPythonSources: same set of event names, same kwargs per
// name, nothing invented and nothing missed.
func TestCatalogMatchesPythonSources(t *testing.T) {
	file := loadCatalog(t)

	want := make(map[string][]string, len(file.Events))
	for _, e := range file.Events {
		want[e.Name] = e.Kwargs
	}

	got := make(map[string]catalogEntry, len(catalog()))
	for _, e := range catalog() {
		if _, dup := got[string(e.name)]; dup {
			t.Errorf("event %q listed twice in the Go catalog", e.name)
		}
		got[string(e.name)] = e
	}

	for name, kwargs := range want {
		entry, ok := got[name]
		if !ok {
			t.Errorf("3.8.3 dispatches %q and this package has no event for it", name)
			continue
		}
		if strings.Join(entry.kwargs, ",") != strings.Join(kwargs, ",") {
			t.Errorf("event %q: kwargs %v carried by fields %v, 3.8.3 passes %v",
				name, entry.kwargs, entry.fields, kwargs)
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			t.Errorf("event %q exists here but 3.8.3 never dispatches it", name)
		}
	}

	t.Logf("%d events, %d dispatch sites, %d CLI registrations, %d unregistrations",
		len(file.Events), countSites(file), len(file.Registrations), len(file.Unregistrations))
}

func countSites(file catalogFile) int {
	n := 0
	for _, e := range file.Events {
		n += len(e.Sites)
	}
	return n
}

// TestNameOfReportsTheWireName: every payload type reports the string 3.8.3
// keys the table with, which is what a subscriber written against the Python
// names relies on.
func TestNameOfReportsTheWireName(t *testing.T) {
	seen := map[Name]bool{}
	for _, e := range catalog() {
		t.Run(string(e.name), func(t *testing.T) {
			if got := e.nameOf(); got != e.name {
				t.Errorf("NameOf = %q, want %q", got, e.name)
			}
			if seen[e.name] {
				t.Errorf("name %q is claimed by two payload types", e.name)
			}
			seen[e.name] = true
		})
	}
	if len(seen) != 19 {
		t.Errorf("catalog has %d distinct names, want 19", len(seen))
	}
}

func TestNameOfIsTotal(t *testing.T) {
	for _, e := range catalog() {
		if got := e.nameOf(); got == "" {
			t.Errorf("NameOf for %q is the empty name", e.name)
		}
	}
	if len(catalog()) != 19 {
		t.Errorf("catalog has %d entries, want 19", len(catalog()))
	}
}

// TestSortedCatalogIsStable guards the constant block against a name being
// pasted twice with different spellings.
func TestSortedCatalogIsStable(t *testing.T) {
	names := make([]string, 0, len(catalog()))
	for _, e := range catalog() {
		names = append(names, string(e.name))
	}
	if !slices.IsSorted(names) {
		t.Errorf("catalog is not in name order: %v", names)
	}
}

// ---------------------------------------------------------------------------
// Every event round-trips
// ---------------------------------------------------------------------------

// roundTrip subscribes to E, dispatches one, and reports what arrived.
func roundTrip[E Payload](t *testing.T, e E) {
	t.Helper()

	d := New()
	var got []E
	Subscribe(d, func(ev E) error {
		got = append(got, ev)
		return nil
	})
	if err := Dispatch(d, e); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("subscriber fired %d times, want 1", len(got))
	}
	if got[0].eventName() != NameOf[E]() {
		t.Errorf("delivered %q, want %q", got[0].eventName(), NameOf[E]())
	}

	// Tearing the event down leaves nothing behind.
	if err := Unsubscribe[E](d); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	if err := Dispatch(d, e); err != nil {
		t.Fatalf("Dispatch after Unsubscribe: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("subscriber fired %d times after Unsubscribe, want 1", len(got))
	}
}

// TestEveryEventRoundTrips walks the whole catalog through
// Subscribe/Dispatch/Unsubscribe, so no event is merely declared.
func TestEveryEventRoundTrips(t *testing.T) {
	lab := model.NewLab("test", model.Defaults{})
	machine := &model.Machine{Name: "pc1"}
	link := &model.Link{Name: "A"}
	total := int64(1024)

	t.Run(string(NameDockerPullStarted), func(t *testing.T) { roundTrip(t, DockerPullStarted{}) })
	t.Run(string(NameDockerPullProgress), func(t *testing.T) {
		status, id := "Downloading", "a1b2c3"
		roundTrip(t, DockerPullProgress{Progress: PullProgress{
			Status: &status, ID: &id,
			Detail: &PullProgressDetail{Total: &total},
		}})
	})
	t.Run(string(NameDockerPullEnded), func(t *testing.T) { roundTrip(t, DockerPullEnded{}) })
	t.Run(string(NameDockerImageUpdateFound), func(t *testing.T) {
		roundTrip(t, DockerImageUpdateFound{Image: &fakePuller{}, ImageName: "kathara/base"})
	})
	t.Run(string(NameLinksDeployStarted), func(t *testing.T) {
		roundTrip(t, LinksDeployStarted{Links: []*model.Link{link}})
	})
	t.Run(string(NameLinkDeployed), func(t *testing.T) { roundTrip(t, LinkDeployed{Link: link}) })
	t.Run(string(NameLinksDeployEnded), func(t *testing.T) { roundTrip(t, LinksDeployEnded{}) })
	t.Run(string(NameLinksUndeployStarted), func(t *testing.T) {
		roundTrip(t, LinksUndeployStarted{Networks: []APIObject{"net-a"}})
	})
	t.Run(string(NameLinkUndeployed), func(t *testing.T) {
		roundTrip(t, LinkUndeployed{Network: "net-a"})
	})
	t.Run(string(NameLinksUndeployEnded), func(t *testing.T) { roundTrip(t, LinksUndeployEnded{}) })
	t.Run(string(NameMachinesDeployStarted), func(t *testing.T) {
		roundTrip(t, MachinesDeployStarted{Machines: []*model.Machine{machine}})
	})
	t.Run(string(NameMachineDeployed), func(t *testing.T) {
		roundTrip(t, MachineDeployed{Machine: machine})
	})
	t.Run(string(NameMachinesDeployEnded), func(t *testing.T) { roundTrip(t, MachinesDeployEnded{}) })
	t.Run(string(NameMachinesUndeployStarted), func(t *testing.T) {
		roundTrip(t, MachinesUndeployStarted{Containers: []APIObject{"container-1"}})
	})
	t.Run(string(NameMachineUndeployed), func(t *testing.T) {
		roundTrip(t, MachineUndeployed{Name: "pc1"})
	})
	t.Run(string(NameMachinesUndeployEnded), func(t *testing.T) { roundTrip(t, MachinesUndeployEnded{}) })
	t.Run(string(NameMachineStartupWaitStarted), func(t *testing.T) {
		roundTrip(t, MachineStartupWaitStarted{})
	})
	t.Run(string(NameMachineStartupWaitEnded), func(t *testing.T) {
		roundTrip(t, MachineStartupWaitEnded{})
	})
	t.Run(string(NameMachinesWithVolumes), func(t *testing.T) {
		roundTrip(t, MachinesWithVolumes{Lab: lab, Machines: []*model.Machine{machine}})
	})
}

// ---------------------------------------------------------------------------
// Payloads
// ---------------------------------------------------------------------------

// fakePuller stands in for `DockerImage`, whose only role in the payload is to
// be the receiver of `pull(image_name)`.
type fakePuller struct {
	pulled []string
	err    error
}

func (f *fakePuller) Pull(imageName string) error {
	f.pulled = append(f.pulled, imageName)
	return f.err
}

// TestImageUpdateSubscriberPullsThroughThePayload is the shape
// `UpdateDockerImage.run` needs: the payload carries the puller and the name,
// and the subscriber's failure comes back out of Dispatch — which is how a
// pull that cannot reach Docker Hub reaches `check_for_updates` in 3.8.3
// (`manager/docker/DockerImage.py:95`, `cli/ui/event/UpdateDockerImage.py:27`).
func TestImageUpdateSubscriberPullsThroughThePayload(t *testing.T) {
	d := New()
	puller := &fakePuller{}

	Subscribe(d, func(e DockerImageUpdateFound) error {
		return e.Image.Pull(e.ImageName)
	})
	if err := Dispatch(d, DockerImageUpdateFound{Image: puller, ImageName: "kathara/base:latest"}); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(puller.pulled) != 1 || puller.pulled[0] != "kathara/base:latest" {
		t.Errorf("pulled %v, want [kathara/base:latest]", puller.pulled)
	}

	puller.err = errBoom
	err := Dispatch(d, DockerImageUpdateFound{Image: puller, ImageName: "kathara/base:latest"})
	if err == nil || err.Error() != errBoom.Error() {
		t.Errorf("Dispatch error = %v, want the pull failure", err)
	}
}

// TestPullProgressDetailKeepsTheAbsentKey: 3.8.3 asks `'total' in
// progress['progressDetail']` and hands rich a None when the key is missing,
// which is an indeterminate bar and not a zero-length one
// (`cli/ui/event/HandleDockerImagePull.py:58,66`).
func TestPullProgressDetailKeepsTheAbsentKey(t *testing.T) {
	zero := int64(0)
	cases := []struct {
		name          string
		detail        PullProgressDetail
		hasTotal      bool
		hasCurrent    bool
		totalIsZeroed bool
	}{
		{name: "absent", detail: PullProgressDetail{}},
		{name: "present and zero", detail: PullProgressDetail{Total: &zero, Current: &zero},
			hasTotal: true, hasCurrent: true, totalIsZeroed: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := New()
			var got PullProgressDetail
			Subscribe(d, func(e DockerPullProgress) error {
				got = *e.Progress.Detail
				return nil
			})
			if err := Dispatch(d, DockerPullProgress{Progress: PullProgress{Detail: &tc.detail}}); err != nil {
				t.Fatalf("Dispatch: %v", err)
			}
			if (got.Total != nil) != tc.hasTotal {
				t.Errorf("Total set = %v, want %v", got.Total != nil, tc.hasTotal)
			}
			if (got.Current != nil) != tc.hasCurrent {
				t.Errorf("Current set = %v, want %v", got.Current != nil, tc.hasCurrent)
			}
			if tc.totalIsZeroed && *got.Total != 0 {
				t.Errorf("Total = %d, want 0 — a present zero is not an absent key", *got.Total)
			}
		})
	}
}

// TestPullProgressCanCarryAnErrorLine: the pull stream's failure lines
// (`{"errorDetail": {...}, "error": "..."}`) carry no `status` key, docker-py
// yields them through untouched, and `HandleDockerImagePull.update` indexes
// `progress['status']` regardless — oracle-probed on 3.8.3:
func TestPullProgressCanCarryAnErrorLine(t *testing.T) {
	d := New()

	// The subscriber's first two lines, as `HandleDockerImagePull.update`
	// writes them: index `status`, then compare it.
	Subscribe(d, func(e DockerPullProgress) error {
		if e.Progress.Status == nil {
			return &model.PyRuntimeError{Class: "KeyError", Msg: "'status'"}
		}
		return nil
	})

	// A well-formed line: no error, and the status is readable.
	status := "Downloading"
	if err := Dispatch(d, DockerPullProgress{Progress: PullProgress{Status: &status}}); err != nil {
		t.Fatalf("Dispatch of a status line: %v", err)
	}

	// The failure line, on which 3.8.3 raises a KeyError.
	err := Dispatch(d, DockerPullProgress{Progress: PullProgress{}})
	if !errors.Is(err, model.ErrPyKeyError) {
		t.Fatalf("Dispatch of an error line = %v, want the KeyError 3.8.3 raises", err)
	}
	if got, want := err.Error(), "'status'"; got != want {
		t.Errorf("KeyError message = %q, want %q", got, want)
	}
}

// TestCountMirrorsLenItems: `HandleProgressBar.init` sizes the bar with
// `len(items)` (`cli/ui/event/HandleProgressBar.py:35`), and the two backends
// pass different things — including, for the Kubernetes undeploy, a set of
// device names rather than a list of API objects
// (`manager/kubernetes/KubernetesMachine.py:621`).
func TestCountMirrorsLenItems(t *testing.T) {
	links := []*model.Link{{Name: "A"}, {Name: "B"}}
	machines := []*model.Machine{{Name: "pc1"}, {Name: "pc2"}, {Name: "pc3"}}

	cases := []struct {
		name string
		got  int
		want int
	}{
		{"links_deploy_started", LinksDeployStarted{Links: links}.Count(), 2},
		{"links_deploy_started empty", LinksDeployStarted{}.Count(), 0},
		{"links_undeploy_started", LinksUndeployStarted{Networks: []APIObject{1, 2, 3}}.Count(), 3},
		{"machines_deploy_started", MachinesDeployStarted{Machines: machines}.Count(), 3},
		{"machines_undeploy_started docker",
			MachinesUndeployStarted{Containers: []APIObject{1, 2}}.Count(), 2},
		{"machines_undeploy_started kubernetes",
			MachinesUndeployStarted{Names: []string{"pc1", "pc2", "pc3", "pc4"}}.Count(), 4},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("Count = %d, want %d", tc.got, tc.want)
			}
		})
	}
}

func TestMachineDeployedCarriesEitherBackendsPayload(t *testing.T) {
	d := New()
	var names []string
	Subscribe(d, func(e MachineDeployed) error {
		if e.Machine != nil {
			names = append(names, "docker:"+e.Machine.Name)
			return nil
		}
		names = append(names, "kubernetes:"+e.Name)
		return nil
	})

	if err := Dispatch(d, MachineDeployed{Machine: &model.Machine{Name: "pc1"}}); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if err := Dispatch(d, MachineDeployed{Name: "pc2"}); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	want := []string{"docker:pc1", "kubernetes:pc2"}
	if !slices.Equal(names, want) {
		t.Errorf("delivered %v, want %v", names, want)
	}
}
