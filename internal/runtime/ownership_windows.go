//go:build windows

package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

const (
	processQueryLimitedInformation = 0x1000
	processSynchronize             = 0x00100000
	errorInvalidParameter          = syscall.Errno(87)
)

var queryFullProcessImageNameW = syscall.NewLazyDLL("kernel32.dll").NewProc("QueryFullProcessImageNameW")

func openProcess(access uint32, pid int) (syscall.Handle, bool, error) {
	h, err := syscall.OpenProcess(access, false, uint32(pid))
	if err == nil {
		return h, true, nil
	}
	if errors.Is(err, errorInvalidParameter) || errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
		return 0, false, nil
	}
	if errors.Is(err, syscall.ERROR_ACCESS_DENIED) {
		return 0, true, err
	}
	return 0, false, err
}

// identity reads the creation time and executable path of a process, which
// stay the same for its lifetime and survive PID reuse.
func identity(ctx context.Context, pid int) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	h, exists, err := openProcess(processQueryLimitedInformation|processSynchronize, pid)
	if err != nil {
		return "", "", err
	}
	if !exists {
		return "", "", os.ErrProcessDone
	}
	defer syscall.CloseHandle(h)
	if event, err := syscall.WaitForSingleObject(h, 0); err != nil {
		return "", "", err
	} else if event == syscall.WAIT_OBJECT_0 {
		return "", "", os.ErrProcessDone
	}
	var creation, exit, kernel, user syscall.Filetime
	if err = syscall.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return "", "", err
	}
	started := strconv.FormatUint(uint64(creation.HighDateTime)<<32|uint64(creation.LowDateTime), 10)
	buf := make([]uint16, 32768)
	size := uint32(len(buf))
	ok, _, callErr := queryFullProcessImageNameW.Call(uintptr(h), 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if ok == 0 {
		if callErr == nil || callErr == syscall.Errno(0) {
			callErr = errors.New("无法读取进程路径")
		}
		return "", "", callErr
	}
	if size == 0 || int(size) > len(buf) {
		return "", "", errors.New("无法读取进程路径")
	}
	return started, syscall.UTF16ToString(buf[:size]), nil
}

func processExists(pid int) (bool, error) {
	h, exists, err := openProcess(processSynchronize|processQueryLimitedInformation, pid)
	if errors.Is(err, syscall.ERROR_ACCESS_DENIED) {
		return true, nil
	}
	if err != nil || !exists {
		return exists, err
	}
	defer syscall.CloseHandle(h)
	event, err := syscall.WaitForSingleObject(h, 0)
	if err != nil {
		return false, err
	}
	return event == syscall.WAIT_TIMEOUT, nil
}

func terminateProcess(pid int, force bool) error {
	h, exists, err := openProcess(processTerminate, pid)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	defer syscall.CloseHandle(h)
	if err = syscall.TerminateProcess(h, 1); err != nil && !errors.Is(err, errorInvalidParameter) {
		return err
	}
	return nil
}

// commandMatches compares the executable path, since Windows exposes no
// command line through the process API used here. The recorded path must
// live in the managed core directory and the creation time must still match.
func commandMatches(record ownership, command string) bool {
	return strings.EqualFold(filepath.Clean(command), filepath.Clean(record.Binary))
}
