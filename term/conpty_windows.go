package term

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// conPty implements Pty over a Windows pseudoconsole.
type conPty struct {
	mu      sync.Mutex
	ws      Winsize
	hpc     windows.Handle // pseudoconsole handle (HPCON)
	inW     *os.File       // parent writes child input here
	outR    *os.File       // parent reads child VT output here
	started bool
	closed  bool
}

var _ Pty = (*conPty)(nil)

func newPty(ws Winsize) (Pty, error) {
	return &conPty{ws: ws}, nil
}

func (p *conPty) Start(cmd *exec.Cmd) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return errClosed
	}
	if p.started {
		return errAlreadyStarted
	}
	if cmd.Path == "" {
		return errors.New("term: conpty: cmd.Path is empty")
	}

	// 1. Two anonymous pipes; the child-facing ends (inR, outW) go to
	// CreatePseudoConsole, which duplicates them, so both are closed before
	// Start returns regardless of outcome.
	var inR, inW, outR, outW windows.Handle
	if err := windows.CreatePipe(&inR, &inW, nil, 0); err != nil {
		return fmt.Errorf("term: conpty: CreatePipe(input): %w", err)
	}
	if err := windows.CreatePipe(&outR, &outW, nil, 0); err != nil {
		closeHandles(inR, inW)
		return fmt.Errorf("term: conpty: CreatePipe(output): %w", err)
	}

	// 2. The pseudoconsole itself, created at the current size — the ConPTY
	// analogue of pty.StartWithSize sizing the slave before the child runs.
	size := windows.Coord{X: int16(p.ws.Cols), Y: int16(p.ws.Rows)}
	var hpc windows.Handle
	if err := windows.CreatePseudoConsole(size, inR, outW, 0, &hpc); err != nil {
		closeHandles(inR, inW, outR, outW)
		return fmt.Errorf("term: conpty: CreatePseudoConsole: %w", err)
	}
	closeHandles(inR, outW) // ConPTY holds duplicates; parent must drop these.

	inWf := os.NewFile(uintptr(inW), "|conpty-in")
	outRf := os.NewFile(uintptr(outR), "|conpty-out")
	fail := func(err error) error {
		windows.ClosePseudoConsole(hpc)
		_ = inWf.Close()
		_ = outRf.Close()
		return err
	}

	// 3. Attribute list carrying the HPCON. Per the Win32 contract the
	// attribute *value* is the HPCON itself (not a pointer to it), passed in
	// the lpValue parameter; x/sys's Update forwards `value` verbatim as
	// lpValue, hence the handle-as-pointer conversion below.
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return fail(fmt.Errorf("term: conpty: NewProcThreadAttributeList: %w", err))
	}
	defer attrs.Delete()
	if err := attrs.Update(
		windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE,
		handleAsPointer(hpc),
		unsafe.Sizeof(hpc),
	); err != nil {
		return fail(fmt.Errorf("term: conpty: attach HPCON: %w", err))
	}

	// 4. CreateProcess with EXTENDED_STARTUPINFO_PRESENT. os/exec cannot
	// carry a pseudoconsole attribute (no SysProcAttr hook), so the child is
	// started directly; cmd supplies Path/Args/Env/Dir and receives Process.
	argv := cmd.Args
	if len(argv) == 0 {
		argv = []string{cmd.Path}
	}
	cmdLine, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(argv))
	if err != nil {
		return fail(fmt.Errorf("term: conpty: command line: %w", err))
	}
	appName, err := windows.UTF16PtrFromString(cmd.Path)
	if err != nil {
		return fail(fmt.Errorf("term: conpty: application name: %w", err))
	}
	var dirp *uint16
	if cmd.Dir != "" {
		if dirp, err = windows.UTF16PtrFromString(cmd.Dir); err != nil {
			return fail(fmt.Errorf("term: conpty: dir: %w", err))
		}
	}
	envp, err := envBlock(cmd.Env)
	if err != nil {
		return fail(fmt.Errorf("term: conpty: environment: %w", err))
	}

	si := new(windows.StartupInfoEx)
	si.Cb = uint32(unsafe.Sizeof(*si))
	si.ProcThreadAttributeList = attrs.List()
	// No STARTF_USESTDHANDLES and inheritHandles=false: the pseudoconsole
	// supplies the child's console handles; nothing else must leak in.
	pi := new(windows.ProcessInformation)
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT)
	if err := windows.CreateProcess(
		appName, cmdLine, nil, nil, false, flags, envp, dirp, &si.StartupInfo, pi,
	); err != nil {
		return fail(fmt.Errorf("term: conpty: CreateProcess %q: %w", cmd.Path, err))
	}
	closeHandles(pi.Thread)

	// Hand the child to the caller through the exec.Cmd, mirroring the Unix
	// contract as far as os/exec allows: cmd.Process.Kill/Wait work;
	// cmd.Wait does NOT (os/exec never observed a Start).
	if proc, findErr := os.FindProcess(int(pi.ProcessId)); findErr == nil {
		cmd.Process = proc
	}
	closeHandles(pi.Process) // cmd.Process holds its own handle

	p.hpc = hpc
	p.inW = inWf
	p.outR = outRf
	p.started = true
	return nil
}

func (p *conPty) Resize(ws Winsize) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return errClosed
	}
	p.ws = ws
	if !p.started {
		// Pre-start: becomes the size passed to CreatePseudoConsole.
		return nil
	}
	size := windows.Coord{X: int16(ws.Cols), Y: int16(ws.Rows)}
	if err := windows.ResizePseudoConsole(p.hpc, size); err != nil {
		return fmt.Errorf("term: conpty: ResizePseudoConsole: %w", err)
	}
	return nil
}

func (p *conPty) Read(b []byte) (int, error) {
	f, err := p.reader()
	if err != nil {
		return 0, err
	}
	return f.Read(b)
}

func (p *conPty) Write(b []byte) (int, error) {
	f, err := p.writer()
	if err != nil {
		return 0, err
	}
	return f.Write(b)
}

func (p *conPty) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	if !p.started {
		return nil
	}
	windows.ClosePseudoConsole(p.hpc)
	return errors.Join(p.inW.Close(), p.outR.Close())
}

func (p *conPty) reader() (*os.File, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, errClosed
	}
	if !p.started {
		return nil, errNotStarted
	}
	return p.outR, nil
}

func (p *conPty) writer() (*os.File, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, errClosed
	}
	if !p.started {
		return nil, errNotStarted
	}
	return p.inW, nil
}

// handleAsPointer reinterprets a kernel handle as an unsafe.Pointer for
// UpdateProcThreadAttribute's lpValue, whose PSEUDOCONSOLE contract wants the
// HPCON *value* in the pointer slot. The round-trip through &h keeps go vet's
// unsafeptr check satisfied; the result is never dereferenced by Go — it is
// marshalled straight back to a pointer-sized integer by the kernel call.
func handleAsPointer(h windows.Handle) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&h))
}

// envBlock renders cmd.Env as a UTF-16 double-NUL-terminated block, or nil
// (inherit parent environment) when env is nil — the os/exec convention.
func envBlock(env []string) (*uint16, error) {
	if env == nil {
		return nil, nil
	}
	var block []uint16
	for _, kv := range env {
		u, err := windows.UTF16FromString(kv) // includes trailing NUL
		if err != nil {
			return nil, fmt.Errorf("invalid environment entry %q: %w", kv, err)
		}
		block = append(block, u...)
	}
	if len(block) == 0 {
		block = append(block, 0) // empty block still needs the string terminator
	}
	block = append(block, 0) // block terminator
	return &block[0], nil
}

func closeHandles(hs ...windows.Handle) {
	for _, h := range hs {
		if h != 0 && h != windows.InvalidHandle {
			_ = windows.CloseHandle(h)
		}
	}
}
