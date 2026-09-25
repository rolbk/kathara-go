package main

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/KatharaFramework/kathara-go/kathara"
	"github.com/KatharaFramework/kathara-go/model"
)

type apiFakeManager struct {
	*fakeManager
	link    *model.Link
	machine *model.Machine
	files   []kathara.CopyEntry
	content string
	source  string
	dest    string
	image   string
}

func (f *apiFakeManager) DeployLink(_ context.Context, link *model.Link) error {
	f.link = link
	return nil
}

func (f *apiFakeManager) UndeployLink(_ context.Context, link *model.Link) error {
	f.link = link
	return nil
}

func (f *apiFakeManager) CheckImage(_ context.Context, name string) error {
	f.image = name
	return nil
}

func (f *apiFakeManager) GetMachineAPIObject(_ context.Context, name string, ref kathara.LabRef, allUsers bool) (any, error) {
	if name != "pc1" || ref.Hash != "H1" || allUsers {
		return nil, io.ErrUnexpectedEOF
	}
	return "running-device", nil
}

func (f *apiFakeManager) CopyFiles(_ context.Context, machine *model.Machine, files []kathara.CopyEntry) error {
	f.machine, f.files = machine, files
	if len(files) > 0 && files[0].Content != nil {
		data, err := io.ReadAll(files[0].Content)
		if err != nil {
			return err
		}
		f.content = string(data)
	}
	return nil
}

func (f *apiFakeManager) RetrieveFiles(_ context.Context, machine *model.Machine, src, dst string) error {
	f.machine, f.source, f.dest = machine, src, dst
	return nil
}

func TestPythonAPIBridge(t *testing.T) {
	for _, tc := range []struct{ operation, payload string }{
		{"deploy-link", `{"lab_hash":"H1","link_name":"A","external":[{"interface":"eth0","vlan":20}]}`},
		{"undeploy-link", `{"lab_hash":"H1","link_name":"A"}`},
		{"copy-files", `{"lab_hash":"H1","machine_name":"pc1","files":[{"guest_path":"/x","is_content":true,"content_base64":"aGVsbG8="}]}`},
		{"retrieve-files", `{"lab_hash":"H1","machine_name":"pc1","src":"/x","dst":"/tmp/x"}`},
		{"check-image", `{"image_name":"kathara/base"}`},
	} {
		t.Run(tc.operation, func(t *testing.T) {
			a := newTestApp(t)
			a.stdin = strings.NewReader(tc.payload)
			m := &apiFakeManager{fakeManager: &fakeManager{}}
			withManager(a, m)
			code := runCommand(t.Context(), a.app, commandTable(a.app)["api"],
				[]string{"--format", "json", tc.operation})
			if code != 0 || a.stdoutString() != "{\"ok\":true}\n" {
				t.Fatalf("exit %d, stdout %q", code, a.stdoutString())
			}
			switch tc.operation {
			case "deploy-link":
				if m.link == nil || m.link.Lab.Hash != "H1" || m.link.Name != "A" || m.link.External[0].VLAN != 20 {
					t.Fatalf("link = %+v", m.link)
				}
			case "undeploy-link":
				if m.link == nil || m.link.Name != "A" {
					t.Fatalf("link = %+v", m.link)
				}
			case "copy-files":
				if m.machine == nil || m.machine.APIObject != "running-device" || m.content != "hello" {
					t.Fatalf("machine = %+v, content = %q", m.machine, m.content)
				}
			case "retrieve-files":
				if m.machine == nil || m.source != "/x" || m.dest != "/tmp/x" {
					t.Fatalf("machine = %+v, src = %q, dst = %q", m.machine, m.source, m.dest)
				}
			case "check-image":
				if m.image != "kathara/base" {
					t.Fatalf("image = %q", m.image)
				}
			}
		})
	}
}
