package app

import (
	"fmt"
	"frp-desktop/internal/domain"
	"strings"
)

func (c *Controller) BackupPreview(blob []byte, password string) (string, error) {
	data, err := decodeBackup(blob, password)
	if err != nil {
		return "", err
	}
	state := c.Store.Snapshot()
	lines := []string{fmt.Sprintf("将新增 %d 个连接，不覆盖现有配置，不自动连接。", len(data.Profiles))}
	for _, p := range data.Profiles {
		if err = domain.Validate(p); err != nil {
			return "", err
		}
		line := fmt.Sprintf("• %s（%d 条隧道，%d 个访问者）", p.Name, len(p.Tunnels), len(p.Visitors))
		if _, ok := state.Core(p.CoreID); !ok {
			line += "；需安装/选择内核 " + p.CoreVersion
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"), nil
}
