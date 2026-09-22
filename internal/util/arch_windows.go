package util

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// The `wProcessorArchitecture` values of SYSTEM_INFO that Windows still ships
// (winnt.h PROCESSOR_ARCHITECTURE_*).
const (
	procArchIntel = 0
	procArchARM   = 5
	procArchIA64  = 6
	procArchAMD64 = 9
	procArchARM64 = 12
)

// systemInfo is SYSTEM_INFO. Only the first field is read; the rest is present
// so the struct is the size GetNativeSystemInfo writes.
type systemInfo struct {
	processorArchitecture     uint16
	reserved                  uint16
	pageSize                  uint32
	minimumApplicationAddress uintptr
	maximumApplicationAddress uintptr
	activeProcessorMask       uintptr
	numberOfProcessors        uint32
	processorType             uint32
	allocationGranularity     uint32
	processorLevel            uint16
	processorRevision         uint16
}

var (
	modkernel32             = windows.NewLazySystemDLL("kernel32.dll")
	procGetNativeSystemInfo = modkernel32.NewProc("GetNativeSystemInfo")
)

// machineName is `platform.machine()` on Windows.
func machineName() string {
	if err := procGetNativeSystemInfo.Find(); err != nil {
		return ""
	}

	var info systemInfo
	// GetNativeSystemInfo returns void and cannot fail.
	_, _, _ = procGetNativeSystemInfo.Call(uintptr(unsafe.Pointer(&info)))

	switch info.processorArchitecture {
	case procArchAMD64:
		return "AMD64"
	case procArchARM64:
		return "ARM64"
	case procArchARM:
		return "ARM"
	case procArchIA64:
		return "ia64"
	case procArchIntel:
		return "x86"
	}

	// PROCESSOR_ARCHITECTURE_UNKNOWN (0xFFFF) and anything newer: CPython's
	// WMI lookup indexes a table and leaves the value empty when it misses.
	return ""
}
