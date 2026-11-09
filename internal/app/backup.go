package app

import (
	"crypto/rand"
	"encoding/json"
	"fmt"

	"frp-desktop/internal/config"
	"frp-desktop/internal/domain"
	run "frp-desktop/internal/runtime"
	"frp-desktop/internal/storage"
	"golang.org/x/crypto/scrypt"
)

type backup struct {
	Version     int
	Profiles    []domain.Profile
	Secrets     map[string]string
	Sources     map[string][]byte
	Attachments map[string][]byte
}

func (c *Controller) backup(password string, expected *uint64) ([]byte, error) {
	if len([]rune(password)) < 10 {
		return nil, fmt.Errorf("备份口令至少需要 10 个字符")
	}
	data := backup{Version: 1, Profiles: c.Store.Snapshot().Profiles, Secrets: map[string]string{}, Sources: map[string][]byte{}, Attachments: map[string][]byte{}}
	budget := 0
	for _, p := range data.Profiles {
		if p.TrustedCA != "" {
			b, err := c.ReadCA(p.TrustedCA)
			if err != nil {
				return nil, err
			}
			budget += len(b)
			if budget > 12<<20 {
				return nil, fmt.Errorf("备份内容超过大小限制")
			}
			data.Attachments[p.TrustedCA] = b
		}
		for _, ref := range run.SecretRefs(p) {
			value, err := storage.Secret(c.Secrets, ref)
			if err != nil {
				return nil, err
			}
			budget += len(value)
			if budget > 12<<20 {
				return nil, fmt.Errorf("备份内容超过大小限制")
			}
			data.Secrets[ref] = value
		}
		if p.Mode == "raw" {
			raw, err := c.Vault.Read(p.RawRef)
			if err != nil {
				return nil, err
			}
			budget += len(raw)
			if budget > 12<<20 {
				return nil, fmt.Errorf("备份内容超过大小限制")
			}
			data.Sources[p.RawRef] = raw
		}
	}
	b, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	if len(b) > 16<<20 {
		return nil, fmt.Errorf("备份超过 16MiB 限制")
	}
	salt := make([]byte, 16)
	if _, err = rand.Read(salt); err != nil {
		return nil, err
	}
	key, err := scrypt.Key([]byte(password), salt, 32768, 8, 1, 32)
	if err != nil {
		return nil, err
	}
	sealed, err := storage.Seal(key, b, []byte("FRPB1"))
	if err != nil {
		return nil, err
	}
	return append(append([]byte("FRPB1"), salt...), sealed...), nil
}
func decodeBackup(blob []byte, password string) (backup, error) {
	if len(blob) < 21 || len(blob) > 20<<20 || string(blob[:5]) != "FRPB1" {
		return backup{}, fmt.Errorf("不是受支持的备份文件")
	}
	key, err := scrypt.Key([]byte(password), blob[5:21], 32768, 8, 1, 32)
	if err != nil {
		return backup{}, err
	}
	plain, err := storage.Unseal(key, blob[21:], []byte("FRPB1"))
	if err != nil {
		return backup{}, fmt.Errorf("口令错误或备份文件已损坏")
	}
	var data backup
	if err = json.Unmarshal(plain, &data); err != nil {
		return backup{}, err
	}
	if data.Version != 1 || len(data.Profiles) > 1000 {
		return backup{}, fmt.Errorf("不支持的备份版本或配置数量")
	}
	for _, p := range data.Profiles {
		if p.Mode == "raw" {
			raw, ok := data.Sources[p.RawRef]
			if !ok {
				return backup{}, fmt.Errorf("备份缺少原文配置")
			}
			if err = config.ValidateSource(raw); err != nil {
				return backup{}, fmt.Errorf("备份原文不符合自包含配置要求：%w", err)
			}
		}
	}
	return data, nil
}

func (c *Controller) restoreBackup(blob []byte, password string, createdIDs *[]string) (commandErr error) {
	defer func() { commandErr = commandError(commandErr, domain.StorageFailed) }()
	data, err := decodeBackup(blob, password)
	if err != nil {
		return err
	}
	var created []string
	committed := false
	defer func() {
		if !committed {
			for _, ref := range created {
				_ = c.Secrets.Delete(ref)
			}
		}
	}()
	needed := map[string]bool{}
	for _, p := range data.Profiles {
		if err = domain.Validate(p); err != nil {
			return err
		}
		for _, ref := range domain.SecretRefs(p) {
			needed[ref] = true
		}
	}
	if len(needed) > 10000 || len(data.Secrets) > 10000 {
		return fmt.Errorf("备份凭证数量超过限制")
	}
	refs := map[string]string{}
	for old := range needed {
		value, ok := data.Secrets[old]
		if !ok {
			return fmt.Errorf("备份缺少配置引用的凭证")
		}
		if len(value) > 2560 {
			return fmt.Errorf("备份中的单项凭证超过大小限制")
		}
		ref, err := storage.PutSecret(c.Secrets, value)
		if err != nil {
			return err
		}
		refs[old] = ref
		if ref != "" {
			created = append(created, ref)
		}
	}
	remap := func(ref *string) error {
		if *ref == "" {
			return nil
		}
		next, ok := refs[*ref]
		if !ok {
			return fmt.Errorf("备份缺少凭证")
		}
		*ref = next
		return nil
	}
	for i := range data.Profiles {
		p := &data.Profiles[i]
		p.ID = domain.NewID()
		p.Revision = 1
		p.Name += "（已恢复）"
		p.AutoConnect = false

		if p.TrustedCA != "" {
			b, ok := data.Attachments[p.TrustedCA]
			if !ok {
				return fmt.Errorf("备份缺少证书附件")
			}
			p.TrustedCA, err = c.importCA(b)
			if err != nil {
				return err
			}
		}
		for k, ref := range p.MetadataRefs {
			if err = remap(&ref); err != nil {
				return err
			}
			p.MetadataRefs[k] = ref
		}
		if err = remap(&p.TokenRef); err != nil {
			return err
		}
		if err = remap(&p.ProxyURLRef); err != nil {
			return err
		}
		for j := range p.Tunnels {
			p.Tunnels[j].ID = domain.NewID()
			if err = remap(&p.Tunnels[j].SecretRef); err != nil {
				return err
			}
			if err = remap(&p.Tunnels[j].HTTPPasswordRef); err != nil {
				return err
			}
		}
		for j := range p.Visitors {
			p.Visitors[j].ID = domain.NewID()
			if err = remap(&p.Visitors[j].SecretRef); err != nil {
				return err
			}
		}
		if p.Mode == "raw" {
			raw, ok := data.Sources[p.RawRef]
			if !ok {
				return fmt.Errorf("备份缺少原文配置")
			}
			p.RawRef = domain.NewID()
			if err = c.Vault.Write(p.RawRef, raw); err != nil {
				return err
			}
		}
		if err = domain.Validate(*p); err != nil {
			return err
		}
	}
	err = c.Store.Update(func(s *domain.State) error { s.Profiles = append(s.Profiles, data.Profiles...); return nil })
	committed = err == nil
	if committed && createdIDs != nil {
		for _, p := range data.Profiles {
			*createdIDs = append(*createdIDs, p.ID)
		}
	}
	return err
}
