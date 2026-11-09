package runtime

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"frp-desktop/internal/config"
)

func TestAdminAuthenticationAndReloadStrictness(t *testing.T) {
	reloaded := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "test" || p != "password" {
			t.Error("missing auth")
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/api/status":
			_, _ = w.Write([]byte(`{"tcp":[{"name":"ssh","status":"running","remote_addr":"example.net:6000"}]}`))
		case "/api/reload":
			if r.Method != "GET" || r.URL.Query().Get("strictConfig") != "true" {
				t.Error("reload not strict")
			}
			reloaded = true
		}
	}))
	defer server.Close()
	host, port, _ := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	n, _ := strconv.Atoi(port)
	client := NewAdmin(config.Admin{Addr: host, Port: n, User: "test", Password: "password"})
	states, err := client.Status(context.Background())
	if err != nil || len(states) != 1 || states[0].Name != "ssh" {
		t.Fatal(states, err)
	}
	if err = client.Reload(context.Background()); err != nil || !reloaded {
		t.Fatal(err)
	}
}
func TestAdminRejectRedirectAndNonLoopback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.net/", http.StatusFound)
	}))
	defer server.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	n, _ := strconv.Atoi(port)
	a := NewAdmin(config.Admin{Addr: "127.0.0.1", Port: n})
	if _, err := a.Status(context.Background()); err == nil {
		t.Fatal("redirect accepted")
	}
	a.Config.Addr = "192.168.1.1"
	if _, err := a.Status(context.Background()); err == nil {
		t.Fatal("non-loopback accepted")
	}
}
