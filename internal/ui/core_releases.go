package ui

import (
	"context"
	"frp-desktop/internal/core"
	"frp-desktop/internal/storage"
	"strings"
)

func (a *App) FetchCoreReleases() {
	ref := a.State.Settings.DownloadProxyRef
	var versions []string
	a.CoreTask("读取官方内核版本列表", func(ctx context.Context) error {
		proxy, err := storage.Secret(a.Controller.Secrets, ref)
		if err != nil {
			return err
		}
		client, err := core.NewClient(proxy)
		if err != nil {
			return err
		}
		catalog := core.Catalog{Dir: a.Controller.Store.Dir, Client: client}
		releases, err := catalog.Releases(ctx)
		if err != nil {
			return err
		}
		for _, r := range releases {
			versions = append(versions, strings.TrimPrefix(r.Tag, "v"))
		}
		return nil
	}, func() { a.CoreReleases = versions })
}
