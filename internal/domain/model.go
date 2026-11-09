package domain

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

const SchemaVersion = 1
const CertifiedCore = "0.72.0"

func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func ValidID(s string) bool { b, err := hex.DecodeString(s); return err == nil && len(b) == 16 }

type Profile struct {
	ID            string            `json:"id"`
	Revision      uint64            `json:"revision"`
	Name          string            `json:"name"`
	Group         string            `json:"group,omitempty"`
	Mode          string            `json:"mode"`
	ServerAddr    string            `json:"serverAddr"`
	ServerPort    int               `json:"serverPort"`
	User          string            `json:"user,omitempty"`
	Metadata      map[string]string `json:"-"`
	MetadataRefs  map[string]string `json:"metadataRefs,omitempty"`
	TokenRef      string            `json:"tokenRef,omitempty"`
	CoreID        string            `json:"coreId"`
	CoreVersion   string            `json:"coreVersion,omitempty"`
	AutoConnect   bool              `json:"autoConnect"`
	Transport     string            `json:"transport"`
	ProxyURLRef   string            `json:"proxyUrlRef,omitempty"`
	TLS           bool              `json:"tls"`
	VerifyTLS     bool              `json:"verifyTls"`
	TLSServerName string            `json:"tlsServerName,omitempty"`
	TrustedCA     string            `json:"trustedCA,omitempty"`
	Tunnels       []Tunnel          `json:"tunnels"`
	Visitors      []Visitor         `json:"visitors"`
	RawRef        string            `json:"rawRef,omitempty"`
}

type Tunnel struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Type            string   `json:"type"`
	Enabled         bool     `json:"enabled"`
	LocalIP         string   `json:"localIP"`
	LocalPort       int      `json:"localPort"`
	RemotePort      int      `json:"remotePort,omitempty"`
	Domains         []string `json:"domains,omitempty"`
	Subdomain       string   `json:"subdomain,omitempty"`
	Locations       []string `json:"locations,omitempty"`
	HTTPUser        string   `json:"httpUser,omitempty"`
	HTTPPasswordRef string   `json:"httpPasswordRef,omitempty"`
	SecretRef       string   `json:"secretRef,omitempty"`
	Encryption      bool     `json:"encryption"`
	Compression     bool     `json:"compression"`
}

type Visitor struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	Enabled    bool   `json:"enabled"`
	ServerName string `json:"serverName"`
	ServerUser string `json:"serverUser,omitempty"`
	SecretRef  string `json:"secretRef"`
	BindAddr   string `json:"bindAddr"`
	BindPort   int    `json:"bindPort"`
}

type Core struct {
	ID        string `json:"id"`
	Version   string `json:"version"`
	Path      string `json:"path"`
	Arch      string `json:"arch"`
	SHA256    string `json:"sha256"`
	SourceURL string `json:"sourceUrl,omitempty"`
	Certified bool   `json:"certified"`
	Bundled   bool   `json:"bundled"`
}

type Settings struct {
	Revision               uint64 `json:"revision,omitempty"`
	DefaultCoreID          string `json:"defaultCoreId,omitempty"`
	OpenAtLogin            bool   `json:"openAtLogin"`
	Appearance             string `json:"appearance"`
	DownloadProxyRef       string `json:"downloadProxyRef,omitempty"`
	ConfirmQuit            bool   `json:"confirmQuit"`
	HideNoticeAcknowledged bool   `json:"hideNoticeAcknowledged,omitempty"`
}

type DeletedProfile struct {
	Profile   Profile   `json:"profile"`
	DeletedAt time.Time `json:"deletedAt"`
}
type State struct {
	CoreRevision  uint64            `json:"coreRevision,omitempty"`
	SecretIDs     []string          `json:"secretIds,omitempty"`
	Secrets       map[string][]byte `json:"secrets,omitempty"`
	SchemaVersion int               `json:"schemaVersion"`
	Revision      uint64            `json:"revision"`
	Profiles      []Profile         `json:"profiles"`
	Cores         []Core            `json:"cores"`
	Settings      Settings          `json:"settings"`
	Trash         []DeletedProfile  `json:"trash,omitempty"`
}

func SecretRefs(p Profile) []string {
	refs := []string{p.TokenRef, p.ProxyURLRef}
	for _, ref := range p.MetadataRefs {
		refs = append(refs, ref)
	}
	for _, t := range p.Tunnels {
		refs = append(refs, t.SecretRef, t.HTTPPasswordRef)
	}
	for _, v := range p.Visitors {
		refs = append(refs, v.SecretRef)
	}
	var out []string
	for _, ref := range refs {
		if ref != "" {
			out = append(out, ref)
		}
	}
	return out
}

func NewState() State {
	return State{SchemaVersion: SchemaVersion, Settings: Settings{Appearance: "system", ConfirmQuit: true}}
}
func NewProfile() Profile {
	return Profile{ID: NewID(), Name: "新连接", Mode: "managed", ServerPort: 7000, Transport: "tcp", TLS: false, VerifyTLS: false}
}
func NewTunnel() Tunnel {
	return Tunnel{ID: NewID(), Name: "新隧道", Type: "tcp", Enabled: true, LocalIP: "127.0.0.1"}
}
func NewVisitor() Visitor {
	return Visitor{ID: NewID(), Type: "stcp", Enabled: true, BindAddr: "127.0.0.1"}
}
func (s State) Profile(id string) (Profile, bool) {
	for _, p := range s.Profiles {
		if p.ID == id {
			return p, true
		}
	}
	return Profile{}, false
}
func (s State) Core(id string) (Core, bool) {
	for _, c := range s.Cores {
		if c.ID == id {
			return c, true
		}
	}
	return Core{}, false
}

type ProxyStatus struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Status     string `json:"status"`
	Err        string `json:"err"`
	LocalAddr  string `json:"local_addr"`
	RemoteAddr string `json:"remote_addr"`
}
type Runtime struct {
	ProfileID            string
	Generation           uint64
	Process              string
	Session              string
	SessionObservedAt    time.Time
	SessionError         string
	PID                  int
	AppliedRevision      uint64
	Proxies              []ProxyStatus
	StartedAt            time.Time
	ObservedAt           time.Time
	ObservationStartedAt time.Time
	Error                string
	Operation            string
}

func (r Runtime) Running() bool {
	return r.Process == "running" || r.Process == "starting" || r.Process == "stopping"
}

// ObservationExpired is a deadline for unconfirmed proxy registration, not a failure verdict.
func (r Runtime) ObservationExpired(now time.Time) bool {
	return !r.ObservationStartedAt.IsZero() && now.Sub(r.ObservationStartedAt) >= 15*time.Second
}
func Endpoint(addr string, port int) string { return net.JoinHostPort(addr, strconv.Itoa(port)) }

func Validate(p Profile) (err error) {
	defer func() {
		if err != nil {
			if _, ok := ErrorInfo(err); !ok {
				err = Failure(Validation, err.Error(), "", false, err)
			}
		}
	}()
	if !ValidID(p.ID) {
		return fmt.Errorf("无效的连接标识")
	}
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("请填写连接名称")
	}
	for _, ref := range SecretRefs(p) {
		if !ValidID(ref) {
			return fmt.Errorf("无效的凭证引用")
		}
	}
	if p.Mode == "raw" {
		if !ValidID(p.RawRef) {
			return fmt.Errorf("原文配置缺失")
		}
		return nil
	}
	if p.Mode != "managed" {
		return fmt.Errorf("未知配置模式")
	}
	if p.ServerAddr == "" || strings.ContainsAny(p.ServerAddr, " /\r\n\t") {
		return Failure(Validation, "请填写服务器地址，不要带 http:// 等前缀", "serverAddr", false, nil)
	}
	if !port(p.ServerPort) {
		return Failure(Validation, "服务器端口应在 1–65535 之间", "serverPort", false, nil)
	}
	if !contains([]string{"tcp", "kcp", "quic", "websocket", "wss"}, p.Transport) {
		return fmt.Errorf("不支持的传输协议")
	}
	names := map[string]bool{}
	for _, t := range p.Tunnels {
		if !ValidID(t.ID) || strings.TrimSpace(t.Name) == "" || names[t.Name] {
			return fmt.Errorf("隧道名称不能为空或重复：%s", t.Name)
		}
		names[t.Name] = true
		if !contains([]string{"tcp", "udp", "http", "https", "stcp", "sudp", "xtcp"}, t.Type) {
			return fmt.Errorf("不支持的隧道协议：%s", t.Type)
		}
		if t.LocalIP == "" || !port(t.LocalPort) {
			return fmt.Errorf("隧道 %s：请填写本地地址和有效端口", t.Name)
		}
		if (t.Type == "tcp" || t.Type == "udp") && (t.RemotePort < 0 || t.RemotePort > 65535) {
			return fmt.Errorf("隧道 %s：远程端口必须为 0–65535", t.Name)
		}
		if (t.Type == "http" || t.Type == "https") && len(t.Domains) == 0 && t.Subdomain == "" {
			return fmt.Errorf("隧道 %s：请填写域名或子域名", t.Name)
		}
		if contains([]string{"stcp", "sudp", "xtcp"}, t.Type) && t.SecretRef == "" {
			return fmt.Errorf("隧道 %s：需要私密代理密钥", t.Name)
		}
	}
	for _, v := range p.Visitors {
		if !ValidID(v.ID) || strings.TrimSpace(v.Name) == "" || names[v.Name] {
			return fmt.Errorf("访问者名称不能为空或重复：%s", v.Name)
		}
		names[v.Name] = true
		if !contains([]string{"stcp", "sudp", "xtcp"}, v.Type) || v.ServerName == "" || v.SecretRef == "" || v.BindAddr == "" || !port(v.BindPort) {
			return fmt.Errorf("访问者 %s：请检查协议、服务名称、密钥和监听端口", v.Name)
		}
	}
	return nil
}
func port(n int) bool { return n > 0 && n <= 65535 }
func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
