package runtime

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"frp-desktop/internal/platform"
)

// Session is an internal child mode. EOF on stdin means the owning app died.
// It never registers a daemon or opens a control socket. The third argument
// is a handshake file that receives the child PID, or an error, once.
func Session(args []string) int {
	if len(args) != 3 || !filepath.IsAbs(args[0]) || !filepath.IsAbs(args[1]) || !filepath.IsAbs(args[2]) {
		fmt.Fprintln(os.Stderr, "invalid frpc session arguments")
		return 2
	}
	cmd := exec.Command(args[0], "-c", args[1])
	cmd.Env = platform.ChildEnv()
	cmd.Dir = filepath.Dir(args[1])
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	prepareChild(cmd)
	if err := cmd.Start(); err != nil {
		reportStarted(args[2], "error: "+err.Error())
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	release := adoptChild(cmd.Process.Pid)
	defer release()
	if !reportStarted(args[2], strconv.Itoa(cmd.Process.Pid)) {
		_ = killChild(cmd)
		_ = cmd.Wait()
		return 1
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
		_ = signalChild(cmd)
	}
	select {
	case <-done:
		return 0
	case <-time.After(5 * time.Second):
		_ = killChild(cmd)
		<-done
		return 0
	}
}

func reportStarted(path, text string) bool {
	return os.WriteFile(path, []byte(text+"\n"), 0600) == nil
}
