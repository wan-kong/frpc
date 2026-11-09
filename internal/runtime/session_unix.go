//go:build !windows

package runtime

import (
	"os/exec"
	"syscall"
)

func processGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

func prepareChild(cmd *exec.Cmd)      { processGroup(cmd) }
func adoptChild(int) func()           { return func() {} }
func signalChild(cmd *exec.Cmd) error { return cmd.Process.Signal(syscall.SIGTERM) }
func killChild(cmd *exec.Cmd) error   { return killGroup(cmd) }
