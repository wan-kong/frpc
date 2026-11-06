package storage

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"frp-desktop/internal/domain"
)

// ErrSecretNotFound reports a missing credential in the config file.
var ErrSecretNotFound = errors.New("secret not found")

type Secrets interface {
	Get(string) ([]byte, error)
	Set(string, []byte) error
	Delete(string) error
}

type MemorySecrets struct {
	mu     sync.Mutex
	Values map[string][]byte
}

func (m *MemorySecrets) Get(k string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.Values[k]
	if !ok {
		return nil, ErrSecretNotFound
	}
	return append([]byte(nil), v...), nil
}
func (m *MemorySecrets) Set(k string, v []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Values == nil {
		m.Values = map[string][]byte{}
	}
	m.Values[k] = append([]byte(nil), v...)
	return nil
}
func (m *MemorySecrets) Delete(k string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.Values, k)
	return nil
}

func PutSecret(s Secrets, value string) (string, error) {
	if value == "" {
		return "", nil
	}
	id := domain.NewID()
	if err := s.Set(id, []byte(value)); err != nil {
		return "", domain.Failure(domain.SecretUnavailable, "无法保存凭证，请检查 ~/.frpc/config.json 的权限和可用空间", "", true, err)
	}
	return id, nil
}
func Secret(s Secrets, ref string) (string, error) {
	if ref == "" {
		return "", nil
	}
	b, err := s.Get(ref)
	if err != nil {
		return "", domain.Failure(domain.SecretUnavailable, "无法读取凭证，请检查 ~/.frpc/config.json 是否存在且完整", "", true, err)
	}
	return string(b), err
}

type Vault struct {
	mu      sync.Mutex
	Dir     string
	Secrets Secrets
}

func (v *Vault) key() ([]byte, error) {
	k, err := v.Secrets.Get("source-master-key")
	if errors.Is(err, ErrSecretNotFound) {
		k = make([]byte, 32)
		if _, err = rand.Read(k); err == nil {
			err = v.Secrets.Set("source-master-key", k)
		}
	}
	if err != nil {
		return nil, err
	}
	if len(k) != 32 {
		return nil, fmt.Errorf("配置主密钥损坏")
	}
	return k, nil
}
func Seal(key, plain, aad []byte) ([]byte, error) {
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	a, err := cipher.NewGCM(b)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return a.Seal(nonce, nonce, plain, aad), nil
}
func Unseal(key, data, aad []byte) ([]byte, error) {
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	a, err := cipher.NewGCM(b)
	if err != nil {
		return nil, err
	}
	if len(data) < a.NonceSize() {
		return nil, fmt.Errorf("加密文件不完整")
	}
	return a.Open(nil, data[:a.NonceSize()], data[a.NonceSize():], aad)
}
func (v *Vault) Write(id string, plain []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !domain.ValidID(id) {
		return fmt.Errorf("无效源文件标识")
	}
	key, err := v.key()
	if err != nil {
		return err
	}
	data, err := Seal(key, plain, []byte(id))
	if err != nil {
		return err
	}
	return AtomicWrite(filepath.Join(v.Dir, "sources", id+".enc"), append([]byte("FRPS1"), data...), 0600)
}
func (v *Vault) Read(id string) ([]byte, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !domain.ValidID(id) {
		return nil, fmt.Errorf("无效源文件标识")
	}
	data, err := os.ReadFile(filepath.Join(v.Dir, "sources", id+".enc"))
	if err != nil {
		return nil, err
	}
	if len(data) < 5 || string(data[:5]) != "FRPS1" {
		return nil, fmt.Errorf("未知加密格式")
	}
	key, err := v.key()
	if err != nil {
		return nil, err
	}
	return Unseal(key, data[5:], []byte(id))
}
