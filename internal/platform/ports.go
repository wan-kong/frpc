package platform

import (
	"net"
	"strconv"
	"strings"
)

type LocalPort struct {
	Process string
	PID     string
	Address string
	Port    int
}

// ParsePorts parses the lsof field output used on Unix.
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
			port, ok := localPort(line[1:])
			if ok {
				address := port.Address
				if !seen[net.JoinHostPort(address, strconv.Itoa(port.Port))] {
					seen[net.JoinHostPort(address, strconv.Itoa(port.Port))] = true
					port.Process, port.PID = process, pid
					out = append(out, port)
				}
			}
		}
	}
	return out
}

// ParseWindowsPorts reads the TCP rows of netstat -ano. A listening socket
// has a foreign port of 0; the state column is not matched, since Windows
// localizes it.
func ParseWindowsPorts(text string, names map[string]string) []LocalPort {
	var out []LocalPort
	seen := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 5 || !strings.EqualFold(f[0], "TCP") {
			continue
		}
		if _, remote, err := net.SplitHostPort(f[2]); err != nil || remote != "0" {
			continue
		}
		port, ok := localPort(f[1])
		if !ok {
			continue
		}
		pid := f[4]
		if _, err := strconv.Atoi(pid); err != nil {
			continue
		}
		key := net.JoinHostPort(port.Address, strconv.Itoa(port.Port))
		if seen[key] {
			continue
		}
		seen[key] = true
		port.PID, port.Process = pid, names[pid]
		out = append(out, port)
	}
	return out
}

// localPort turns a listener address into a local, connectable address.
func localPort(addr string) (LocalPort, bool) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return LocalPort{}, false
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return LocalPort{}, false
	}
	switch host {
	case "*", "0.0.0.0", "":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}
	return LocalPort{Address: host, Port: n}, true
}
