//go:build !windows

package platform

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"time"
)

func ListeningPorts(ctx context.Context) ([]LocalPort, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/sbin/lsof", "-nP", "-iTCP", "-sTCP:LISTEN", "-Fpcn")
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	b, readErr := io.ReadAll(io.LimitReader(pipe, 1<<20))
	_ = pipe.Close()
	err = cmd.Wait()
	if readErr != nil {
		return nil, readErr
	}
	if err != nil && len(b) == 0 {
		return nil, fmt.Errorf("未发现可见的 TCP 监听端口，可手动填写")
	}
	return ParsePorts(string(b)), nil
}
