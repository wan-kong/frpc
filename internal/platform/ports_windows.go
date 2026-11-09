//go:build windows

package platform

import (
	"context"
	"encoding/csv"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

func ListeningPorts(ctx context.Context) ([]LocalPort, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "netstat", "-ano", "-p", "tcp").Output()
	if err != nil {
		return nil, fmt.Errorf("未发现可见的 TCP 监听端口，可手动填写")
	}
	return ParseWindowsPorts(string(out), processNames(ctx)), nil
}

func processNames(ctx context.Context) map[string]string {
	names := map[string]string{}
	out, err := exec.CommandContext(ctx, "tasklist", "/FO", "CSV", "/NH").Output()
	if err != nil {
		return names
	}
	records, err := csv.NewReader(strings.NewReader(string(out))).ReadAll()
	if err != nil {
		return names
	}
	for _, record := range records {
		if len(record) < 2 {
			continue
		}
		names[strings.TrimSpace(record[1])] = strings.TrimSpace(record[0])
	}
	return names
}
