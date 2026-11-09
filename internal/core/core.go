package core

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/macho"
	"debug/pe"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"frp-desktop/internal/domain"
	"frp-desktop/internal/platform"
	"frp-desktop/internal/storage"
	"github.com/egoist/mygo/netproxy"
)

var versionRE = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
var bundledSHA = map[string]string{
	"darwin-arm64":  "92f72de7473ca7918f9381e4e467495f4047413f706ee4756582a90706563980",
	"darwin-amd64":  "64b7422a5a497191aa96b509eae3c6acedfd24ba163aebea25ddce444f2ea5f4",
	"windows-amd64": "b8580b916ecb2e27ecc348bc67bc8b93b0e65266f85fd5d703e2f2f69862b089",
}

// BinaryName is the managed frpc executable's file name on this system.
func BinaryName() string {
	if runtime.GOOS == "windows" {
		return "frpc.exe"
	}
	return "frpc"
}

func platformKey() string { return runtime.GOOS + "-" + runtime.GOARCH }

// executable reports whether mode lets the user run a kernel on this system.
// Windows has no execute bit and relies on the file extension instead.
func executable(mode os.FileMode) bool {
	return runtime.GOOS == "windows" || mode&0111 != 0
}

// validateFormat rejects a kernel built for another system or architecture.
func validateFormat(path string) error {
	switch runtime.GOOS {
	case "darwin":
		f, err := macho.Open(path)
		if err != nil {
			return fmt.Errorf("不是有效的 macOS 内核：%w", err)
		}
		defer f.Close()
		expected := macho.CpuArm64
		if runtime.GOARCH == "amd64" {
			expected = macho.CpuAmd64
		}
		if f.Cpu != expected {
			return fmt.Errorf("内核架构与当前设备不符")
		}
	case "windows":
		f, err := pe.Open(path)
		if err != nil {
			return fmt.Errorf("不是有效的 Windows 内核：%w", err)
		}
		defer f.Close()
		expected, ok := map[string]uint16{
			"amd64": pe.IMAGE_FILE_MACHINE_AMD64,
			"arm64": pe.IMAGE_FILE_MACHINE_ARM64,
			"386":   pe.IMAGE_FILE_MACHINE_I386,
		}[runtime.GOARCH]
		if !ok {
			return fmt.Errorf("不支持的内核架构 %s", runtime.GOARCH)
		}
		if f.Machine != expected {
			return fmt.Errorf("内核架构与当前设备不符")
		}
	default:
		return fmt.Errorf("当前平台不支持可视化内核管理")
	}
	return nil
}

func Hash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func Inspect(ctx context.Context, path string) (domain.Core, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return domain.Core{}, err
	}
	st, err := os.Stat(path)
	if err != nil {
		return domain.Core{}, err
	}
	if !st.Mode().IsRegular() || !executable(st.Mode()) {
		return domain.Core{}, fmt.Errorf("请选择可执行的 frpc 文件")
	}
	if err = validateFormat(path); err != nil {
		return domain.Core{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	cmd.Env = platform.ChildEnv()
	out := &limitWriter{max: 4096}
	cmd.Stdout = out
	cmd.Stderr = out
	if err = cmd.Run(); err != nil {
		return domain.Core{}, fmt.Errorf("无法读取内核版本：%w", err)
	}
	version := strings.TrimSpace(out.s)
	if !versionRE.MatchString(version) {
		return domain.Core{}, fmt.Errorf("内核未返回受支持的版本号")
	}
	hash, err := Hash(path)
	if err != nil {
		return domain.Core{}, err
	}
	return domain.Core{ID: domain.NewID(), Version: version, Path: path, Arch: runtime.GOARCH, SHA256: hash}, nil
}

type limitWriter struct {
	max int
	s   string
}

func (w *limitWriter) Write(b []byte) (int, error) {
	if len(w.s) < w.max {
		n := min(len(b), w.max-len(w.s))
		w.s += string(b[:n])
	}
	return len(b), nil
}

func Bundled(ctx context.Context, resources, dir string) (domain.Core, error) {
	src := filepath.Join(resources, "bin", "frpc.gz")
	if _, err := os.Stat(src); err != nil {
		src = filepath.Join(resources, platformKey(), "bin", "frpc.gz")
	}
	f, err := os.Open(src)
	if err != nil {
		return domain.Core{}, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return domain.Core{}, err
	}
	defer gz.Close()
	b, err := io.ReadAll(io.LimitReader(gz, 80<<20))
	if err != nil {
		return domain.Core{}, err
	}
	sum := sha256.Sum256(b)
	hash := hex.EncodeToString(sum[:])
	expected, ok := bundledSHA[platformKey()]
	if !ok {
		return domain.Core{}, fmt.Errorf("此平台没有内置保底内核")
	}
	if hash != expected {
		return domain.Core{}, domain.Failure(domain.ChecksumMismatch, "保底内核摘要校验失败", "coreId", false, nil)
	}
	id := "bundled-" + domain.CertifiedCore + "-" + runtime.GOARCH
	dest := filepath.Join(dir, "cores", id, BinaryName())
	existing, _ := Hash(dest)
	if existing != hash {
		if err = storage.AtomicWrite(dest, b, 0700); err != nil {
			return domain.Core{}, err
		}
	}
	c, err := Inspect(ctx, dest)
	if err != nil {
		return domain.Core{}, err
	}
	c.ID = id
	c.Bundled = true
	c.Certified = true
	return c, nil
}

type Release struct {
	Tag        string  `json:"tag_name"`
	Prerelease bool    `json:"prerelease"`
	Draft      bool    `json:"draft"`
	Assets     []Asset `json:"assets"`
}
type Asset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}
type Catalog struct {
	Dir    string
	Client *http.Client
}

func NewClient(proxy string) (*http.Client, error) {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = netproxy.Proxy
	if proxy != "" {
		u, err := url.Parse(proxy)
		if err != nil || u.Host == "" || !strings.Contains("|http|https|socks5|socks5h|", "|"+u.Scheme+"|") {
			return nil, fmt.Errorf("无效下载代理")
		}
		t.Proxy = http.ProxyURL(u)
	}
	return &http.Client{Transport: t, Timeout: 5 * time.Minute, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) > 5 || r.URL.Scheme != "https" {
			return fmt.Errorf("不安全的下载重定向")
		}
		return nil
	}}, nil
}
func (c *Catalog) get(ctx context.Context, u string, maxBytes int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "frp-desktop")
	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("下载失败：HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if int64(len(b)) > maxBytes {
		return nil, fmt.Errorf("下载超过大小限制")
	}
	return b, err
}
func (c *Catalog) Releases(ctx context.Context) ([]Release, error) {
	b, err := c.get(ctx, "https://api.github.com/repos/fatedier/frp/releases?per_page=30", 4<<20)
	if err != nil {
		return nil, err
	}
	var rs []Release
	if err = json.Unmarshal(b, &rs); err != nil {
		return nil, err
	}
	var out []Release
	for _, r := range rs {
		if !r.Draft && !r.Prerelease && versionRE.MatchString(strings.TrimPrefix(r.Tag, "v")) {
			out = append(out, r)
		}
	}
	return out, nil
}
func (c *Catalog) Install(ctx context.Context, version string) (domain.Core, error) {
	if !versionRE.MatchString(version) {
		return domain.Core{}, fmt.Errorf("版本格式应为 0.72.0")
	}
	b, err := c.get(ctx, "https://api.github.com/repos/fatedier/frp/releases/tags/v"+version, 2<<20)
	if err != nil {
		return domain.Core{}, err
	}
	var r Release
	if err = json.Unmarshal(b, &r); err != nil {
		return domain.Core{}, err
	}
	if r.Draft || r.Prerelease {
		return domain.Core{}, fmt.Errorf("仅支持稳定版本")
	}
	name := "frp_" + version + "_" + runtime.GOOS + "_" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		name += ".zip"
	} else {
		name += ".tar.gz"
	}
	var asset, checks Asset
	for _, a := range r.Assets {
		if a.Name == name {
			asset = a
		}
		if a.Name == "frp_sha256_checksums.txt" {
			checks = a
		}
	}
	if asset.URL == "" || checks.URL == "" {
		return domain.Core{}, fmt.Errorf("该版本缺少本机资产或官方校验文件")
	}
	check, err := c.get(ctx, checks.URL, 1<<20)
	if err != nil {
		return domain.Core{}, err
	}
	expected := ""
	for _, line := range strings.Split(string(check), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			expected = f[0]
		}
	}
	if len(expected) != 64 {
		return domain.Core{}, fmt.Errorf("官方校验文件未包含目标资产")
	}
	if asset.Digest != "" && asset.Digest != "sha256:"+expected {
		return domain.Core{}, domain.Failure(domain.ChecksumMismatch, "官方摘要信息不一致", "coreId", false, nil)
	}
	archive, err := c.get(ctx, asset.URL, 80<<20)
	if err != nil {
		return domain.Core{}, err
	}
	sum := sha256.Sum256(archive)
	if hex.EncodeToString(sum[:]) != expected {
		return domain.Core{}, domain.Failure(domain.ChecksumMismatch, "内核下载摘要不匹配", "coreId", false, nil)
	}
	bin, license, err := ExtractArchive(name, archive)
	if err != nil {
		return domain.Core{}, err
	}
	return c.installVerified(ctx, version, asset.URL, bin, license)
}

func (c *Catalog) installVerified(ctx context.Context, version, source string, bin, license []byte) (domain.Core, error) {
	root := filepath.Join(c.Dir, "cores")
	if err := os.MkdirAll(root, 0700); err != nil {
		return domain.Core{}, err
	}
	stage, err := os.MkdirTemp(root, ".install-")
	if err != nil {
		return domain.Core{}, err
	}
	defer os.RemoveAll(stage)
	stagedBinary := filepath.Join(stage, BinaryName())
	if err = storage.AtomicWrite(stagedBinary, bin, 0700); err != nil {
		return domain.Core{}, err
	}
	if err = storage.AtomicWrite(filepath.Join(stage, "LICENSE"), license, 0600); err != nil {
		return domain.Core{}, err
	}
	co, err := Inspect(ctx, stagedBinary)
	if err != nil {
		return domain.Core{}, err
	}
	if co.Version != version {
		return domain.Core{}, fmt.Errorf("内核版本与下载标签不符")
	}
	if err = ctx.Err(); err != nil {
		return domain.Core{}, err
	}
	id := "official-" + version + "-" + runtime.GOARCH
	finalDir := filepath.Join(root, id)
	dest := filepath.Join(finalDir, BinaryName())
	existing := func() error {
		dirInfo, err := os.Lstat(finalDir)
		if err != nil {
			return err
		}
		binInfo, err := os.Lstat(dest)
		if err != nil {
			return err
		}
		if !dirInfo.IsDir() || dirInfo.Mode()&os.ModeSymlink != 0 || !binInfo.Mode().IsRegular() || !executable(binInfo.Mode()) {
			return fmt.Errorf("已有内核路径或可执行权限无效，未覆盖；请检查内核库文件")
		}
		hash, err := Hash(dest)
		if err != nil {
			return err
		}
		if hash != co.SHA256 {
			return domain.Failure(domain.ChecksumMismatch, "此版本已安装的内容不同；内核不可变，未覆盖原文件", "coreId", false, nil)
		}
		return nil
	}
	if _, err = os.Lstat(finalDir); err == nil {
		if err = existing(); err != nil {
			return domain.Core{}, err
		}
	} else if !os.IsNotExist(err) {
		return domain.Core{}, err
	} else if err = os.Rename(stage, finalDir); err != nil {
		// A concurrent installer may have committed the identical immutable version.
		if matchErr := existing(); matchErr != nil {
			return domain.Core{}, fmt.Errorf("无法原子安装内核：%w", err)
		}
	}
	co.ID, co.Path, co.SourceURL = id, dest, source
	co.Certified = version == domain.CertifiedCore
	return co, nil
}

// ExtractArchive reads a release archive, which is a .zip on Windows and a
// .tar.gz elsewhere.
func ExtractArchive(name string, archive []byte) (bin, license []byte, err error) {
	if strings.HasSuffix(strings.ToLower(name), ".zip") {
		return ExtractZip(archive)
	}
	return Extract(archive)
}

func Extract(archive []byte) (bin, license []byte, err error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(io.LimitReader(gz, 200<<20))
	total := int64(0)
	for n := 0; ; n++ {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, nil, e
		}
		clean := filepath.Clean(h.Name)
		if n > 100 || filepath.IsAbs(h.Name) || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(h.Name, "\\") || h.Size < 0 {
			return nil, nil, fmt.Errorf("不安全的内核归档")
		}
		if h.Typeflag != tar.TypeDir && h.Typeflag != tar.TypeReg {
			return nil, nil, fmt.Errorf("内核归档包含链接或特殊文件")
		}
		total += h.Size
		if total > 180<<20 {
			return nil, nil, fmt.Errorf("内核解压超过大小限制")
		}
		base := filepath.Base(clean)
		if base != "frpc" && base != "frpc.exe" && base != "LICENSE" {
			continue
		}
		data, e := io.ReadAll(io.LimitReader(tr, 80<<20))
		if e != nil {
			return nil, nil, e
		}
		if base == "frpc" || base == "frpc.exe" {
			if bin != nil {
				return nil, nil, fmt.Errorf("归档内核重复")
			}
			bin = data
		} else {
			license = data
		}
	}
	if len(bin) == 0 || len(license) == 0 {
		return nil, nil, fmt.Errorf("归档缺少 frpc 或许可文件")
	}
	return bin, license, nil
}

func ExtractZip(archive []byte) (bin, license []byte, err error) {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, nil, err
	}
	if len(zr.File) > 100 {
		return nil, nil, fmt.Errorf("不安全的内核归档")
	}
	total := uint64(0)
	for _, f := range zr.File {
		clean := filepath.Clean(f.Name)
		if filepath.IsAbs(f.Name) || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(f.Name, "\\") {
			return nil, nil, fmt.Errorf("不安全的内核归档")
		}
		if f.FileInfo().IsDir() {
			continue
		}
		if f.Mode()&os.ModeSymlink != 0 || !f.Mode().IsRegular() {
			return nil, nil, fmt.Errorf("内核归档包含链接或特殊文件")
		}
		total += f.UncompressedSize64
		if total > 180<<20 {
			return nil, nil, fmt.Errorf("内核解压超过大小限制")
		}
		base := filepath.Base(clean)
		if base != "frpc" && base != "frpc.exe" && base != "LICENSE" {
			continue
		}
		rc, e := f.Open()
		if e != nil {
			return nil, nil, e
		}
		data, e := io.ReadAll(io.LimitReader(rc, 80<<20))
		_ = rc.Close()
		if e != nil {
			return nil, nil, e
		}
		if base == "frpc" || base == "frpc.exe" {
			if bin != nil {
				return nil, nil, fmt.Errorf("归档内核重复")
			}
			bin = data
		} else {
			license = data
		}
	}
	if len(bin) == 0 || len(license) == 0 {
		return nil, nil, fmt.Errorf("归档缺少 frpc 或许可文件")
	}
	return bin, license, nil
}
