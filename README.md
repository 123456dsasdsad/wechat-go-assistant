# WeChat Go Assistant

用手机微信发送任务，在自己的 Linux 服务器运行 Codex，再把文字、图片和文件发回微信。微信接入、任务调度和网页入口采用 Go 实现，运行微信服务无需 OpenClaw、Node.js 或 npm。Codex CLI 和可选的 Cockpit 账号网关是独立的外部组件。

协议兼容实现参考腾讯公开项目，本仓库不是腾讯官方 Go SDK，也不包含模型权重、账号、密钥或已授权的微信登录状态。

## 功能

- 扫码绑定微信，保存接收游标和消息去重状态。
- 手机切换模型、推理强度和带编号的持久会话。
- 不同会话并行执行（默认最多 4 条），同会话普通消息顺序排队，`补充：……` 追加到当前会话运行任务。
- 引用原文恢复、图片和文件输入、网页粘贴聊天与大文件上传。
- 带签名的任务页面，显示执行过程、最终答复、图片及原件下载。
- 多文件结果自动打包，按任务保存回传收据，暂停旧附件混发。
- 可选的 Cockpit 账号 JSON 导入：已有账号更新、新账号添加。
- 确定性运维命令：软件更新检查、账号状态与 token 用量日报，不调用模型。
- SQLite 保存任务、会话、设置及引用索引，历史任务按需读取；原 JSON 保留为迁移前备份。
- 手机管理偏好，以及一次性和每日定时任务；命令解析不调用模型，计划到点执行时调用模型。

## 组成

| 组件 | 职责 | 常见运行位置 |
| --- | --- | --- |
| `weixin` / `cmd/weixin` | 微信协议库、扫码登录和独立连接测试 | 可访问微信的服务器 |
| `cmd/relay` | 消息接收、设置、会话、队列、上传页和结果页 | 云端 Windows 或 Linux |
| `cmd/worker` | 领取任务、调用 Codex、处理文件、上传结果和过程文字 | Linux 执行服务器 |
| `cmd/maintenance` | 用量、账号及更新检查 | 两端服务器 |
| `cmd/retry` | 服务停止后的离线任务修复工具 | 管理员使用 |
| `cmd/storageprobe` | 离线迁移检查、任务导出与合成内存测试 | 管理员使用 |

Relay 与 Worker 使用独立密钥认证，经 SSH 私有转发通信。Worker 在自己的网络中访问模型网关；校园服务器不能访问上游时，可通过云端的 SSH 转发访问 Cockpit。用户电脑不需要长期在线。

## 构建

要求 Go 1.24 或更高版本。依赖已 vendor，可离线构建 Go 程序：

```sh
go test -mod=vendor ./...
go vet -mod=vendor ./...
go build -mod=vendor -o bin/weixin ./cmd/weixin
go build -mod=vendor -o bin/relay ./cmd/relay
go build -mod=vendor -o bin/worker ./cmd/worker
go build -mod=vendor -o bin/maintenance ./cmd/maintenance
```

Windows PowerShell 或已安装 PowerShell 的系统可以运行 `./build.ps1`。脚本构建 Windows amd64、Linux amd64 和 Linux arm64，生成每个平台的 ZIP 与 SHA-256 清单；产物写入被 Git 忽略的 `dist/`。

## 开始使用

1. 先用 `weixin login -state ./data/weixin.json -qr ./data/login-qr.png` 扫码绑定；需要数字验证时在终端输入手机显示的验证码。
2. 在 Relay 和 Worker 两端建立受保护的配置、密钥目录，按 [配置与部署](docs/deployment.md) 配置 SSH 私有转发和外部 Codex CLI。
3. 从 [examples/config](examples/config) 复制配置模板，替换安装路径、域名和模型目录。模板只有示例值，不能直接用于已有生产环境。
4. 启动 `relay --config /private/relay.json` 与 `worker --config /private/worker.json`。Windows 的可选账号导入功能另需适配 Cockpit 的重载脚本。
5. 在微信发送“当前设置”“新建会话 测试”，再发送一条简单任务。收到回复后核对任务页和执行服务器上的真实结果。

模型权限取决于所使用的账号及网关。当前代码首次初始化默认选择 `gpt-6-sol/high`，因此两端模型目录必须包含这一项；模板中的其他型号仅作配置示例，不保证你的上游授权支持。可在接入验证后再启用对应型号。

## 微信常用指令

```text
模型列表
当前设置
默认模型 2
推理强度 low
新建会话 代码分析
会话列表
继续会话 1
当前会话
补充：结果图请使用中文标题
任务状态
上传文件
转发内容
上传账号
账号状态
用量日报
记住 绘图 中文标签
记忆列表
定时列表
```

查看会话列表后，10 分钟内直接回复编号即可切换。切换后发送新任务，可与原会话同时执行；同会话普通消息仍按顺序排队。只有显式“补充：”尝试加入当前选中会话正在执行的任务。并发上限由 Worker 的 `max_concurrent_tasks` 配置，默认 4，支持 1–16。完整说明见 [手机指令](docs/commands.md)。

## 运行边界

- 微信客户端决定转发联系人名单，现有 Bot 通道不能通过本项目设置成普通可转发好友。合并聊天卡片暂不解析，可用“转发内容”网页入口粘贴原文。
- 微信接口可能拒绝主动推送；服务保存结果和真实收据，但不能保证手机随时接受消息。任务页可查看和下载结果，显式“回传任务”可申请补发。
- Go 协议库的 CDN 直传处理上限为 25 MiB；大文件使用网页上传。网页无固定字节上限，仍受磁盘空间、保留期和每任务最多 4 个输入附件约束。
- ZIP 有路径、链接、条数和异常展开检查；密码压缩包、RAR、7z、tar 暂不自动解压。
- 过程页约每 3 秒刷新，只显示助手公开输出的文字，不展示内部推理；尚无文字输出时页面不会虚构进度。
- Worker 的执行权限由配置与操作系统账号共同决定。使用完整执行权限前请阅读 [安全与数据边界](SECURITY.md)。

## 目录

```text
cmd/                 可执行程序
weixin/              微信协议库
internal/            会话、队列、文件、Codex、账号和运维实现
examples/            无凭据配置、Caddy 和 systemd 模板
deploy/              可选的固定布局运维适配脚本
docs/                部署、指令和验证说明
licenses/            第三方许可
vendor/              固定版本的 Go 依赖
```

GitHub Actions 检查 Linux/Windows 的测试与 vet，并交叉编译各平台。测试用本地模拟服务，不要求微信账号或模型密钥；真实扫码、手机接收和特定网关的兼容性需要在自己的环境另行验收。

## 许可与来源

本项目采用 [MIT License](LICENSE)。腾讯协议参考与二维码库的来源及许可保留在 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。本仓库包含源码和无凭据示例，运行数据不随源码发布。
