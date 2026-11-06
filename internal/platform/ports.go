package platform

import (
	"context"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type LocalPort struct {
	Process string
	PID     string
	Address string
	Port    int
}

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
func ParsePorts(text string) []LocalPort {
	var out []LocalPort
	process, pid := "", ""
	seen := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			pid = line[1:]
		case 'c':
			process = line[1:]
		case 'n':
			addr := line[1:]
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				continue
			}
			n, err := strconv.Atoi(port)
			if err != nil || n < 1 || n > 65535 {
				continue
			}
			if host == "*" || host == "0.0.0.0" {
				host = "127.0.0.1"
			} else if host == "::" {
				host = "::1"
			}
			key := net.JoinHostPort(host, port)
			if !seen[key] {
				seen[key] = true
				out = append(out, LocalPort{process, pid, host, n})
			}
		}
	}
	return out
}
