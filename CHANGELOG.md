# Changelog

## 0.1.0

首个公开版本。

- MyGo Native UI 的 macOS 与 Windows frpc 客户端，界面与更新面板均由 Go 绘制。
- 支持 TCP、UDP、HTTP、HTTPS、STCP、SUDP、XTCP 隧道与访问者。
- 每个连接对应独立 frpc 进程，可同时管理多个 frps 连接。
- 支持导入 `frpc.toml`，可视化编辑器未覆盖的字段保留为加密原文运行。
- 隧道可单独启停，日志可按等级、文本、本地时间范围与标签筛选。
- 关闭主窗口后连接继续在后台运行，可通过 Dock、任务栏或托盘图标重开。
- 通过 GitHub Release 的签名更新安装新版本。