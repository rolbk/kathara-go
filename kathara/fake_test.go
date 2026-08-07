package kathara

import (
	"context"
	"errors"
	"io"

	"github.com/KatharaFramework/kathara-go/model"
)

// This file holds the doubles the tests in this package delegate through.
// They record, they do not simulate: the point of every test here is that
// [Client] passes what it was given, unchanged, to the method it was supposed
// to pass it to — which is the whole of `manager/Kathara.py`.

// call is one recorded invocation.
type call struct {
	method string
	args   []any
}

// fakeManager records every call and answers with canned values. The zero
// value is usable; set the want* fields to control what comes back.
type fakeManager struct {
	calls []call

	// Canned results. err is returned by every method that can fail.
	err error

	wantAPIObject  any
	wantAPIObjects []any
	wantLab        *model.Lab
	wantSession    TTYSession
	wantStream     ExecStream
	wantStdout     []byte
	wantStderr     []byte
	wantExitCode   int
	wantVersion    string
	wantName       string

	wantMachinesStats MachinesStatsStream
	wantMachineStats  MachineStatsStream
	wantLinksStats    LinksStatsStream
	wantLinkStats     LinkStatsStream
}

func (f *fakeManager) record(method string, args ...any) {
	f.calls = append(f.calls, call{method: method, args: args})
}

// last is the most recent recorded call. It reports a zero call when nothing
// has been recorded, which every caller treats as a failure.
func (f *fakeManager) last() call {
	if len(f.calls) == 0 {
		return call{}
	}
	return f.calls[len(f.calls)-1]
}

func (f *fakeManager) DeployMachine(ctx context.Context, machine *model.Machine) error {
	f.record("DeployMachine", ctx, machine)
	return f.err
}

func (f *fakeManager) DeployLink(ctx context.Context, link *model.Link) error {
	f.record("DeployLink", ctx, link)
	return f.err
}

func (f *fakeManager) DeployLab(ctx context.Context, lab *model.Lab, opts DeployLabOptions) error {
	f.record("DeployLab", ctx, lab, opts)
	return f.err
}

func (f *fakeManager) ConnectMachineToLink(ctx context.Context, machine *model.Machine, link *model.Link, macAddress string) error {
	f.record("ConnectMachineToLink", ctx, machine, link, macAddress)
	return f.err
}

func (f *fakeManager) DisconnectMachineFromLink(ctx context.Context, machine *model.Machine, link *model.Link, keepLink bool) error {
	f.record("DisconnectMachineFromLink", ctx, machine, link, keepLink)
	return f.err
}

func (f *fakeManager) UndeployMachine(ctx context.Context, machine *model.Machine, keepLinks bool) error {
	f.record("UndeployMachine", ctx, machine, keepLinks)
	return f.err
}

func (f *fakeManager) UndeployLink(ctx context.Context, link *model.Link) error {
	f.record("UndeployLink", ctx, link)
	return f.err
}

func (f *fakeManager) UndeployLab(ctx context.Context, ref LabRef, opts UndeployLabOptions) error {
	f.record("UndeployLab", ctx, ref, opts)
	return f.err
}

func (f *fakeManager) Wipe(ctx context.Context, allUsers bool) error {
	f.record("Wipe", ctx, allUsers)
	return f.err
}

func (f *fakeManager) ConnectTTY(ctx context.Context, machineName string, ref LabRef, opts ConnectTTYOptions) (TTYSession, error) {
	f.record("ConnectTTY", ctx, machineName, ref, opts)
	return f.wantSession, f.err
}

func (f *fakeManager) ConnectTTYObj(ctx context.Context, machine *model.Machine, opts ConnectTTYOptions) (TTYSession, error) {
	f.record("ConnectTTYObj", ctx, machine, opts)
	return f.wantSession, f.err
}

func (f *fakeManager) Exec(ctx context.Context, machineName string, command Command, ref LabRef, wait WaitPolicy) ([]byte, []byte, int, error) {
	f.record("Exec", ctx, machineName, command, ref, wait)
	return f.wantStdout, f.wantStderr, f.wantExitCode, f.err
}

func (f *fakeManager) ExecObj(ctx context.Context, machine *model.Machine, command Command, wait WaitPolicy) ([]byte, []byte, int, error) {
	f.record("ExecObj", ctx, machine, command, wait)
	return f.wantStdout, f.wantStderr, f.wantExitCode, f.err
}

func (f *fakeManager) ExecStream(ctx context.Context, machineName string, command Command, ref LabRef, wait WaitPolicy) (ExecStream, error) {
	f.record("ExecStream", ctx, machineName, command, ref, wait)
	return f.wantStream, f.err
}

func (f *fakeManager) ExecStreamObj(ctx context.Context, machine *model.Machine, command Command, wait WaitPolicy) (ExecStream, error) {
	f.record("ExecStreamObj", ctx, machine, command, wait)
	return f.wantStream, f.err
}

func (f *fakeManager) CopyFiles(ctx context.Context, machine *model.Machine, files []CopyEntry) error {
	f.record("CopyFiles", ctx, machine, files)
	return f.err
}

func (f *fakeManager) RetrieveFiles(ctx context.Context, machine *model.Machine, src, dst string) error {
	f.record("RetrieveFiles", ctx, machine, src, dst)
	return f.err
}

func (f *fakeManager) GetMachineAPIObject(ctx context.Context, machineName string, ref LabRef, allUsers bool) (any, error) {
	f.record("GetMachineAPIObject", ctx, machineName, ref, allUsers)
	return f.wantAPIObject, f.err
}

func (f *fakeManager) GetMachinesAPIObjects(ctx context.Context, ref LabRef, allUsers bool) ([]any, error) {
	f.record("GetMachinesAPIObjects", ctx, ref, allUsers)
	return f.wantAPIObjects, f.err
}

func (f *fakeManager) GetLinkAPIObject(ctx context.Context, linkName string, ref LabRef, allUsers bool) (any, error) {
	f.record("GetLinkAPIObject", ctx, linkName, ref, allUsers)
	return f.wantAPIObject, f.err
}

func (f *fakeManager) GetLinksAPIObjects(ctx context.Context, ref LabRef, allUsers bool) ([]any, error) {
	f.record("GetLinksAPIObjects", ctx, ref, allUsers)
	return f.wantAPIObjects, f.err
}

func (f *fakeManager) GetLabFromAPI(ctx context.Context, labHash, labName string) (*model.Lab, error) {
	f.record("GetLabFromAPI", ctx, labHash, labName)
	return f.wantLab, f.err
}

func (f *fakeManager) UpdateLabFromAPI(ctx context.Context, lab *model.Lab) error {
	f.record("UpdateLabFromAPI", ctx, lab)
	return f.err
}

func (f *fakeManager) GetMachinesStats(ctx context.Context, ref LabRef, machineName string, allUsers bool) (MachinesStatsStream, error) {
	f.record("GetMachinesStats", ctx, ref, machineName, allUsers)
	return f.wantMachinesStats, f.err
}

func (f *fakeManager) GetMachineStats(ctx context.Context, machineName string, ref LabRef, allUsers bool) MachineStatsStream {
	f.record("GetMachineStats", ctx, machineName, ref, allUsers)
	return f.wantMachineStats
}

func (f *fakeManager) GetMachineStatsObj(ctx context.Context, machine *model.Machine, allUsers bool) (MachineStatsStream, error) {
	f.record("GetMachineStatsObj", ctx, machine, allUsers)
	return f.wantMachineStats, f.err
}

func (f *fakeManager) GetLinksStats(ctx context.Context, ref LabRef, linkName string, allUsers bool) (LinksStatsStream, error) {
	f.record("GetLinksStats", ctx, ref, linkName, allUsers)
	return f.wantLinksStats, f.err
}

func (f *fakeManager) GetLinkStats(ctx context.Context, linkName string, ref LabRef, allUsers bool) LinkStatsStream {
	f.record("GetLinkStats", ctx, linkName, ref, allUsers)
	return f.wantLinkStats
}

func (f *fakeManager) GetLinkStatsObj(ctx context.Context, link *model.Link, allUsers bool) (LinkStatsStream, error) {
	f.record("GetLinkStatsObj", ctx, link, allUsers)
	return f.wantLinkStats, f.err
}

func (f *fakeManager) CheckImage(ctx context.Context, imageName string) error {
	f.record("CheckImage", ctx, imageName)
	return f.err
}

func (f *fakeManager) GetReleaseVersion(ctx context.Context) (string, error) {
	f.record("GetReleaseVersion", ctx)
	return f.wantVersion, f.err
}

func (f *fakeManager) GetFormattedManagerName() string {
	f.record("GetFormattedManagerName")
	return f.wantName
}

// fakeManager satisfies the contract [Client] proxies; if it stops doing so,
// the package stops compiling, which is the cheapest possible check that the
// interface and the facade have not drifted apart.
var _ Manager = (*fakeManager)(nil)

// fakeSession is an inert [TTYSession] used only for pointer identity.
type fakeSession struct{ id string }

func (s *fakeSession) Read([]byte) (int, error)    { return 0, io.EOF }
func (s *fakeSession) Write(p []byte) (int, error) { return len(p), nil }
func (s *fakeSession) Resize(uint16, uint16) error { return nil }
func (s *fakeSession) Close() error                { return nil }

// fakeExecStream is an inert [ExecStream] used only for pointer identity.
type fakeExecStream struct{ id string }

func (s *fakeExecStream) Next(context.Context) ([]byte, []byte, error) { return nil, nil, io.EOF }
func (s *fakeExecStream) ExitCode(context.Context) (int, error)        { return 0, nil }
func (s *fakeExecStream) Close() error                                 { return nil }

// The four inert stats streams, likewise.
type fakeMachinesStats struct{ id string }

func (s *fakeMachinesStats) Next(context.Context) ([]MachineStatsEntry, error) { return nil, io.EOF }
func (s *fakeMachinesStats) Close() error                                      { return nil }

type fakeMachineStats struct{ id string }

func (s *fakeMachineStats) Next(context.Context) (*MachineStats, error) { return nil, io.EOF }
func (s *fakeMachineStats) Close() error                                { return nil }

type fakeLinksStats struct{ id string }

func (s *fakeLinksStats) Next(context.Context) ([]LinkStatsEntry, error) { return nil, io.EOF }
func (s *fakeLinksStats) Close() error                                   { return nil }

type fakeLinkStats struct{ id string }

func (s *fakeLinkStats) Next(context.Context) (*LinkStats, error) { return nil, io.EOF }
func (s *fakeLinkStats) Close() error                             { return nil }

// errBackend is the canned failure the delegation tests thread through, chosen
// so that it is not any taxonomy error and cannot be produced by accident.
var errBackend = errors.New("kathara_test: backend failed")
