package app

import (
	"context"
	"frp-desktop/internal/domain"
	"path/filepath"
	"testing"
)

func TestIntegrationMoveRejectsConflictAtomically(t *testing.T) {
	c := testController(t)
	ctx := context.Background()
	if err := c.Bootstrap(ctx, filepath.Join("..", "..", "resources")); err != nil {
		t.Fatal(err)
	}
	co := c.Store.Snapshot().Cores[0]
	makeProfile := func(name string) domain.Profile {
		p := domain.NewProfile()
		p.Name = name
		p.ServerAddr = "127.0.0.1"
		p.TLS = false
		p.VerifyTLS = false
		p.CoreID = co.ID
		x := domain.NewTunnel()
		x.Name = "same-name"
		x.LocalPort = 8080
		p.Tunnels = []domain.Tunnel{x}
		if err := c.Save(ctx, p, nil, domain.NewID()); err != nil {
			t.Fatal(err)
		}
		saved, _ := c.Store.Snapshot().Profile(p.ID)
		return saved
	}
	a, b := makeProfile("source"), makeProfile("target")
	revision := c.Store.Snapshot().Revision
	if _, err := c.TransformEntries(ctx, []string{a.Tunnels[0].ID}, b.ID, false); err == nil {
		t.Fatal("duplicate name move accepted")
	}
	if c.Store.Snapshot().Revision != revision {
		t.Fatal("failed move changed profiles")
	}
	b.Tunnels[0].Name = "different-name"
	if err := c.Save(ctx, b, nil, domain.NewID()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.TransformEntries(ctx, []string{a.Tunnels[0].ID}, b.ID, false); err != nil {
		t.Fatal(err)
	}
	a, _ = c.Store.Snapshot().Profile(a.ID)
	b, _ = c.Store.Snapshot().Profile(b.ID)
	if len(a.Tunnels) != 0 || len(b.Tunnels) != 2 {
		t.Fatal("move lost or duplicated entries")
	}
}
