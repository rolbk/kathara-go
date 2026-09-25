package main

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/KatharaFramework/kathara-go/kathara"
	"github.com/KatharaFramework/kathara-go/model"
)

type linfoManager struct {
	*fakeManager
	machine *kathara.MachineStats
}

func (m *linfoManager) GetMachineStats(context.Context, string, kathara.LabRef, bool) kathara.MachineStatsStream {
	return &linfoMachineStream{machine: m.machine}
}

func (m *linfoManager) UpdateLabFromAPI(_ context.Context, lab *model.Lab) error {
	_, err := lab.GetOrNewMachine("pc1", nil)
	return err
}

type linfoMachineStream struct {
	machine *kathara.MachineStats
	done    bool
}

func (s *linfoMachineStream) Next(context.Context) (*kathara.MachineStats, error) {
	if s.done {
		return nil, io.EOF
	}
	s.done = true
	return s.machine, nil
}

func (s *linfoMachineStream) Close() error { return nil }

func TestLinfoRunningViews(t *testing.T) {
	dir := scenarioDir(t, map[string]string{
		"lab.conf": "LAB_NAME=linfo-test\npc1[0]=A\n",
	})
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"lab", nil, `"mode":"lab"`},
		{"machine", []string{"-n", "pc1"}, `"mode":"machine"`},
		{"topology", []string{"-t"}, `"mode":"topology"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp(t)
			stats := &kathara.MachineStats{Name: "pc1", NetworkScenarioID: "H1", Image: "kathara/base"}
			m := &linfoManager{fakeManager: &fakeManager{stats: []kathara.MachineStatsEntry{{ID: "pc1", Stats: stats}}}, machine: stats}
			withManager(a, m)
			args := append([]string{"--format", "json", "-d", dir}, tc.args...)
			if code := runCommand(t.Context(), a.app, commandTable(a.app)["linfo"], args); code != 0 {
				t.Fatalf("exit = %d, stdout = %s", code, a.stdoutString())
			}
			if !strings.Contains(a.stdoutString(), tc.want) {
				t.Errorf("stdout = %q, want %q", a.stdoutString(), tc.want)
			}
		})
	}
}
