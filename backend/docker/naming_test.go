package docker

import (
	"errors"
	"maps"
	"reflect"
	"testing"

	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/model"
	"github.com/KatharaFramework/kathara-go/settings"
)

// fixtureHash is `generate_urlsafe_hash("Default scenario")`, the hash the
// whole Python docker suite is written against
// (EXPECTATIONS-docker.md, "Fixture lab").
const fixtureHash = "9pe3y6IDMwx4PfOPu5mbNg"

// TestFixtureHashIsTheOracles anchors the identity chain the rest of this file
// interpolates: if `generate_urlsafe_hash` ever drifts, every name below is
// wrong and this is the test that says so first.
func TestFixtureHashIsTheOracles(t *testing.T) {
	if got := generateHashFixture(); got != fixtureHash {
		t.Fatalf("generate_urlsafe_hash(%q) = %q, want %q", "Default scenario", got, fixtureHash)
	}
}

// TestContainerName is `test_get_container_name_lab_hash`
// (EXPECTATIONS-docker.md §1.9) plus the SYNTHESIS C-7 correction: the name
// does NOT vary with `shared_cds`, contrary to what the two assertion-free
// Python tests imply.
func TestContainerName(t *testing.T) {
	tests := []struct {
		name                                     string
		devicePrefix, user, machineName, labHash string
		want                                     string
	}{
		{
			name:         "the four-part name",
			devicePrefix: "kathara", user: "user", machineName: "pc1", labHash: fixtureHash,
			want: "kathara_user_pc1_9pe3y6IDMwx4PfOPu5mbNg",
		},
		{
			name:         "a configured prefix is used verbatim",
			devicePrefix: "devprefix", user: "kathara-user", machineName: "r_2", labHash: "abc",
			want: "devprefix_kathara-user_r_2_abc",
		},
		{
			// The dead `lab_hash if "_%s" % lab_hash else ""` conditional never
			// takes its else branch, so an empty hash is kept and leaves a
			// trailing underscore rather than being dropped.
			name:         "an empty hash leaves the trailing separator",
			devicePrefix: "kathara", user: "user", machineName: "pc1", labHash: "",
			want: "kathara_user_pc1_",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ContainerName(tt.devicePrefix, tt.user, tt.machineName, tt.labHash); got != tt.want {
				t.Errorf("ContainerName = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestNetworkName is EXPECTATIONS-docker.md §3.1: the one name that DOES change
// with `shared_cds`, shedding the hash and then the user as sharing widens.
func TestNetworkName(t *testing.T) {
	tests := []struct {
		name   string
		shared settings.SharedCollisionDomains
		want   string
	}{
		{"not shared keeps user and hash", settings.NotShared, "kathara_user_A_9pe3y6IDMwx4PfOPu5mbNg"},
		{"shared between labs drops the hash", settings.SharedBetweenLabs, "kathara_user_A"},
		{"shared between users drops both", settings.SharedBetweenUsers, "kathara_A"},
		{
			// Python's if/elif chain has no else and returns None, which the
			// daemon then rejects. Nothing validates `shared_cds` on load, so
			// the value is reachable.
			"an out-of-range mode has no name at all", settings.SharedCollisionDomains(9), "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NetworkName("kathara", "user", "A", fixtureHash, tt.shared)
			if got != tt.want {
				t.Errorf("NetworkName = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestObjectFilters is EXPECTATIONS-docker.md §1.8 and §3.5, which are the same
// five cases for containers and networks.
//
// The ORDER is the assertion, not just the membership: SYNTHESIS §1.2 pins it
// as `app=kathara`, `user=`, `lab_hash=`, `name=`, and the goldens read the
// request the SDK builds from it.
func TestObjectFilters(t *testing.T) {
	tests := []struct {
		name                  string
		user, labHash, object string
		want                  []string
	}{
		{
			name: "all three filters, in order",
			user: "user", labHash: fixtureHash, object: "pc1",
			want: []string{"app=kathara", "user=user", "lab_hash=9pe3y6IDMwx4PfOPu5mbNg", "name=pc1"},
		},
		{name: "no filters at all", want: []string{"app=kathara"}},
		{name: "lab hash only", labHash: fixtureHash, want: []string{"app=kathara", "lab_hash=9pe3y6IDMwx4PfOPu5mbNg"}},
		{name: "object name only", object: "pc1", want: []string{"app=kathara", "name=pc1"}},
		{name: "user only", user: "user", want: []string{"app=kathara", "user=user"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			terms := ObjectFilters(tt.user, tt.labHash, tt.object)
			got := make([]string, 0, len(terms))
			for _, term := range terms {
				got = append(got, term.String())
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ObjectFilters = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestObjectFiltersTreatsEmptyAsAbsent is the NILABILITY row for these three
// arguments: "" is Python's None here, because the filter list is built with
// truthiness tests.
func TestObjectFiltersTreatsEmptyAsAbsent(t *testing.T) {
	if got := ObjectFilters("", "", ""); len(got) != 1 {
		t.Fatalf("empty strings produced %d terms, want just app=kathara", len(got))
	}
}

// TestContainerLabels is the five-label scheme of `DockerMachine.create` plus
// the conditional sixth (EXPECTATIONS-docker.md §7 item 1).
func TestContainerLabels(t *testing.T) {
	want := map[string]string{
		"name":     "pc1",
		"lab_hash": fixtureHash,
		"user":     "user",
		"app":      "kathara",
		"shell":    "/bin/bash",
	}
	got := ContainerLabels("pc1", fixtureHash, "user", "/bin/bash", nil)
	if !maps.Equal(got, want) {
		t.Errorf("ContainerLabels = %v, want %v", got, want)
	}

	number := 3
	want["bridged_iface"] = "3"
	got = ContainerLabels("pc1", fixtureHash, "user", "/bin/bash", &number)
	if !maps.Equal(got, want) {
		t.Errorf("bridged ContainerLabels = %v, want %v", got, want)
	}
}

// TestNetworkLabels is EXPECTATIONS-docker.md §3.2's shared-CD variants: the
// label set SHRINKS as sharing widens, which is what makes
// `DockerLinkStats.__init__` KeyError in the shared modes (SYNTHESIS §1.2).
func TestNetworkLabels(t *testing.T) {
	tests := []struct {
		name   string
		shared settings.SharedCollisionDomains
		want   map[string]string
	}{
		{
			name: "not shared carries user and lab_hash", shared: settings.NotShared,
			want: map[string]string{"name": "A", "app": "kathara", "external": "", "user": "user", "lab_hash": fixtureHash},
		},
		{
			name: "shared between labs omits lab_hash", shared: settings.SharedBetweenLabs,
			want: map[string]string{"name": "A", "app": "kathara", "external": "", "user": "user"},
		},
		{
			name: "shared between users omits both", shared: settings.SharedBetweenUsers,
			want: map[string]string{"name": "A", "app": "kathara", "external": ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NetworkLabels("A", "user", fixtureHash, "", tt.shared)
			if !maps.Equal(got, tt.want) {
				t.Errorf("NetworkLabels = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestNetworkLabelsAlwaysCarriesExternal pins the key that must exist even
// though 1.0 never fills it: `_delete_link` reads it unguarded, and an absent
// key is a KeyError there.
func TestNetworkLabelsAlwaysCarriesExternal(t *testing.T) {
	for _, shared := range []settings.SharedCollisionDomains{settings.NotShared, settings.SharedBetweenLabs, settings.SharedBetweenUsers} {
		labels := NetworkLabels("A", "user", fixtureHash, "", shared)
		if _, ok := labels["external"]; !ok {
			t.Errorf("shared_cds=%d dropped the external label", shared)
		}
	}
}

// TestExternalLabelIsDeferred: `lab.ext` is post-1.0, so the label is always ""
// and a hand-built external link gets the deferral error rather than a wrong
// label (PACKAGE_GRAPH.md §2.8).
func TestExternalLabelIsDeferred(t *testing.T) {
	lab := model.NewLab("Default scenario", model.DefaultDefaults())
	link := lab.GetOrNewLink("A")

	label, err := externalLabel(link)
	if err != nil || label != "" {
		t.Fatalf("externalLabel with no externals = (%q, %v), want (\"\", nil)", label, err)
	}

	link.External = []model.ExternalLink{{Interface: "eth0"}}
	if _, err := externalLabel(link); !errors.Is(err, kerrors.ErrNotSupported) &&
		kerrors.Code(err) != kerrors.CodeFeatureNotAvailable {
		t.Errorf("externalLabel with an external link = %v, want FeatureNotAvailable", err)
	}
}

// TestNetworkDriverAndPluginName covers the driver reference both the plugin
// lifecycle and the network create build, including the nullable setting whose
// None Python interpolates as the literal "None".
func TestNetworkDriverAndPluginName(t *testing.T) {
	if got := NetworkDriver("kathara/katharanp_vde", "amd64"); got != "kathara/katharanp_vde:amd64" {
		t.Errorf("NetworkDriver = %q", got)
	}

	plugin := "kathara/katharanp"
	if got := pluginNameOf(&settings.Settings{NetworkPlugin: &plugin}); got != plugin {
		t.Errorf("pluginNameOf(set) = %q, want %q", got, plugin)
	}
	if got := pluginNameOf(&settings.Settings{}); got != "None" {
		t.Errorf("pluginNameOf(nil) = %q, want the literal \"None\"", got)
	}
}

// TestBridgeName is `_get_bridge_name`: `kt-` plus the first twelve characters
// of the network id, with a short id returned whole rather than panicking —
// Python's slice does the same.
func TestBridgeName(t *testing.T) {
	if got := BridgeName("0123456789abcdef0123"); got != "kt-0123456789ab" {
		t.Errorf("BridgeName = %q", got)
	}
	if got := BridgeName("abc"); got != "kt-abc" {
		t.Errorf("BridgeName(short) = %q", got)
	}
	if got := BridgeName(""); got != "kt-" {
		t.Errorf("BridgeName(empty) = %q", got)
	}
}

// generateHashFixture keeps the one identity-chain call this file makes in one
// place, so the import of `model` is not needed for it.
func generateHashFixture() string {
	return model.NewLab("Default scenario", model.DefaultDefaults()).Hash
}
