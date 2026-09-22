package cliout

import (
	"strings"
	"testing"
	"time"
)

// TestPanelMatchesGoldens pins the two panels every Layer A recording opens
// with, byte for byte, against `test/goldens/*/commands.json`.
func TestPanelMatchesGoldens(t *testing.T) {
	tests := []struct {
		name    string
		message string
		want    []string
	}{
		{
			name:    "starting network scenario",
			message: "Starting Network Scenario",
			want: []string{
				"┌──────────────────────────────────────────────────────────────────────────────┐",
				"│                          Starting Network Scenario                           │",
				"└──────────────────────────────────────────────────────────────────────────────┘",
			},
		},
		{
			name:    "stopping network scenario",
			message: "Stopping Network Scenario",
			want: []string{
				"┌──────────────────────────────────────────────────────────────────────────────┐",
				"│                          Stopping Network Scenario                           │",
				"└──────────────────────────────────────────────────────────────────────────────┘",
			},
		},
		{
			name:    "checking network scenario",
			message: "Checking Network Scenario",
			want: []string{
				"┌──────────────────────────────────────────────────────────────────────────────┐",
				"│                          Checking Network Scenario                           │",
				"└──────────────────────────────────────────────────────────────────────────────┘",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Panel(tc.message, PanelOptions{Justify: JustifyCenter, Width: 80})
			assertLines(t, got, tc.want)
		})
	}
}

// TestLabMetadataPanelMatchesGoldens is the wrap assertion: the author list of
// `07-static-routing` folds after "F. Ricci, " and `Text.rstrip_end` crops the
// one space that overflowed, which is the behaviour `muesli/reflow` does not
// have and the reason text.go exists.
func TestLabMetadataPanelMatchesGoldens(t *testing.T) {
	meta := strings.Join([]string{
		"Description: A simple example showing how to configure static routes",
		"Version: 3.0",
		"Author(s): T. Caiazzi, G. Di Battista, M. Patrignani, M. Pizzonia, F. Ricci, M. Rimondini",
		"Email: contact@kathara.org",
		"Website: http://www.kathara.org/",
	}, "\n")

	want := []string{
		"┌──────────────────────────────────────────────────────────────────────────────┐",
		"│ Description: A simple example showing how to configure static routes         │",
		"│ Version: 3.0                                                                 │",
		"│ Author(s): T. Caiazzi, G. Di Battista, M. Patrignani, M. Pizzonia, F. Ricci, │",
		"│ M. Rimondini                                                                 │",
		"│ Email: contact@kathara.org                                                   │",
		"│ Website: http://www.kathara.org/                                             │",
		"└──────────────────────────────────────────────────────────────────────────────┘",
	}

	assertLines(t, Panel(meta, PanelOptions{Width: 80}), want)
}

// TestSyntheticLabMetaPanel is the `syn-lab-meta` golden, the only recording
// that exercises `LAB_NAME`.
func TestSyntheticLabMetaPanel(t *testing.T) {
	meta := strings.Join([]string{
		"Name: goldenharness-lab-meta",
		"Description: Synthetic scenario exercising every LAB_ metadata key",
		"Version: 1.0",
		"Author(s): Kathara Go golden harness",
		"Email: golden@example.invalid",
		"Website: https://example.invalid/golden",
	}, "\n")

	want := []string{
		"┌──────────────────────────────────────────────────────────────────────────────┐",
		"│ Name: goldenharness-lab-meta                                                 │",
		"│ Description: Synthetic scenario exercising every LAB_ metadata key           │",
		"│ Version: 1.0                                                                 │",
		"│ Author(s): Kathara Go golden harness                                         │",
		"│ Email: golden@example.invalid                                                │",
		"│ Website: https://example.invalid/golden                                      │",
		"└──────────────────────────────────────────────────────────────────────────────┘",
	}

	assertLines(t, Panel(meta, PanelOptions{Width: 80}), want)
}

// TestLongDescriptionWrapsLikeRich covers the two-line descriptions in the
// corpus, where the fold lands mid-sentence at a space.
func TestLongDescriptionWrapsLikeRich(t *testing.T) {
	tests := []struct {
		name string
		meta string
		want []string
	}{
		{
			name: "traffic control",
			meta: "Description: Lab for showing how to use the tc tool to add a fixed delay on a device interface",
			want: []string{
				"│ Description: Lab for showing how to use the tc tool to add a fixed delay on  │",
				"│ a device interface                                                           │",
			},
		},
		{
			name: "web server",
			meta: "Description: A simple lab showing the operation of a web server accessed by a single browser client",
			want: []string{
				"│ Description: A simple lab showing the operation of a web server accessed by  │",
				"│ a single browser client                                                      │",
			},
		},
		{
			name: "ospf",
			meta: "Description: A network showing the operation of the OSPF routing protocol in a simple scenario with a single area",
			want: []string{
				"│ Description: A network showing the operation of the OSPF routing protocol in │",
				"│ a simple scenario with a single area                                         │",
			},
		},
		{
			name: "frr introduction",
			meta: "Description: Experiences with FRRouting configurations and command line interface",
			want: []string{
				"│ Description: Experiences with FRRouting configurations and command line      │",
				"│ interface                                                                    │",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Panel(tc.meta, PanelOptions{Width: 80})
			assertLines(t, got[1:len(got)-1], tc.want)
		})
	}
}

// TestVolumeTreeMatchesGolden is the `syn-volume` recording.
func TestVolumeTreeMatchesGolden(t *testing.T) {
	node := TreeNode{
		Label: "* Device `pc1`",
		Children: []TreeNode{
			{Label: "Host Path: /tmp/kathara-golden-vol-rw -> Device Path: /guest-rw"},
			{Label: "Host Path: /tmp/kathara-golden-vol-ro -> Device Path: /guest-ro"},
		},
	}
	want := []string{
		"* Device `pc1`",
		"├── Host Path: /tmp/kathara-golden-vol-rw -> Device Path: /guest-rw",
		"└── Host Path: /tmp/kathara-golden-vol-ro -> Device Path: /guest-ro",
	}
	assertLines(t, Tree(node, 80), want)
}

// TestWrapFoldsOverlongWords covers `divide_line`'s fold branch, which no
// golden reaches but which a URL in a `LAB_WEB` value would.
func TestWrapFoldsOverlongWords(t *testing.T) {
	got := Wrap("Website: "+strings.Repeat("x", 20), 12, JustifyDefault)
	want := []string{
		"Website:    ",
		"xxxxxxxxxxxx",
		"xxxxxxxx    ",
	}
	assertLines(t, got, want)
}

// TestWrapCenterFloorsTheLeftPad pins `Lines.justify`'s "center" arm: the slack
// is halved with floor, so an odd slack leaves the extra column on the right.
func TestWrapCenterFloorsTheLeftPad(t *testing.T) {
	got := Wrap("ab", 5, JustifyCenter)
	assertLines(t, got, []string{" ab  "})
}

// TestCellLenCountsWideRunesAsTwo guards the one place a non-ASCII scenario
// name would move a fold.
func TestCellLenCountsWideRunesAsTwo(t *testing.T) {
	if got := CellLen("日本"); got != 4 {
		t.Fatalf("CellLen(日本) = %d, want 4", got)
	}
	if got := CellLen("kathara"); got != 7 {
		t.Fatalf("CellLen(kathara) = %d, want 7", got)
	}
}

// TestEmptyBlockMatchesRichGroup covers `create_lab_table`'s "no devices" arm.
func TestEmptyBlockMatchesRichGroup(t *testing.T) {
	got := EmptyBlock("TIMESTAMP: 2026-08-07 12:00:00", "No Devices Found", 80)
	want := []string{
		"                         TIMESTAMP: 2026-08-07 12:00:00",
		"╔══════════════════════════════════════════════════════════════════════════════╗",
		"║                               No Devices Found                               ║",
		"╚══════════════════════════════════════════════════════════════════════════════╝",
	}
	assertLines(t, got, want)
}

// TestTimestampMatchesCPythonStr pins `str(datetime.now())`: microseconds are
// written only when non-zero.
func TestTimestampMatchesCPythonStr(t *testing.T) {
	withMicros := time.Date(2026, 8, 7, 1, 2, 3, 456789000, time.UTC)
	if got, want := Timestamp(withMicros), "TIMESTAMP: 2026-08-07 01:02:03.456789"; got != want {
		t.Fatalf("Timestamp = %q, want %q", got, want)
	}
	whole := time.Date(2026, 8, 7, 1, 2, 3, 0, time.UTC)
	if got, want := Timestamp(whole), "TIMESTAMP: 2026-08-07 01:02:03"; got != want {
		t.Fatalf("Timestamp = %q, want %q", got, want)
	}
}

// TestColumnHeaderMatchesPython is `cli/ui/utils.py:82`.
func TestColumnHeaderMatchesPython(t *testing.T) {
	for in, want := range map[string]string{
		"network_scenario_id": "NETWORK SCENARIO ID",
		"container_name":      "CONTAINER NAME",
		"name":                "NAME",
	} {
		if got := ColumnHeader(in); got != want {
			t.Errorf("ColumnHeader(%q) = %q, want %q", in, got, want)
		}
	}
}

func assertLines(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("line count = %d, want %d\ngot:\n%s\nwant:\n%s",
			len(got), len(want), strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d:\n got %q\nwant %q", i, got[i], want[i])
		}
	}
}
