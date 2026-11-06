package app

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"frp-desktop/internal/config"
	"frp-desktop/internal/core"
	"frp-desktop/internal/domain"
	"frp-desktop/internal/logging"
	run "frp-desktop/internal/runtime"
	"frp-desktop/internal/storage"
)

type Controller struct {
	Store         *storage.Store
	Secrets       storage.Secrets
	Runtime       *run.Supervisor
	Vault         *storage.Vault
	mu            sync.Mutex
	coresMu       sync.RWMutex
	commandMu     sync.Mutex
	coreClient    func(string) (*http.Client, error)
	commandKey    [32]byte
	receipts      map[string]*commandReceipt
	commandScopes map[string]chan struct{}
}

func New(store *storage.Store, secrets storage.Secrets, executable string) *Controller {
	c := &Controller{Store: store, Secrets: secrets, Runtime: run.New(store.Dir, secrets, executable), Vault: &storage.Vault{Dir: store.Dir, Secrets: secrets}, receipts: map[string]*commandReceipt{}, commandScopes: map[string]chan struct{}{}}
	if _, err := rand.Read(c.commandKey[:]); err != nil {
		panic(err)
	}
	return c
}
func (c *Controller) Bootstrap(ctx context.Context, resources string) (commandErr error) {
	defer func() { commandErr = commandError(commandErr, domain.BinaryMissing) }()
	co, err := core.Bundled(ctx, resources, c.Store.Dir)
	if err != nil {
		return err
	}
	return c.Store.Update(func(s *domain.State) error {
		for i, old := range s.Cores {
			if old.ID == co.ID {
				s.Cores[i] = co
				return nil
			}
		}
		s.Cores = append(s.Cores, co)
		return nil
	})
}
func (c *Controller) Resolve(p domain.Profile) (domain.Core, error) {
	co, ok := c.Store.Snapshot().Core(p.CoreID)
	if !ok {
		return co, domain.Failure(domain.BinaryMissing, "请先为连接选择已安装内核", "coreId", false, nil)
	}
	return co, nil
}
func (c *Controller) startProfile(ctx context.Context, id string) (commandErr error) {
	defer func() { commandErr = commandError(commandErr, domain.StartFailed) }()
	c.coresMu.RLock()
	defer c.coresMu.RUnlock()
	p, ok := c.Store.Snapshot().Profile(id)
	if !ok {
		return os.ErrNotExist
	}
	co, err := c.Resolve(p)
	if err != nil {
		return err
	}
	if p.TrustedCA != "" {
		if _, err = c.ReadCA(p.TrustedCA); err != nil {
			return err
		}
	}
	return c.Runtime.Start(ctx, p, co)
}
func (c *Controller) applyProfile(ctx context.Context, id string) (commandErr error) {
	defer func() { commandErr = commandError(commandErr, domain.ApplyFailed) }()
	c.coresMu.RLock()
	defer c.coresMu.RUnlock()
	p, ok := c.Store.Snapshot().Profile(id)
	if !ok {
		return os.ErrNotExist
	}
	co, err := c.Resolve(p)
	if err != nil {
		return err
	}
	old, running := c.Runtime.AppliedProfile(id)
	err = c.Runtime.Apply(ctx, p, co)
	if err != nil && running && old.CoreID != p.CoreID {
		current, ok := c.Store.Snapshot().Profile(id)
		if ok && current.Revision == p.Revision {
			current.CoreID = old.CoreID
			restore := c.save(context.Background(), current, nil)
			if restore != nil {
				return fmt.Errorf("应用失败：%v；内核选择恢复失败：%v", err, restore)
			}
			return fmt.Errorf("应用失败，已恢复原内核选择：%w", err)
		}
	}
	return err
}

// Save keeps credentials out of serialized models. An empty value removes the old secret.
// Omitted keys preserve it. Execute owns request identity and profile serialization.
func (c *Controller) save(ctx context.Context, p domain.Profile, values map[string]string) (commandErr error) {
	defer func() { commandErr = commandError(commandErr, domain.StorageFailed) }()
	c.mu.Lock()
	defer c.mu.Unlock()
	if p.TrustedCA != "" {
		if _, err := c.ReadCA(p.TrustedCA); err != nil {
			return err
		}
	}
	var newRefs []string
	committed := false
	defer func() {
		if !committed {
			for _, ref := range newRefs {
				_ = c.Secrets.Delete(ref)
			}
		}
	}()
	set := func(key string, ref *string) error {
		value, ok := values[key]
		if !ok {
			return nil
		}
		if *ref != "" {
			old, err := storage.Secret(c.Secrets, *ref)
			if err != nil {
				return err
			}
			if old == value {
				return nil
			}
		}
		next, err := storage.PutSecret(c.Secrets, value)
		if err != nil {
			return err
		}
		if next != "" {
			newRefs = append(newRefs, next)
		}
		*ref = next
		return nil
	}
	if err := set("token", &p.TokenRef); err != nil {
		return err
	}
	if err := set("proxy", &p.ProxyURLRef); err != nil {
		return err
	}
	for i := range p.Tunnels {
		if err := set("secret/"+p.Tunnels[i].ID, &p.Tunnels[i].SecretRef); err != nil {
			return err
		}
		if err := set("http/"+p.Tunnels[i].ID, &p.Tunnels[i].HTTPPasswordRef); err != nil {
			return err
		}
	}
	for i := range p.Visitors {
		if err := set("secret/"+p.Visitors[i].ID, &p.Visitors[i].SecretRef); err != nil {
			return err
		}
	}
	if p.Metadata != nil {
		next := map[string]string{}
		for k, value := range p.Metadata {
			ref := p.MetadataRefs[k]
			old, e := storage.Secret(c.Secrets, ref)
			if e != nil {
				return e
			}
			if ref != "" && old == value {
				next[k] = ref
				continue
			}
			ref, e = storage.PutSecret(c.Secrets, value)
			if e != nil {
				return e
			}
			next[k] = ref
			if ref != "" {
				newRefs = append(newRefs, ref)
			}
		}
		p.MetadataRefs = next
		p.Metadata = nil
	}
	co, err := c.Resolve(p)
	if err != nil {
		return err
	}
	if _, _, err = c.Runtime.Verify(ctx, p, co); err != nil {
		return err
	}
	p.CoreVersion = co.Version
	if err = c.Store.SaveProfile(p, p.Revision); err != nil {
		return err
	}
	saved, _ := c.Store.Snapshot().Profile(p.ID)
	c.Runtime.AcknowledgeDisplay(saved)
	committed = true

	return nil
}
func (c *Controller) importTOML(ctx context.Context, data []byte, name, profileID string) (string, string, error) {
	out, err := config.Import(data, name, c.Secrets)
	if err != nil {
		return "", "", err
	}
	p := out.Profile
	p.ID = profileID
	state := c.Store.Snapshot()
	for _, co := range state.Cores {
		if co.Certified {
			p.CoreID = co.ID
			break
		}
	}
	if co, ok := state.Core(state.Settings.DefaultCoreID); ok && co.Certified {
		p.CoreID = co.ID
	}
	if p.Mode == "raw" {
		if err = c.Vault.Write(p.RawRef, out.Raw); err != nil {
			return "", "", err
		}
	}
	err = c.save(ctx, p, nil)
	if err != nil {
		for _, ref := range out.SecretRefs {
			_ = c.Secrets.Delete(ref)
		}
		return "", "", err
	}
	return p.ID, out.Reason, nil
}
func (c *Controller) Raw(id string) (string, error) {
	p, ok := c.Store.Snapshot().Profile(id)
	if !ok {
		return "", os.ErrNotExist
	}
	if p.Mode == "raw" {
		b, err := c.Vault.Read(p.RawRef)
		return string(b), err
	}
	b, err := config.Compile(p, c.Secrets, nil, false)
	return string(b), err
}
func (c *Controller) saveRaw(ctx context.Context, p domain.Profile, text string) (commandErr error) {
	defer func() { commandErr = commandError(commandErr, domain.StorageFailed) }()
	result, err := config.Import([]byte(text), p.Name, &storage.MemorySecrets{})
	if err != nil {
		return err
	}
	p.RawRef = domain.NewID()
	p.ServerAddr = result.Profile.ServerAddr
	p.ServerPort = result.Profile.ServerPort
	if err = c.Vault.Write(p.RawRef, []byte(text)); err != nil {
		return err
	}
	return c.save(ctx, p, nil)
}
func (c *Controller) deleteProfile(id string) (commandErr error) {
	defer func() { commandErr = commandError(commandErr, domain.StorageFailed) }()
	if err := c.Runtime.Stop(id); err != nil {
		return err
	}
	return c.Store.DeleteProfile(id)
}
func (c *Controller) Export(id string, includeSecrets bool) ([]byte, error) {
	p, ok := c.Store.Snapshot().Profile(id)
	if !ok {
		return nil, os.ErrNotExist
	}
	if p.Mode == "raw" {
		if !includeSecrets {
			return nil, fmt.Errorf("原文配置无法保证逐字段脱敏，请选择加密备份或明确包含凭证")
		}
		return c.Vault.Read(p.RawRef)
	}
	return config.Compile(p, c.Secrets, nil, includeSecrets)
}
func (c *Controller) addCore(co domain.Core, expected *uint64) error {
	c.coresMu.RLock()
	defer c.coresMu.RUnlock()
	hash, err := core.Hash(co.Path)
	if err != nil {
		return err
	}
	if hash != co.SHA256 {
		return domain.Failure(domain.ChecksumMismatch, "登记前内核内容已改变", "coreId", false, nil)
	}
	return c.Store.Update(func(s *domain.State) error {
		if expected != nil && s.CoreRevision != *expected {
			return storageConflict()
		}
		for i, x := range s.Cores {
			if x.ID == co.ID {
				s.Cores[i] = co
				return nil
			}
		}
		s.Cores = append(s.Cores, co)
		return nil
	})
}

func (c *Controller) setDefaultCore(id string, expected *uint64) (commandErr error) {
	defer func() { commandErr = commandError(commandErr, domain.Validation) }()
	co, ok := c.Store.Snapshot().Core(id)
	if !ok || !co.Certified {
		return fmt.Errorf("新连接默认内核必须是已安装的认证版本")
	}
	return c.Store.Update(func(s *domain.State) error {
		if expected != nil && s.CoreRevision != *expected {
			return storageConflict()
		}
		s.Settings.DefaultCoreID = id
		return nil
	})
}
func (c *Controller) switchCore(ctx context.Context, id, coreID string) (commandErr error) {
	defer func() { commandErr = commandError(commandErr, domain.ApplyFailed) }()
	p, ok := c.Store.Snapshot().Profile(id)
	if !ok {
		return os.ErrNotExist
	}
	p.CoreID = coreID
	if err := c.save(ctx, p, nil); err != nil {
		return err
	}
	return c.applyProfile(ctx, id)
}
func (c *Controller) duplicate(ctx context.Context, id, newID string) (commandErr error) {
	defer func() { commandErr = commandError(commandErr, domain.StorageFailed) }()
	p, ok := c.Store.Snapshot().Profile(id)
	if !ok {
		return os.ErrNotExist
	}
	p.ID = newID
	p.Name += " 副本"
	p.Revision = 0
	p.AutoConnect = false
	for i := range p.Tunnels {
		p.Tunnels[i].ID = domain.NewID()
	}
	for i := range p.Visitors {
		p.Visitors[i].ID = domain.NewID()
	}
	if p.Mode == "raw" {
		data, err := c.Vault.Read(p.RawRef)
		if err != nil {
			return err
		}
		p.RawRef = domain.NewID()
		if err = c.Vault.Write(p.RawRef, data); err != nil {
			return err
		}
	}
	return c.save(ctx, p, nil)
}
func (c *Controller) saveSettings(settings domain.Settings, proxy *string, expected *uint64) (commandErr error) {
	defer func() { commandErr = commandError(commandErr, domain.StorageFailed) }()
	c.mu.Lock()
	defer c.mu.Unlock()
	created := ""
	if proxy != nil {
		if _, err := core.NewClient(*proxy); err != nil {
			return err
		}
		old, err := storage.Secret(c.Secrets, settings.DownloadProxyRef)
		if err != nil {
			return err
		}
		if old != *proxy {
			ref, err := storage.PutSecret(c.Secrets, *proxy)
			if err != nil {
				return err
			}
			settings.DownloadProxyRef = ref
			created = ref
		}
	}
	err := c.Store.Update(func(s *domain.State) error {
		if expected != nil && s.Settings.Revision != *expected {
			return storageConflict()
		}
		settings.DefaultCoreID = s.Settings.DefaultCoreID
		settings.HideNoticeAcknowledged = s.Settings.HideNoticeAcknowledged
		s.Settings = settings
		return nil
	})
	if err != nil && created != "" {
		_ = c.Secrets.Delete(created)
	}
	return err
}

func (c *Controller) WriteResume(ids []string) (commandErr error) {
	defer func() { commandErr = commandError(commandErr, domain.StorageFailed) }()
	b, err := json.Marshal(struct {
		At  time.Time
		IDs []string
	}{time.Now(), ids})
	if err != nil {
		return err
	}
	return storage.AtomicWrite(filepath.Join(c.Store.Dir, "resume.json"), b, 0600)
}
func (c *Controller) ConsumeResume() []string {
	path := filepath.Join(c.Store.Dir, "resume.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if err = os.Remove(path); err != nil {
		return nil
	}
	var data struct {
		At  time.Time
		IDs []string
	}
	if json.Unmarshal(b, &data) != nil || time.Since(data.At) > 10*time.Minute || time.Until(data.At) > time.Minute {
		return nil
	}
	var out []string
	for _, id := range data.IDs {
		if domain.ValidID(id) {
			out = append(out, id)
		}
	}
	return out
}
func (c *Controller) Diagnostics(id string) ([]byte, error) {
	if _, ok := c.Store.Snapshot().Profile(id); !ok {
		return nil, os.ErrNotExist
	}
	lines, err := c.Runtime.Logs(id)
	if err != nil {
		return nil, err
	}
	return c.diagnosticsSelection(id, lines)
}
func (c *Controller) diagnosticsSelection(id string, lines []logging.Line) ([]byte, error) {
	p, ok := c.Store.Snapshot().Profile(id)
	if !ok {
		return nil, os.ErrNotExist
	}
	if len(lines) > 10000 {
		return nil, domain.Failure(domain.Validation, "导出日志超过行数限制", "selection", false, nil)
	}
	size := 0
	for _, l := range lines {
		size += len(l.Text)
	}
	if size > 8<<20 {
		return nil, domain.Failure(domain.Validation, "导出日志超过大小限制", "selection", false, nil)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "frp-desktop diagnostics\n连接：%s\n时间：%s\n", p.Name, time.Now().Format(time.RFC3339))
	for _, l := range lines {
		fmt.Fprintf(&b, "%s [%s] %s\n", l.Time.Format(time.RFC3339Nano), l.Level, logging.Redact(l.Text, nil))
	}
	return []byte(b.String()), nil
}
