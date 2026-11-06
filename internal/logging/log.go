package logging

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type Line struct {
	Seq   uint64
	Time  time.Time
	Level string
	Text  string
}
type Buffer struct {
	Notify   func()
	OnLine   func(Line, string)
	mu       sync.Mutex
	lines    []Line
	bytes    int
	seq      uint64
	secrets  []string
	redactor *strings.Replacer
	dir      string
	file     *os.File
	size     int64
	diskErr  error
}

var authorization = regexp.MustCompile(`(?i)(authorization["\s:=]+)(?:Bearer|Basic)\s+[^\s,"\r\n]+`)
var sensitive = regexp.MustCompile(`(?i)(token|password|secretKey|clientSecret|authorization)(["']?\s*[:=]\s*["']?)([^\s,"'}]+)`)
var userinfo = regexp.MustCompile(`(https?|socks5h?)://[^\s/@]+:[^\s/@]+@`)
var ansi = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func buildRedactor(values []string) *strings.Replacer {
	seen := map[string]bool{}
	var secrets []string
	for _, v := range values {
		if v != "" && !seen[v] {
			seen[v] = true
			secrets = append(secrets, v)
		}
	}
	sort.SliceStable(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	var pairs []string
	for _, v := range secrets {
		pairs = append(pairs, v, "[已隐藏]")
	}
	return strings.NewReplacer(pairs...)
}
func redactPrepared(s string, replacer *strings.Replacer) string {
	s = ansi.ReplaceAllString(s, "")
	s = authorization.ReplaceAllString(s, "${1}[已隐藏]")
	s = sensitive.ReplaceAllString(s, "${1}${2}[已隐藏]")
	s = userinfo.ReplaceAllString(s, "${1}://[已隐藏]@")
	if replacer != nil {
		s = replacer.Replace(s)
	}
	return s
}
func Redact(s string, secrets []string) string { return redactPrepared(s, buildRedactor(secrets)) }
func New(dir string, secrets []string) *Buffer {
	return &Buffer{dir: dir, secrets: append([]string(nil), secrets...), redactor: buildRedactor(secrets)}
}

func (b *Buffer) Add(s string) {
	raw := s
	var added Line
	b.mu.Lock()
	defer func() {
		b.mu.Unlock()
		if b.OnLine != nil {
			b.OnLine(added, raw)
		}
		if b.Notify != nil {
			b.Notify()
		}
	}()
	s = redactPrepared(s, b.redactor)
	if len(s) > 64<<10 {
		s = s[:64<<10] + " [内容已截断]"
	}
	level := "info"
	lower := strings.ToLower(raw)
	for _, x := range []string{"error", "warn", "debug", "trace"} {
		short := map[string]string{"error": "e", "warn": "w", "debug": "d", "trace": "t"}[x]
		if strings.Contains(lower, "["+x+"]") || strings.Contains(lower, "["+short+"]") {
			level = x
			break
		}
	}
	b.seq++
	line := Line{Seq: b.seq, Time: time.Now(), Level: level, Text: s}
	added = line
	b.lines = append(b.lines, line)
	b.bytes += len(s)
	for len(b.lines) > 10000 || b.bytes > 8<<20 {
		b.bytes -= len(b.lines[0].Text)
		b.lines = b.lines[1:]
	}
	if b.dir != "" {
		b.write(line)
	}
}
func (b *Buffer) write(l Line) {
	if b.diskErr != nil {
		return
	}
	if b.file == nil {
		if err := os.MkdirAll(b.dir, 0700); err != nil {
			b.diskErr = err
			return
		}
		f, err := os.OpenFile(filepath.Join(b.dir, "current.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			b.diskErr = err
			return
		}
		b.file = f
		if st, err := f.Stat(); err == nil {
			b.size = st.Size()
		}
	}
	s := fmt.Sprintf("%s [%s] %s\n", l.Time.Format(time.RFC3339), l.Level, l.Text)
	n, err := io.WriteString(b.file, s)
	b.size += int64(n)
	if err != nil {
		b.diskErr = err
		return
	}
	if b.size >= 5<<20 {
		_ = b.file.Close()
		b.file = nil
		_ = os.Remove(filepath.Join(b.dir, "4.log"))
		for i := 3; i >= 1; i-- {
			_ = os.Rename(filepath.Join(b.dir, fmt.Sprintf("%d.log", i)), filepath.Join(b.dir, fmt.Sprintf("%d.log", i+1)))
		}
		b.diskErr = os.Rename(filepath.Join(b.dir, "current.log"), filepath.Join(b.dir, "1.log"))
		b.size = 0
		if b.diskErr == nil {
			b.diskErr = Prune(filepath.Dir(b.dir), 250<<20)
		}
	}
}
func (b *Buffer) Snapshot() ([]Line, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]Line(nil), b.lines...), b.diskErr
}
func (b *Buffer) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.file != nil {
		_ = b.file.Close()
		b.file = nil
	}
}
func (b *Buffer) SetSecrets(values []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.secrets = append([]string(nil), values...)
	b.redactor = buildRedactor(values)
}
func (b *Buffer) Read(r io.Reader) {
	reader := bufio.NewReaderSize(r, 64<<10)
	var line []byte
	truncated := false
	for {
		part, prefix, err := reader.ReadLine()
		if len(line)+len(part) <= 64<<10 {
			line = append(line, part...)
		} else {
			truncated = true
		}
		if !prefix && len(line) > 0 {
			if truncated {
				line = append(line, []byte(" [内容已截断]")...)
			}
			b.Add(string(line))
			line = nil
			truncated = false
		}
		if err != nil {
			if err != io.EOF {
				b.Add("[error] 日志读取失败：" + err.Error())
			}
			return
		}
	}
}
