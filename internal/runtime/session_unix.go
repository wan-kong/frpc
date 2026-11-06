package runtime

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// Session is an internal child mode. EOF on stdin means the owning app died.
// It never registers a daemon or opens a control socket.
func Session(args []string) int {
	if len(args) != 2 || !filepath.IsAbs(args[0]) || !filepath.IsAbs(args[1]) {
		fmt.Fprintln(os.Stderr, "invalid frpc session arguments")
		return 2
	}
	cmd := exec.Command(args[0], "-c", args[1])
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=en_US.UTF-8"}
	cmd.Dir = filepath.Dir(args[1])
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	ready := os.NewFile(3, "frpc-started")
	syscall.CloseOnExec(3)
	if err := cmd.Start(); err != nil {
		if ready != nil {
			_, _ = fmt.Fprintln(ready, "error:", err)
			_ = ready.Close()
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if ready != nil {
		_, _ = fmt.Fprintln(ready, cmd.Process.Pid)
		_ = ready.Close()
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	eof := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, os.Stdin); close(eof) }()
	select {
	case err := <-done:
		if err != nil {
			return 1
		}
		return 0
	case <-eof:
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}
	select {
	case <-done:
		return 0
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		return 0
	}
}

func processGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
