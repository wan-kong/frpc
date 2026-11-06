package acceptance

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"frp-desktop/internal/app"
	"frp-desktop/internal/domain"
	"frp-desktop/internal/logging"
	run "frp-desktop/internal/runtime"
	"frp-desktop/internal/storage"
	view "frp-desktop/internal/ui"
	u "github.com/egoist/mygo/ui"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "--frpc-session" {
		os.Exit(run.Session(os.Args[2:]))
	}
	os.Exit(m.Run())
}

type latency struct {
	Count int     `json:"count"`
	P95MS float64 `json:"p95Ms"`
	MaxMS float64 `json:"maxMs"`
}

func summarize(samples []time.Duration) latency {
	if len(samples) == 0 {
		return latency{}
	}
	ordered := append([]time.Duration(nil), samples...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	return latency{len(ordered), float64(ordered[(len(ordered)*95+99)/100-1]) / float64(time.Millisecond), float64(ordered[len(ordered)-1]) / float64(time.Millisecond)}
}

type processSample struct {
	PID        int     `json:"pid"`
	RSSKiB     uint64  `json:"rssKiB"`
	CPUPercent float64 `json:"cpuPercent"`
}

type memorySample struct {
	Seconds   float64         `json:"seconds"`
	HeapBytes uint64          `json:"heapBytes"`
	Processes []processSample `json:"processes"`
}

type soakReport struct {
	InputMeasurement        string         `json:"inputMeasurement"`
	FrameMeasurement        string         `json:"frameMeasurement"`
	LayoutMeasurement       string         `json:"layoutMeasurement"`
	SourceSHA256            string         `json:"sourceSha256"`
	CoreSHA256              string         `json:"coreSha256"`
	RequestedSeconds        float64        `json:"requestedSeconds"`
	Platform                string         `json:"platform"`
	GoVersion               string         `json:"goVersion"`
	Status                  string         `json:"status"`
	Started                 time.Time      `json:"started"`
	DurationSeconds         float64        `json:"durationSeconds"`
	FullDuration            bool           `json:"fullDuration"`
	Profiles                int            `json:"profiles"`
	RunningConnections      int            `json:"runningConnections"`
	ConfiguredTunnels       int            `json:"configuredTunnels"`
	RegisteredTunnels       int            `json:"registeredTunnels"`
	LogLines                uint64         `json:"logLines"`
	LogLinesPerSecond       float64        `json:"logLinesPerSecond"`
	FirstHeadlessFrameMS    float64        `json:"firstHeadlessFrameMs"`
	Frames                  latency        `json:"frames"`
	LayoutBuilds            latency        `json:"layoutBuilds"`
	UIUpdates               latency        `json:"uiUpdates"`
	Inputs                  latency        `json:"inputs"`
	InputSequences          latency        `json:"inputSequences"`
	ShutdownMS              float64        `json:"shutdownMs"`
	Memory                  []memorySample `json:"memory"`
	HeapSlopeBytesPerMinute float64        `json:"heapSlopeBytesPerMinute"`
	Failures                []string       `json:"failures"`
}

// This opt-in CLI check combines real processes with deterministic synthetic
// log input. It measures Tester layout, not a Metal frame or OS input latency.
func TestPerformanceSoak(t *testing.T) {
	value := os.Getenv("FRP_SOAK_DURATION")
	if value == "" {
		t.Skip("长时验收仅在显式设置 FRP_SOAK_DURATION 时运行；不加入日常 check")
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration < 5*time.Second {
		t.Fatal("FRP_SOAK_DURATION 必须是至少 5s 的时长")
	}
	reportPath := os.Getenv("FRP_SOAK_REPORT")
	if reportPath == "" {
		t.Fatal("需要 FRP_SOAK_REPORT 保存验收证据")
	}
	report := soakReport{LayoutMeasurement: "first-view-pass-through-child-scene-recording", InputMeasurement: "single-tester-key-or-text-event", FrameMeasurement: "tester-advance-and-frame", RequestedSeconds: duration.Seconds(), Platform: runtime.GOOS + "/" + runtime.GOARCH, GoVersion: runtime.Version(), Status: "preparing", Profiles: 20, RunningConnections: 10, ConfiguredTunnels: 500}
	writeReport := func() {
		t.Helper()
		data, err := json.MarshalIndent(report, "", "  ")
		if err == nil {
			err = storage.AtomicWrite(reportPath, append(data, '\n'), 0600)
		}
		if err != nil {
			t.Error("写验收报告：", err)
		}
	}
	t.Cleanup(func() {
		if t.Failed() {
			report.Status = "failed"
		}
		writeReport()
	})
	report.SourceSHA256, err = sourceFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	store, err := storage.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	controller := app.New(store, &storage.MemorySecrets{}, executable)
	platformLogs := filepath.Join(t.TempDir(), "Logs", "frp-desktop")
	if err := controller.Runtime.SetLogDirectory(platformLogs); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := controller.Runtime.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), duration+2*time.Minute)
	defer cancel()
	if err = controller.Bootstrap(ctx, filepath.Join("..", "..", "resources")); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	serverConfig := filepath.Join(dir, "frps.toml")
	if err = storage.AtomicWrite(serverConfig, []byte(fmt.Sprintf("bindAddr='127.0.0.1'\nbindPort=%d\nproxyBindAddr='127.0.0.1'\n", port)), 0600); err != nil {
		t.Fatal(err)
	}
	serverPath, err := filepath.Abs(filepath.Join("..", "..", ".test-tools", "frps"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(serverPath); err != nil {
		t.Skip("缺少 .test-tools/frps；真实内核负载检查已跳过")
	}
	server := exec.CommandContext(ctx, serverPath, "-c", serverConfig)
	server.Stdout, server.Stderr = io.Discard, io.Discard
	if err = server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Process.Kill(); _ = server.Wait() })
	await(t, 10*time.Second, func() bool {
		conn, e := net.DialTimeout("tcp", domain.Endpoint("127.0.0.1", port), 100*time.Millisecond)
		if e == nil {
			_ = conn.Close()
		}
		return e == nil
	})
	local, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	go func() {
		for {
			conn, err := local.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(time.Second))
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	co := store.Snapshot().Cores[0]
	report.CoreSHA256 = co.SHA256
	var ids []string
	for i := 0; i < 20; i++ {
		p := domain.NewProfile()
		p.Name = fmt.Sprintf("性能连接-%02d", i)
		p.ServerAddr, p.ServerPort, p.CoreID = "127.0.0.1", port, co.ID
		p.TLS, p.VerifyTLS = false, false
		for j := 0; j < 25; j++ {
			x := domain.NewTunnel()
			x.Name, x.LocalPort = fmt.Sprintf("service-%02d-%02d", i, j), local.Addr().(*net.TCPAddr).Port
			p.Tunnels = append(p.Tunnels, x)
		}
		if err := controller.Save(ctx, p, nil, domain.NewID()); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, p.ID)
	}
	commands := make([]app.Command, 10)
	for i := range commands {
		commands[i] = controller.CurrentCommand(ids[i], app.StartProfile{})
	}
	if _, err := controller.ExecuteMany(ctx, commands); err != nil {
		t.Fatal(err)
	}
	await(t, 30*time.Second, func() bool { return registered(controller) == 250 })
	var pids = []int{os.Getpid()}
	for _, state := range controller.Runtime.Snapshot() {
		if state.Process == "running" {
			pids = append(pids, state.PID)
		}
	}
	if len(pids) != 11 {
		t.Fatal("运行连接数不等于 10")
	}
	checkForwarding(t, controller)

	// Input goes through the production bounded reader/redactor/rotating writer.
	// It substitutes only the selected profile's log snapshot in the Tester.
	logs := logging.New(filepath.Join(dir, "load-logs", ids[0]), nil)
	defer logs.Close()
	reader, writer := io.Pipe()
	readDone := make(chan struct{})
	go func() { defer close(readDone); logs.Read(reader) }()
	producerCtx, stopProducer := context.WithCancel(ctx)
	var produced atomic.Uint64
	producerDone := make(chan struct{})
	go func() {
		defer close(producerDone)
		defer writer.Close()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-producerCtx.Done():
				return
			case <-ticker.C:
				for i := 0; i < 10; i++ {
					n := produced.Load() + 1
					if _, err := fmt.Fprintf(writer, "[info] service-00-00 performance log %08d token=check-secret 中文日志\n", n); err != nil {
						return
					}
					produced.Add(1)
				}
			}
		}
	}()
	defer func() { stopProducer(); _ = reader.Close(); <-producerDone; <-readDone }()
	a := view.New(controller)
	a.Selected, a.ShowLogs = ids[0], true
	first := time.Now()
	var observed frameObserver
	tester := u.NewTester(observed.wrap(a.View), 1120, 760)
	report.FirstHeadlessFrameMS = float64(time.Since(first)) / float64(time.Millisecond)
	profile, err := os.Create(reportPath + ".cpu.pprof")
	if err != nil {
		t.Fatal(err)
	}
	if err := pprof.StartCPUProfile(profile); err != nil {
		_ = profile.Close()
		t.Fatal(err)
	}
	defer func() { pprof.StopCPUProfile(); _ = profile.Close() }()
	var frames, updates, inputs []time.Duration
	var inputSequences []time.Duration
	var checks uint64
	lastFrameTime := time.Now()
	report.Started, report.Status = time.Now(), "running"
	report.RegisteredTunnels = registered(controller)
	initialLines := produced.Load()
	writeReport()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(duration)
	defer deadline.Stop()
	progress := time.NewTicker(time.Minute)
	defer progress.Stop()
	sampleMemory := func() {
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		report.Memory = append(report.Memory, memorySample{time.Since(report.Started).Seconds(), m.HeapAlloc, processMemory(t, pids)})
	}
	sampleMemory()
load:
	for {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-deadline.C:
			break load
		case <-progress.C:
			sampleMemory()
			report.DurationSeconds = time.Since(report.Started).Seconds()
			report.LogLines, report.Frames, report.UIUpdates, report.Inputs = produced.Load()-initialLines, summarize(frames), summarize(updates), summarize(inputs)
			report.InputSequences = summarize(inputSequences)
			report.LayoutBuilds = summarize(observed.samples)
			report.LogLinesPerSecond = float64(report.LogLines) / report.DurationSeconds
			report.RegisteredTunnels = registered(controller)
			writeReport()
			t.Logf("负载 %.0fs：%d 行，帧 p95 %.2fms，更新 p95 %.2fms", report.DurationSeconds, report.LogLines, report.Frames.P95MS, report.UIUpdates.P95MS)
		case <-ticker.C:
			begin := time.Now()
			if checks%100 == 0 {
				a.SelectScope("all")
			} else if checks%100 == 20 {
				a.SelectScope(ids[0])
			}
			a.Refresh()
			lines, err := logs.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			if len(lines) > 10000 {
				t.Fatal("日志超出行数上限")
			}
			size := 0
			for _, line := range lines {
				size += len(line.Text)
				if strings.Contains(line.Text, "check-secret") {
					t.Fatal("日志脱敏失效")
				}
			}
			if size > 8<<20 {
				t.Fatal("日志超出字节上限")
			}
			a.Logs = lines
			updates = append(updates, time.Since(begin))
			if checks%10 == 0 {
				if registered(controller) != 250 {
					t.Fatal("持续负载中连接或代理注册丢失")
				}
				input := time.Now()
				measureEvent := func(event func()) {
					begin := time.Now()
					event()
					inputs = append(inputs, time.Since(begin))
				}
				wasEmpty := a.Search == ""
				measureEvent(func() { tester.Key(u.Cmd, u.KeyF) })
				measureEvent(func() { tester.Key(u.Cmd, u.KeyA) })
				measureEvent(func() { tester.Key(0, u.KeyBackspace) })
				if wasEmpty {
					measureEvent(func() { tester.Type("service-00") })
				}
				if (a.Search == "") == wasEmpty {
					t.Fatal("搜索输入未到达控件")
				}
				inputSequences = append(inputSequences, time.Since(input))
			}
			frame := time.Now()
			// Advance the Tester's clock with real elapsed time, so its native
			// transitions and timers run rather than staying at their first frame.
			tester.Advance(frame.Sub(lastFrameTime))
			lastFrameTime = frame
			tester.Frame()
			frames = append(frames, time.Since(frame))
			if checks%100 == 0 {
				checkForwarding(t, controller)
			}
			checks++
		}
	}
	stopProducer()
	<-producerDone
	<-readDone
	report.DurationSeconds = time.Since(report.Started).Seconds()
	report.FullDuration = duration >= 30*time.Minute && report.DurationSeconds >= 1800
	report.LogLines = produced.Load() - initialLines
	report.LogLinesPerSecond = float64(report.LogLines) / report.DurationSeconds
	report.Frames, report.UIUpdates, report.Inputs = summarize(frames), summarize(updates), summarize(inputs)
	report.InputSequences = summarize(inputSequences)
	report.LayoutBuilds = summarize(observed.samples)
	report.RegisteredTunnels = registered(controller)
	sampleMemory()
	// Keep the live view and sampling arrays reachable during the final GC.
	// Compiler liveness must not make the final retained heap look smaller.
	runtime.KeepAlive(tester)
	runtime.KeepAlive(a)
	runtime.KeepAlive(frames)
	runtime.KeepAlive(updates)
	runtime.KeepAlive(inputs)
	runtime.KeepAlive(inputSequences)
	runtime.KeepAlive(observed)
	begin := time.Now()
	if err := controller.CloseForQuit(false, nil); err != nil {
		t.Fatal(err)
	}
	report.ShutdownMS = float64(time.Since(begin)) / float64(time.Millisecond)
	check := func(ok bool, reason string) {
		if !ok {
			report.Failures = append(report.Failures, reason)
			t.Error(reason)
		}
	}
	// At the boundary a single 100ms batch may still be in flight. Account
	// for that quantization without hiding a sustained scheduling shortfall.
	check(float64(report.LogLines+10)/report.DurationSeconds >= 99, "日志负载未达到每秒 100 行（允许 1% 误差及边界的一批 10 行）")
	check(report.Frames.P95MS <= 16, "headless 帧 p95 超过 16ms")
	check(report.LayoutBuilds.Count > 0 && report.LayoutBuilds.P95MS <= 16, "单次布局/场景构建 p95 超过 16ms，或缺少构建采样")
	check(report.UIUpdates.P95MS <= 50 && report.Inputs.P95MS <= 50, "UI 更新或搜索反馈 p95 超过 50ms")
	check(report.ShutdownMS <= 5000, "受管进程退出超过 5s")
	report.Status = "smoke-passed"
	if report.FullDuration {
		// Compare post-warmup retained heaps; GC and ps are outside latency samples.
		var baseline uint64
		for _, m := range report.Memory {
			if m.Seconds >= 300 {
				baseline = m.HeapBytes
				break
			}
		}
		check(baseline != 0 && report.Memory[len(report.Memory)-1].HeapBytes <= baseline+16<<20, "暖机后保留堆增长超过 16MiB，需要分析内存趋势")
		report.HeapSlopeBytesPerMinute = heapSlope(report.Memory)
		check(report.HeapSlopeBytesPerMinute <= 256<<10, "暖机后保留堆持续增长超过每分钟 256KiB，需要分析内存趋势")
		report.Status = "full-cli-passed"
	}
	if t.Failed() {
		report.Status = "failed"
	}
	t.Logf("验收报告：%s（%s）", reportPath, report.Status)
}

func sourceFingerprint() (string, error) {
	root := filepath.Join("..", "..")
	files := []string{"main.go", "go.mod", "go.sum", "mygo.json"}
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".go") {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	hash := sha256.New()
	for _, name := range files {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			return "", err
		}
		_, _ = fmt.Fprintf(hash, "%s\x00%d\x00", filepath.ToSlash(name), len(data))
		_, _ = hash.Write(data)
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func checkForwarding(t *testing.T, c *app.Controller) {
	t.Helper()
	for _, state := range c.Runtime.Snapshot() {
		if state.Process != "running" {
			continue
		}
		if len(state.Proxies) == 0 {
			t.Fatal("运行连接缺少代理状态")
		}
		conn, err := net.DialTimeout("tcp", state.Proxies[0].RemoteAddr, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		_, err = io.WriteString(conn, "soak-echo")
		var data [9]byte
		if err == nil {
			_, err = io.ReadFull(conn, data[:])
		}
		_ = conn.Close()
		if err != nil || string(data[:]) != "soak-echo" {
			t.Fatalf("真实转发失败：%q %v", data, err)
		}
	}
}

func heapSlope(samples []memorySample) float64 {
	var n, x, y, xy, xx float64
	for _, m := range samples {
		if m.Seconds < 300 {
			continue
		}
		xi, yi := m.Seconds/60, float64(m.HeapBytes)
		n++
		x += xi
		y += yi
		xy += xi * yi
		xx += xi * xi
	}
	if n < 2 || n*xx == x*x {
		return 0
	}
	return (n*xy - x*y) / (n*xx - x*x)
}

func registered(c *app.Controller) int {
	count := 0
	for _, state := range c.Runtime.Snapshot() {
		if state.Process != "running" {
			continue
		}
		for _, proxy := range state.Proxies {
			if proxy.Status == "running" {
				count++
			}
		}
	}
	return count
}

func await(t *testing.T, timeout time.Duration, ready func() bool) {
	t.Helper()
	end := time.Now().Add(timeout)
	for time.Now().Before(end) {
		if ready() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("等待真实进程就绪超时")
}

func processMemory(t *testing.T, pids []int) []processSample {
	t.Helper()
	var ids []string
	for _, pid := range pids {
		ids = append(ids, strconv.Itoa(pid))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, "ps", "-o", "pid=,rss=,%cpu=", "-p", strings.Join(ids, ",")).Output()
	if err != nil {
		t.Fatal(err)
	}
	var out []processSample
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var m processSample
		if _, err := fmt.Sscan(line, &m.PID, &m.RSSKiB, &m.CPUPercent); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	if len(out) != len(pids) {
		t.Fatal("资源采样丢失受管进程")
	}
	return out
}
