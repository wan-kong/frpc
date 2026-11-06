package app

import (
	"context"
	"fmt"
	"frp-desktop/internal/config"
	"frp-desktop/internal/domain"
)

func (c *Controller) convertToRaw(ctx context.Context, id string) (commandErr error) {
	defer func() { commandErr = commandError(commandErr, domain.StorageFailed) }()
	p, ok := c.Store.Snapshot().Profile(id)
	if !ok {
		return fmt.Errorf("连接不存在")
	}
	if p.Mode == "raw" {
		return nil
	}
	if p.TrustedCA != "" {
		return fmt.Errorf("含证书附件的连接请保留可视化模式；原文模式首版仅支持自包含配置")
	}
	data, err := config.Compile(p, c.Secrets, nil, true)
	if err != nil {
		return err
	}
	if err = config.ValidateSource(data); err != nil {
		return err
	}
	p.Mode = "raw"
	p.RawRef = domain.NewID()
	if err = c.Vault.Write(p.RawRef, data); err != nil {
		return err
	}
	p.TokenRef = ""
	p.ProxyURLRef = ""
	p.Metadata = nil
	p.MetadataRefs = nil
	p.Tunnels = nil
	p.Visitors = nil
	return c.save(ctx, p, nil)
}
