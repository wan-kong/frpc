package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"frp-desktop/internal/config"
	"frp-desktop/internal/domain"
)

type AdminClient struct {
	Config config.Admin
	Client *http.Client
}

func NewAdmin(a config.Admin) *AdminClient {
	return &AdminClient{Config: a, Client: &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (a *AdminClient) request(ctx context.Context, method, path string) ([]byte, error) {
	if a.Config.Addr != "127.0.0.1" || a.Config.Port < 1 || a.Config.Port > 65535 {
		return nil, fmt.Errorf("管理 API 必须绑定本机回环地址")
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://127.0.0.1:"+strconv.Itoa(a.Config.Port)+path, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(a.Config.User, a.Config.Password)
	resp, err := a.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("管理 API 返回 HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if len(b) > 2<<20 {
		return nil, fmt.Errorf("管理 API 响应过大")
	}
	return b, err
}
func (a *AdminClient) Status(ctx context.Context) ([]domain.ProxyStatus, error) {
	b, err := a.request(ctx, "GET", "/api/status")
	if err != nil {
		return nil, err
	}
	var grouped map[string][]domain.ProxyStatus
	if err = json.Unmarshal(b, &grouped); err != nil {
		return nil, err
	}
	var out []domain.ProxyStatus
	for _, items := range grouped {
		out = append(out, items...)
	}
	return out, nil
}
func (a *AdminClient) Reload(ctx context.Context) error {
	_, err := a.request(ctx, "GET", "/api/reload?strictConfig=true")
	return err
}
