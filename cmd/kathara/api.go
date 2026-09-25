package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"

	"github.com/KatharaFramework/kathara-go/internal/cliout"
	"github.com/KatharaFramework/kathara-go/kathara"
	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/model"
)

// api is the narrow JSON transport for Python manager methods that cannot be
// expressed as ordinary Kathara commands. It is intentionally absent from the
// user-facing command list; the Go Client remains the public in-process API.
func newAPICmd(a *app) *commandSpec {
	cmd := newParser("api")
	registerFormat(cmd, false)
	cmd.pos("deploy-link|undeploy-link|copy-files|retrieve-files|check-image", nargsOne, "Python API operation.")
	return &commandSpec{
		Name: "api", Cmd: cmd,
		Run: func(ctx context.Context, a *app, positional, _ []string) (int, error) {
			if len(positional) != 1 {
				return 2, errUsage("api requires exactly one operation")
			}
			if err := runAPIOperation(ctx, a, positional[0]); err != nil {
				return 1, err
			}
			a.console.Emit(cliout.APIResult{OK: true})
			return 0, nil
		},
	}
}

type apiFile struct {
	GuestPath string `json:"guest_path"`
	HostPath  string `json:"host_path"`
	Content   string `json:"content_base64"`
	IsContent bool   `json:"is_content"`
}

type apiRequest struct {
	LabHash     string               `json:"lab_hash"`
	MachineName string               `json:"machine_name"`
	LinkName    string               `json:"link_name"`
	External    []model.ExternalLink `json:"external"`
	Files       []apiFile            `json:"files"`
	Source      string               `json:"src"`
	Destination string               `json:"dst"`
	ImageName   string               `json:"image_name"`
}

func runAPIOperation(ctx context.Context, a *app, operation string) error {
	switch operation {
	case "deploy-link", "undeploy-link", "copy-files", "retrieve-files", "check-image":
	default:
		return kerrors.New(kerrors.ErrInvocation, "Unknown Python API operation `"+operation+"`.")
	}
	data, err := io.ReadAll(a.stdin)
	if err != nil {
		return err
	}
	var request apiRequest
	if err := json.Unmarshal(data, &request); err != nil {
		return kerrors.WrapValue(err, "Invalid Python API request.")
	}
	if operation != "check-image" && request.LabHash == "" {
		return kerrors.New(kerrors.ErrInvocation, "Python API request is missing lab_hash.")
	}
	if operation == "check-image" && request.ImageName == "" {
		return kerrors.New(kerrors.ErrInvocation, "Python API request is missing image_name.")
	}
	if (operation == "deploy-link" || operation == "undeploy-link") && request.LinkName == "" {
		return kerrors.New(kerrors.ErrInvocation, "Python API request is missing link_name.")
	}
	if (operation == "copy-files" || operation == "retrieve-files") && request.MachineName == "" {
		return kerrors.New(kerrors.ErrInvocation, "Python API request is missing machine_name.")
	}
	mgr, err := a.manager(ctx)
	if err != nil {
		return err
	}
	lab := model.NewLab("", a.defaults())
	lab.Hash = request.LabHash

	switch operation {
	case "check-image":
		return mgr.CheckImage(ctx, request.ImageName)
	case "deploy-link", "undeploy-link":
		link := lab.GetOrNewLink(request.LinkName)
		link.External = request.External
		if operation == "deploy-link" {
			return mgr.DeployLink(ctx, link)
		}
		return mgr.UndeployLink(ctx, link)
	case "copy-files", "retrieve-files":
		machine, err := lab.GetOrNewMachine(request.MachineName, nil)
		if err != nil {
			return err
		}
		obj, err := mgr.GetMachineAPIObject(ctx, machine.Name, kathara.LabRef{Hash: lab.Hash}, false)
		if err != nil {
			return err
		}
		machine.APIObject = obj
		if operation == "retrieve-files" {
			return mgr.RetrieveFiles(ctx, machine, request.Source, request.Destination)
		}
		files := make([]kathara.CopyEntry, 0, len(request.Files))
		for _, file := range request.Files {
			entry := kathara.CopyEntry{GuestPath: file.GuestPath, HostPath: file.HostPath}
			if file.IsContent {
				content, err := base64.StdEncoding.DecodeString(file.Content)
				if err != nil {
					return kerrors.WrapValue(err, "Invalid base64 file content in Python API request.")
				}
				entry.Content = bytes.NewReader(content)
			}
			files = append(files, entry)
		}
		return mgr.CopyFiles(ctx, machine, files)
	}
	return nil
}
