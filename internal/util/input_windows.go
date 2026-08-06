package util

import "golang.org/x/sys/windows"

var (
	modmsvcrt    = windows.NewLazySystemDLL("msvcrt.dll")
	procKbhit    = modmsvcrt.NewProc("_kbhit")
	procGetch    = modmsvcrt.NewProc("_getch")
	procMsvcrtEr = firstError(procKbhit.Find(), procGetch.Find())
)

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// WaitUserInput is utils.wait_user_input_windows (utils.py:172), the Windows
// arm of the device-startup wait probe (DockerMachine.py:935-939):
//
//	return b'\r' in msvcrt.getch() if msvcrt.kbhit() else False
//
// It differs from the Unix arm in two ways, both of them Python's and both
// preserved (PACKAGE_GRAPH.md §4). It **consumes** the keystroke — `_getch`
// removes it from the console buffer, where the Unix `select` leaves it — and
// it only accepts Enter, where the Unix arm treats any readable byte as the
// signal. So on Windows a stray key is swallowed and ignored, while on Unix it
// ends the wait.
//
// `b'\r' in ...` is a substring test over the single byte `_getch` returns,
// i.e. a comparison against carriage return. A function key arrives as two
// calls, of which only the first is consumed here.
func WaitUserInput() (bool, error) {
	if procMsvcrtEr != nil {
		return false, procMsvcrtEr
	}

	pending, _, _ := procKbhit.Call()
	if pending == 0 {
		return false, nil
	}

	ch, _, _ := procGetch.Call()

	return byte(ch) == '\r', nil
}
