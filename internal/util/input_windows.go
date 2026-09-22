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
