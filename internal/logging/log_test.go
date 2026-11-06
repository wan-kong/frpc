package logging

import (
	"strings"
	"testing"
)

func TestBoundedReadContinuesAfterLongLine(t *testing.T) {
	b := New("", []string{"very-sensitive"})
	b.Read(strings.NewReader(strings.Repeat("x", 100000) + "\n[error] token=very-sensitive\nnext line\n"))
	lines, _ := b.Snapshot()
	if len(lines) != 3 || lines[2].Text != "next line" {
		t.Fatalf("reader stopped: %d", len(lines))
	}
	for _, l := range lines {
		if strings.Contains(l.Text, "very-sensitive") {
			t.Fatal("secret leaked")
		}
	}
	for i := 0; i < 11000; i++ {
		b.Add("line")
	}
	lines, _ = b.Snapshot()
	if len(lines) != 10000 {
		t.Fatal("unbounded buffer")
	}
}
func TestRedactURLAndHeaders(t *testing.T) {
	s := Redact("https://user:pass@example.net Authorization: Bearer-secret password=abc", nil)
	for _, secret := range []string{"user:pass", "Bearer-secret", "abc"} {
		if strings.Contains(s, secret) {
			t.Fatal(s)
		}
	}
}

func TestStandardAuthorizationHeader(t *testing.T) {
	for _, scheme := range []string{"Bearer", "Basic"} {
		s := Redact("Authorization: "+scheme+" synthetic-secret\nnext line", nil)
		if strings.Contains(s, "synthetic-secret") {
			t.Fatal(s)
		}
		if !strings.Contains(s, "next line") {
			t.Fatal("redacted unrelated line")
		}
	}
}

func TestFRPShortLogLevels(t *testing.T) {
	b := New("", nil)
	b.Add("2026/11/05 [W] [client/service.go:100] connect to server error: no such host")
	b.Add("2026/11/05 [E] [client/proxy.go:100] failed")
	lines, _ := b.Snapshot()
	if lines[0].Level != "warn" || lines[1].Level != "error" {
		t.Fatal(lines)
	}
}

func TestOverlappingSecretsAndDynamicAuthorization(t *testing.T) {
	text := "Authorization: Bearer dynamic-credential\nproxy rejected abcdef"
	redacted := Redact(text, []string{"a", "abcdef"})
	if strings.Contains(redacted, "bcdef") || strings.Contains(redacted, "dynamic-credential") {
		t.Fatal(redacted)
	}
}
