package platform

import (
	"errors"
	"runtime"
	"syscall"
)

// IsAddrInUse reports whether err is a TCP bind conflict. Windows reports
// WSAEADDRINUSE (10048), which Go does not map to syscall.EADDRINUSE.
func IsAddrInUse(err error) bool {
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	return runtime.GOOS == "windows" && errors.Is(err, syscall.Errno(10048))
}
