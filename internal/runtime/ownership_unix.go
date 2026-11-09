//go:build !windows

package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func identity(ctx context.Context, pid int) (string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/ps", "-p", strconv.Itoa(pid), "-o", "lstart=", "-o", "stat=", "-o", "command=")
	out, err := cmd.Output()
	if err != nil {
		return "", "", err
	}
	fields := strings.Fields(string(out))
	if len(fields) < 7 {
		return "", "", fmt.Errorf("无法确认进程身份")
	}
	if strings.Contains(fields[5], "Z") {
		return "", "", os.ErrProcessDone
	}
	return strings.Join(fields[:5], " "), strings.Join(fields[6:], " "), nil
}

func processExists(pid int) (bool, error) {
	err := syscall.Kill(pid, 0)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, syscall.ESRCH):
		return false, nil
	case errors.Is(err, syscall.EPERM):
		return true, nil
	default:
		return false, err
	}
}

func terminateProcess(pid int, force bool) error {
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	if err := syscall.Kill(pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

// commandMatches compares the command line ps reports with the one the
// ownership record wrote when the process started.
func commandMatches(record ownership, command string) bool {
	expected := strings.Join(strings.Fields(record.Binary+" -c "+record.Config), " ")
	return command == expected
}
