package app

import (
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"

	"frp-desktop/internal/domain"
	"frp-desktop/internal/storage"
)

func (c *Controller) importCA(data []byte) (string, error) {
	if len(data) > 1<<20 {
		return "", fmt.Errorf("CA 文件超过 1MiB 限制")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(data) {
		return "", fmt.Errorf("未找到有效 PEM 证书")
	}
	path := filepath.Join(c.Store.Dir, "attachments", domain.NewID()+".pem")
	if err := storage.AtomicWrite(path, data, 0600); err != nil {
		return "", err
	}
	return path, nil
}
func (c *Controller) ReadCA(path string) ([]byte, error) {
	if filepath.Dir(path) != filepath.Join(c.Store.Dir, "attachments") || !domain.ValidID(pathBaseID(path)) {
		return nil, fmt.Errorf("证书必须由应用导入管理")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) > 1<<20 {
		return nil, fmt.Errorf("CA 文件过大")
	}
	return b, nil
}
func pathBaseID(path string) string {
	base := filepath.Base(path)
	if filepath.Ext(base) != ".pem" {
		return ""
	}
	return base[:len(base)-4]
}
