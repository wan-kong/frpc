package platform

import "testing"

func TestParseListeningPorts(t *testing.T) {
	ports := ParsePorts("p123\ncnode\nn*:3000\nn127.0.0.1:3000\np234\ncserver\nn[::1]:8080\nninvalid\n")
	if len(ports) != 2 || ports[0].Address != "127.0.0.1" || ports[1].Port != 8080 || ports[1].Process != "server" {
		t.Fatal(ports)
	}
}

func TestParseWindowsPorts(t *testing.T) {
	text := "  Proto  Local Address          Foreign Address        State           PID\n" +
		"  TCP    0.0.0.0:3000           0.0.0.0:0              LISTENING       123\n" +
		"  TCP    127.0.0.1:3000         0.0.0.0:0              LISTENING       123\n" +
		"  TCP    [::1]:8080             [::]:0                 LISTENING       234\n" +
		"  TCP    127.0.0.1:5000         127.0.0.1:6000         ESTABLISHED     345\n"
	ports := ParseWindowsPorts(text, map[string]string{"123": "node.exe", "234": "server.exe"})
	if len(ports) != 2 || ports[0].Address != "127.0.0.1" || ports[0].Process != "node.exe" || ports[1].Port != 8080 || ports[1].Process != "server.exe" {
		t.Fatal(ports)
	}
}
