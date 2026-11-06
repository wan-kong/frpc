# frp-desktop

采用 MyGo Native UI 的 macOS frp 客户端管理工具。主窗口与更新面板均由 Go 绘制，不运行 WebView、JavaScript 或前端开发服务器。仅管理本机 frpc，不管理 frps。

## 开发

需要 macOS 13+、Go 1.27.3，以及已有的 frps 地址和凭证。

```sh
go tool mygo dev
```

新建连接时选择保底内核，填写 frps 地址、端口和 token，再添加隧道。新连接默认启用 TLS 和系统 CA 校验；自签名环境可以导入 PEM CA。已有 frps 的配置需与客户端设置匹配。

已提供 TCP、UDP、HTTP、HTTPS、STCP、SUDP、XTCP 和访问者表单。每个连接对应独立 frpc 进程，多个连接可以同时运行。保存仅更新配置意图，“应用更改”才更新运行实例；隧道开关是直接应用操作。进程运行、代理注册和业务可达分别表示。

红色关闭按钮隐藏主窗口，连接继续运行；首次关闭会说明后台运行方式，通过 Dock 或菜单栏重开。侧栏和日志区支持拖动与键盘调整。退出应用会停止受管进程。登录启动和每连接自动连接默认关闭。

“视图”菜单可切换侧栏、检查器和日志；侧栏快捷键为 `⌃⌘S`。日志可组合等级、文本、本地时间范围和隧道/访问者标签筛选，复制与导出使用同一份筛选结果。对象关联仅匹配日志中的完整名称标签，连接级日志在“全部日志”中查看。

## CLI 检查

```sh
gofmt -l main.go internal
go vet ./...
go tool mygo vet .
CGO_ENABLED=0 go test ./... -count=1
go test -race ./internal/app/... ./internal/runtime/... ./internal/storage/... ./internal/logging/... ./internal/ui/...
```

包含单元与 Native UI Tester 检查、Go 检查及 race。真实 frpc/frps 集成用例需要自行放置官方 `frps` 到 `.test-tools/frps`，缺失时自动跳过。不运行 build 或 GUI 自动化。

产品资源仅含两种 macOS 架构的压缩 frpc 及其许可，第一次启动核验摘要后复制到用户内核库。压缩保存也避免打包时对内核重新签名导致固定摘要改变。

完整持续负载为可选 Go 用例，直接运行：

```sh
FRP_SOAK_DURATION=30m CGO_ENABLED=0 go test ./internal/acceptance -run '^TestPerformanceSoak$' -count=1 -timeout=45m -v
```

具体场景、指标和报告边界见[性能验收](docs/性能验收.md)，短时运行不计作完整验收通过。

## 配置与数据

配置与凭证保存在用户目录的 `~/.frpc/config.json`（目录 0700、文件 0600），不使用 macOS Keychain。自包含 TOML 可以导入；编辑器未覆盖的字段保留为加密原文。包含 includes、环境模板和任意外部文件路径的配置会明确拒绝，需先整理为自包含配置。原文件不被覆盖。

新日志写入 MyGo 的平台日志目录（macOS 为 `~/Library/Logs/<应用运行名称>/`），目录仅当前用户可访问。旧版数据目录中的日志保留原位。界面使用系统语义状态色和统一线性图标；检查器与日志的展开反馈遵循“减少动态效果”设置。

默认 TOML 导出省略凭证；包含凭证的导出是明文。完整备份使用口令加密，恢复新增连接并默认不启动。运行 frpc 时需要在受保护目录生成明文配置，停止后清理。

## 应用更新

直接使用 MyGo 内置的 `mygo.Updater` 读取 GitHub Release 签名更新，不再维护自建更新状态机、偏好或后台轮询。检查与安装从菜单或设置面板触发；安装完成可稍后生效，或走应用统一退出协调器重启并一次性恢复此前运行的连接。应用更新不会自动切换连接固定的 frpc 内核版本。

已确定仓库 `wan-kong/frpc` 和 Bundle ID `top.wankong.frpc`。`mygo.json` 已填写 `updates.github` 与 `mygo-update.pub` 的公钥（两者都是公开信息）；更新签名私钥只通过 `MYGO_UPDATER_PRIVATE_KEY` Secret 提供。发行构建不做 Apple 签名与公证，应用只有 ad-hoc 签名，因此从 GitHub 下载后首次打开会被 Gatekeeper 拦截，需由用户在 Finder 中右键打开；更新包签名保证更新来源，与 Apple 代码签名是两套机制。

推送 `v*` 标签会触发 [`.github/workflows/release.yml`](.github/workflows/release.yml)：校验标签与 `mygo.json.version`，构建 `darwin/arm64` 与 `darwin/amd64`，把应用包与更新资产上传到草稿 Release。草稿需人工检查后再发布。

完整产品与架构要求见 [技术与设计方案](docs/技术与设计方案.md)。实施状态及未完成项见 [实施进度](docs/实施进度.md)。CLI 检查不能替代真实 macOS 呈现、VoiceOver 及发行更新闭环验证，未执行的验收不会标为通过。
