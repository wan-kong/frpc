package platform

import "testing"

func TestParseListeningPorts(t *testing.T) {
	ports := ParsePorts("p123\ncnode\nn*:3000\nn127.0.0.1:3000\np234\ncserver\nn[::1]:8080\nninvalid\n")
	if len(ports) != 2 || ports[0].Address != "127.0.0.1" || ports[1].Port != 8080 || ports[1].Process != "server" {
		t.Fatal(ports)
	}
}
