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

## CLI 检查

```sh
scripts/check.sh
```

包含 gofmt、go vet、MyGo 生命周期 vet、单元与 Native UI Tester 检查、真实 frpc/frps 的本机集成检查及 race 检查。不运行 build 或 GUI 自动化。

首次检查会将官方校验资产中的测试 frps 下载到忽略目录 `.test-tools/`；它只用于 loopback 集成检查，不进入产品资源或提供服务端管理能力。产品资源仅含两种 macOS 架构的压缩 frpc 及其许可，第一次启动核验摘要后复制到用户内核库。压缩保存也避免打包时对内核重新签名导致固定摘要改变。

## 配置与数据

数据保存在 MyGo 的用户数据目录，凭证通过 `mygo.Secrets` 存入 macOS Keychain。自包含 TOML 可以导入；编辑器未覆盖的字段保留为加密原文。包含 includes、环境模板和任意外部文件路径的配置会明确拒绝，需先整理为自包含配置。原文件不被覆盖。

默认 TOML 导出省略凭证；包含凭证的导出是明文。完整备份使用口令加密，恢复新增连接并默认不启动。运行 frpc 时需要在受保护目录生成明文配置，停止后清理。

## 应用更新

使用 MyGo 的 GitHub Release 签名更新引擎，本项目原生面板负责检查、下载、跳过版本和受控重启。重启前确认中断，随后一次性恢复此前运行的连接。应用更新不会自动切换连接固定的 frpc 内核版本。

当前配置是开发身份，尚未填写正式发布仓库、公钥和 Apple 签名/公证身份，因此开发模式如实显示不可自更新。发行配置检查：

```sh
python3 scripts/configure-release.py
```

该命令在缺少发行配置时明确失败。具备 `FRP_BUNDLE_ID`、`FRP_RELEASE_REPO`、`FRP_UPDATE_PUBLIC_KEY`、`FRP_SIGNING_IDENTITY`、`FRP_NOTARY_PROFILE` 环境变量后，可加 `--write` 写入公开元数据；它不打包或发布。更新私钥只通过 `MYGO_UPDATER_PRIVATE_KEY` 提供给未来的发行任务，不得提交。

完整产品与架构要求见 [技术与设计方案](docs/技术与设计方案.md)。实施状态及未完成项见 [实施进度](docs/实施进度.md)。CLI 检查不能替代真实 macOS 呈现、VoiceOver、签名公证及发行更新闭环验证，未执行的验收不会标为通过。
