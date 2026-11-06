package config

import (
	"bytes"
	"fmt"
	"net"
	"reflect"
	"strconv"
	"strings"

	"frp-desktop/internal/domain"
	"frp-desktop/internal/storage"
	"github.com/pelletier/go-toml/v2"
)

type Document struct {
	ServerAddr    string            `toml:"serverAddr"`
	ServerPort    int               `toml:"serverPort"`
	User          string            `toml:"user,omitempty"`
	Metadatas     map[string]string `toml:"metadatas,omitempty"`
	LoginFailExit bool              `toml:"loginFailExit"`
	Auth          Auth              `toml:"auth"`
	Transport     Transport         `toml:"transport"`
	Log           Log               `toml:"log"`
	WebServer     *Admin            `toml:"webServer,omitempty"`
	Proxies       []Proxy           `toml:"proxies,omitempty"`
	Visitors      []Visitor         `toml:"visitors,omitempty"`
}
type Auth struct {
	Method string `toml:"method,omitempty"`
	Token  string `toml:"token,omitempty"`
}
type TLS struct {
	Enable      bool   `toml:"enable"`
	UseSystemCA bool   `toml:"useSystemCa"`
	ServerName  string `toml:"serverName,omitempty"`
	TrustedCA   string `toml:"trustedCaFile,omitempty"`
}
type Transport struct {
	Protocol string `toml:"protocol"`
	ProxyURL string `toml:"proxyURL,omitempty"`
	TLS      TLS    `toml:"tls"`
}
type Log struct {
	To                string `toml:"to"`
	Level             string `toml:"level"`
	DisablePrintColor bool   `toml:"disablePrintColor"`
}
type Admin struct {
	Addr     string `toml:"addr"`
	Port     int    `toml:"port"`
	User     string `toml:"user"`
	Password string `toml:"password"`
}
type ProxyTransport struct {
	Encryption  bool `toml:"useEncryption"`
	Compression bool `toml:"useCompression"`
}
type Proxy struct {
	Name         string         `toml:"name"`
	Type         string         `toml:"type"`
	LocalIP      string         `toml:"localIP"`
	LocalPort    int            `toml:"localPort"`
	RemotePort   *int           `toml:"remotePort,omitempty"`
	Domains      []string       `toml:"customDomains,omitempty"`
	Subdomain    string         `toml:"subdomain,omitempty"`
	Locations    []string       `toml:"locations,omitempty"`
	HTTPUser     string         `toml:"httpUser,omitempty"`
	HTTPPassword string         `toml:"httpPassword,omitempty"`
	SecretKey    string         `toml:"secretKey,omitempty"`
	Transport    ProxyTransport `toml:"transport"`
}
type Visitor struct {
	Name       string `toml:"name"`
	Type       string `toml:"type"`
	ServerName string `toml:"serverName"`
	ServerUser string `toml:"serverUser,omitempty"`
	SecretKey  string `toml:"secretKey"`
	BindAddr   string `toml:"bindAddr"`
	BindPort   int    `toml:"bindPort"`
}

func Compile(p domain.Profile, secrets storage.Secrets, admin *Admin, includeSecrets bool) ([]byte, error) {
	if err := domain.Validate(p); err != nil {
		return nil, domain.Failure(domain.Validation, err.Error(), "", false, err)
	}
	if p.Mode != "managed" {
		return nil, fmt.Errorf("原文配置需从加密仓储读取")
	}
	get := func(ref string) (string, error) {
		if ref == "" {
			return "", nil
		}
		if !includeSecrets {
			return "<请补充凭证>", nil
		}
		return storage.Secret(secrets, ref)
	}
	token, err := get(p.TokenRef)
	if err != nil {
		return nil, err
	}
	proxyURL, err := get(p.ProxyURLRef)
	if err != nil {
		return nil, err
	}
	metadata := map[string]string{}
	for k, ref := range p.MetadataRefs {
		value, e := get(ref)
		if e != nil {
			return nil, e
		}
		metadata[k] = value
	}
	for k, value := range p.Metadata {
		if !includeSecrets {
			value = "<请补充凭证>"
		}
		metadata[k] = value
	}
	d := Document{ServerAddr: p.ServerAddr, ServerPort: p.ServerPort, User: p.User, Metadatas: metadata, Auth: Auth{Method: "token", Token: token}, Transport: Transport{Protocol: p.Transport, ProxyURL: proxyURL, TLS: TLS{Enable: p.TLS, UseSystemCA: p.VerifyTLS, ServerName: p.TLSServerName, TrustedCA: p.TrustedCA}}, Log: Log{To: "console", Level: "info", DisablePrintColor: true}, WebServer: admin}
	for _, t := range p.Tunnels {
		if !t.Enabled {
			continue
		}
		x := Proxy{Name: t.Name, Type: t.Type, LocalIP: t.LocalIP, LocalPort: t.LocalPort, Transport: ProxyTransport{t.Encryption, t.Compression}}
		switch t.Type {
		case "tcp", "udp":
			n := t.RemotePort
			x.RemotePort = &n
		case "http", "https":
			x.Domains = t.Domains
			x.Subdomain = t.Subdomain
			if t.Type == "http" {
				x.Locations = t.Locations
				x.HTTPUser = t.HTTPUser
				password, err := get(t.HTTPPasswordRef)
				if err != nil {
					return nil, err
				}
				x.HTTPPassword = password
			}
		case "stcp", "sudp", "xtcp":
			secret, err := get(t.SecretRef)
			if err != nil {
				return nil, err
			}
			x.SecretKey = secret
		}
		d.Proxies = append(d.Proxies, x)
	}
	for _, v := range p.Visitors {
		if !v.Enabled {
			continue
		}
		secret, err := get(v.SecretRef)
		if err != nil {
			return nil, err
		}
		d.Visitors = append(d.Visitors, Visitor{Name: v.Name, Type: v.Type, ServerName: v.ServerName, ServerUser: v.ServerUser, SecretKey: secret, BindAddr: v.BindAddr, BindPort: v.BindPort})
	}
	b, err := toml.Marshal(d)
	if err != nil {
		return nil, err
	}
	if !includeSecrets {
		b = append([]byte("# 凭证已省略；补充凭证后才能运行。\n"), b...)
	}
	return b, nil
}

type ImportResult struct {
	Profile    domain.Profile
	Raw        []byte
	Reason     string
	SecretRefs []string
}

func Import(data []byte, name string, secrets storage.Secrets) (out ImportResult, err error) {
	if len(data) > 2<<20 {
		return out, fmt.Errorf("配置超过 2MiB 限制")
	}
	if bytes.Contains(data, []byte("{{")) {
		return out, fmt.Errorf("配置包含环境模板，请先展开为自包含 TOML")
	}
	var tree map[string]any
	if err = toml.Unmarshal(data, &tree); err != nil {
		return out, fmt.Errorf("TOML 格式错误：%w", err)
	}
	if err = checkExternal(tree, ""); err != nil {
		return out, err
	}
	p := domain.NewProfile()
	p.Name = name
	if host, ok := tree["serverAddr"].(string); ok {
		p.ServerAddr = host
	}
	if port, ok := tree["serverPort"].(int64); ok {
		p.ServerPort = int(port)
	}
	d := Document{ServerPort: 7000, Transport: Transport{Protocol: "tcp", TLS: TLS{Enable: true}}}
	decoder := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields()
	if e := decoder.Decode(&d); e != nil {
		p.Mode = "raw"
		p.RawRef = domain.NewID()
		p.ServerAddr, _ = tree["serverAddr"].(string)
		out.Profile = p
		out.Raw = append([]byte(nil), data...)
		out.Reason = "包含可视化编辑器未覆盖的字段，保留原文运行"
		return out, nil
	}
	if d.WebServer != nil || d.Auth.Method != "" && d.Auth.Method != "token" || d.Log.To != "" && d.Log.To != "console" || d.LoginFailExit {
		p.Mode = "raw"
		p.RawRef = domain.NewID()
		p.ServerAddr = d.ServerAddr
		out.Profile = p
		out.Raw = append([]byte(nil), data...)
		out.Reason = "包含自定义运行策略，保留原文运行"
		return out, nil
	}
	defer func() {
		if err != nil {
			for _, ref := range out.SecretRefs {
				_ = secrets.Delete(ref)
			}
		}
	}()
	put := func(value string) (string, error) {
		ref, e := storage.PutSecret(secrets, value)
		if ref != "" {
			out.SecretRefs = append(out.SecretRefs, ref)
		}
		return ref, e
	}
	p.ServerAddr, p.ServerPort, p.User, p.Metadata = d.ServerAddr, d.ServerPort, d.User, d.Metadatas
	p.Transport, p.TLS, p.VerifyTLS, p.TLSServerName = d.Transport.Protocol, d.Transport.TLS.Enable, d.Transport.TLS.UseSystemCA, d.Transport.TLS.ServerName
	if p.TokenRef, err = put(d.Auth.Token); err != nil {
		return out, err
	}
	if p.ProxyURLRef, err = put(d.Transport.ProxyURL); err != nil {
		return out, err
	}
	for _, x := range d.Proxies {
		t := domain.NewTunnel()
		t.Name, t.Type, t.LocalIP, t.LocalPort = x.Name, x.Type, x.LocalIP, x.LocalPort
		if t.LocalIP == "" {
			t.LocalIP = "127.0.0.1"
		}
		if t.Type == "" {
			t.Type = "tcp"
		}
		if x.RemotePort != nil {
			t.RemotePort = *x.RemotePort
		}
		t.Domains, t.Subdomain, t.Locations, t.HTTPUser = x.Domains, x.Subdomain, x.Locations, x.HTTPUser
		t.Encryption, t.Compression = x.Transport.Encryption, x.Transport.Compression
		if t.SecretRef, err = put(x.SecretKey); err != nil {
			return out, err
		}
		if t.HTTPPasswordRef, err = put(x.HTTPPassword); err != nil {
			return out, err
		}
		p.Tunnels = append(p.Tunnels, t)
	}
	for _, x := range d.Visitors {
		v := domain.NewVisitor()
		v.Name, v.Type, v.ServerName, v.ServerUser, v.BindAddr, v.BindPort = x.Name, x.Type, x.ServerName, x.ServerUser, x.BindAddr, x.BindPort
		if v.BindAddr == "" {
			v.BindAddr = "127.0.0.1"
		}
		if v.SecretRef, err = put(x.SecretKey); err != nil {
			return out, err
		}
		p.Visitors = append(p.Visitors, v)
	}
	if err = domain.Validate(p); err != nil {
		return out, err
	}
	out.Profile = p
	return out, nil
}

func checkExternal(v any, path string) error {
	switch x := v.(type) {
	case map[string]any:
		for k, v := range x {
			key := strings.ToLower(k)
			if key == "includes" || key == "assetsdir" || key == "plugin" || key == "tokensource" || strings.HasSuffix(key, "file") {
				return fmt.Errorf("%s%s 包含外部资源或动态配置，首版要求自包含 TOML", path, k)
			}
			if key == "to" {
				if value, ok := v.(string); ok && value != "" && value != "console" {
					return fmt.Errorf("日志必须输出到 console，不能写入外部路径")
				}
			}
			if err := checkExternal(v, path+k+"."); err != nil {
				return err
			}
		}
	case []any:
		for _, v := range x {
			if err := checkExternal(v, path); err != nil {
				return err
			}
		}
	}
	return nil
}

func NeedsRestart(old, next domain.Profile) bool {
	a, b := old, next
	a.Tunnels = nil
	a.Visitors = nil
	b.Tunnels = nil
	b.Visitors = nil
	a.Name = ""
	b.Name = ""
	a.Group = ""
	b.Group = ""
	a.Revision = 0
	b.Revision = 0
	a.AutoConnect = false
	b.AutoConnect = false
	return a.Mode == "raw" || !reflect.DeepEqual(a, b)
}

func RuntimeEquivalent(a, b domain.Profile) bool {
	a.Name = ""
	b.Name = ""
	a.Group = ""
	b.Group = ""
	a.Revision = 0
	b.Revision = 0
	a.AutoConnect = false
	b.AutoConnect = false
	return reflect.DeepEqual(a, b)
}

func ExpandPorts(base domain.Tunnel, local, remote string) ([]domain.Tunnel, error) {
	parse := func(s string) (int, int, error) {
		a := strings.Split(strings.TrimSpace(s), "-")
		if len(a) > 2 {
			return 0, 0, fmt.Errorf("端口范围格式：6000-6010")
		}
		lo, e := strconv.Atoi(a[0])
		if e != nil || lo < 1 || lo > 65535 {
			return 0, 0, fmt.Errorf("无效端口范围")
		}
		hi := lo
		if len(a) == 2 {
			hi, e = strconv.Atoi(a[1])
			if e != nil || hi < lo || hi > 65535 {
				return 0, 0, fmt.Errorf("无效端口范围")
			}
		}
		return lo, hi, nil
	}
	if base.Type != "tcp" && base.Type != "udp" {
		return nil, fmt.Errorf("批量端口只支持 TCP/UDP")
	}
	l, h, e := parse(local)
	if e != nil {
		return nil, e
	}
	r, rh, e := parse(remote)
	if e != nil {
		return nil, e
	}
	if h-l != rh-r || h-l >= 500 {
		return nil, fmt.Errorf("两侧范围长度必须相同，且最多 500 条")
	}
	out := make([]domain.Tunnel, 0, h-l+1)
	for n := 0; n <= h-l; n++ {
		t := base
		t.ID = domain.NewID()
		t.Name = fmt.Sprintf("%s-%d", base.Name, l+n)
		t.LocalPort = l + n
		t.RemotePort = r + n
		out = append(out, t)
	}
	return out, nil
}

func Remote(t domain.Tunnel, p domain.Profile) string {
	if len(t.Domains) > 0 {
		return strings.Join(t.Domains, ", ")
	}
	if t.Subdomain != "" {
		return t.Subdomain
	}
	if t.Type == "tcp" || t.Type == "udp" {
		if t.RemotePort == 0 {
			return "由服务端分配"
		}
		return net.JoinHostPort(p.ServerAddr, strconv.Itoa(t.RemotePort))
	}
	return "私密代理"
}

// ValidateSource checks import policy without writing secrets or files.
func ValidateSource(data []byte) error {
	if len(data) > 2<<20 {
		return fmt.Errorf("配置超过 2MiB 限制")
	}
	if bytes.Contains(data, []byte("{{")) {
		return fmt.Errorf("配置包含环境模板，请先展开")
	}
	var tree map[string]any
	if err := toml.Unmarshal(data, &tree); err != nil {
		return err
	}
	return checkExternal(tree, "")
}
