# 配置与部署

此文描述可复现的组件关系，示例域名均为无效占位域名。现有生产环境应先备份私有配置，不能把模板直接覆盖到服务目录。

## 1. 目录与外部依赖

Relay 推荐使用 `bin/`、`config/`、`secrets/`、`data/` 分离目录。Worker 推荐使用专用 Linux 账号 `worker`，部署在 `/home/worker/campus-stack`。配置支持其他绝对路径，可选维护适配脚本使用固定布局，见第 6 节。

Go 二进制无需 Node/OpenClaw。Worker 另需官方 [Codex CLI](https://github.com/openai/codex)，其版本需支持本项目使用的 `app-server`、线程恢复、过程事件和 `turn/steer` 协议。`live_steering=true` 使用本地标准输入输出，不新增网络监听。`live_steering=false` 走 exec JSONL 兼容路径，不提供运行中追加。

本项目不安装、托管大模型。可使用自己的模型 API，也可接入自己已授权的 Cockpit 网关。客户端 Key 保存在独立文件，`CODEX_HOME/config.toml` 参考 [Codex 配置](../examples/codex/config.example.toml)。

## 2. 私有密钥和微信授权

生成随机 Relay Key（两个服务器保存相同内容），与网关客户端 Key 分开管理：

```sh
umask 077
mkdir -p secrets data config
openssl rand -hex 32 > secrets/relay.key
chmod 700 secrets data config
```

Windows 可使用密码学随机数生成同样长度的 Key，并限制目录 ACL。不要把密钥直接写进示例配置、终端历史或 Git。

在 Relay 端执行：

```sh
weixin login -state ./data/weixin.json -qr ./data/login-qr.png
```

扫码绑定的是微信 Bot 会话。正常服务与独立 listen 测试不能同时占用同一授权文件。残留 `.lock` 只能在确认持有进程已结束后处理。

## 3. 跨服务器转发

Relay 私有任务监听默认示例为 `127.0.0.1:17440`，公网网页监听为 `127.0.0.1:17444`；云端网关示例监听 `127.0.0.1:17442`。

从 Worker 向云端发起连接即可同时映射任务 API 与模型网关：

```sh
ssh -N -T -o ExitOnForwardFailure=yes -o ServerAliveInterval=30 \
  -o ServerAliveCountMax=3 \
  -L 127.0.0.1:17441:127.0.0.1:17440 \
  -L 127.0.0.1:18317:127.0.0.1:17442 \
  cloud-user@cloud.example.invalid
```

配置 SSH 密钥和核验远端主机指纹后，交给 systemd 管理该连接。Worker `relay_url` 指向 `http://127.0.0.1:17441`，Codex 提供商指向 `http://127.0.0.1:18317/v1`。若模型网关本来就在 Worker 本机或可访问的 HTTPS 地址，可修改 Codex 配置；这与任务转发相互独立。

## 4. 服务配置

- Relay：复制 [relay.windows.example.json](../examples/config/relay.windows.example.json) 或 [relay.linux.example.json](../examples/config/relay.linux.example.json)。`listen` 必须是 IP 回环地址。`public_url` 是含 `/wechat-files/` 的公开 HTTPS URL。
- Worker：复制 [worker.example.json](../examples/config/worker.example.json)，填写二进制、私有 Key 文件和任务根目录。`turn_timeout_seconds=43200` 为每轮 12 小时，允许范围 30–86400 秒。
- `max_concurrent_tasks` 控制一个 Worker 进程的执行位数，省略或设为 0 时默认 4，显式值允许 1–16；设为 1 可恢复单任务运行。Relay 按 owner 和会话原子认领任务，跳过已有有效租约的会话，因此同会话始终顺序执行，其他会话可并行。等待用户回答占一个执行位，达到上限后继续排队。
- 两端复制相同 [models.example.json](../examples/config/models.example.json)。当前默认初始化需要 `gpt-6-sol/high`；其他项须先通过自己的网关验证。
- `permissions=":danger-full-access"` 显式使用完整执行权限，操作系统账号仍是最终权限边界。若希望由 Codex 自己管理权限，可删除该字段并审查专用 Codex 配置。

启动命令：

```sh
relay --config /private/relay.json
worker --config /private/worker.json
```

Linux Worker 服务见 [systemd 模板](../examples/systemd/campus-wechat-worker.service)，使用 `systemctl --user` 安装到专用账号的用户服务目录。服务可自动重启；并发由单个 Worker 进程内的执行池提供，不能同时启动两个 Worker 写入同一会话映射。各任务独立维护租约、模型、工作目录、过程、提问和回传结果；同一进程共享同步保护的原生线程映射。升级前应取得空闲维护租约，保留正在执行的任务。

## 5. HTTPS 页面与模型 API

[Caddyfile 模板](../examples/caddy/Caddyfile.example) 只公开上传和结果路径。不要将 `/jobs/*`、`/maintenance/*` 或整个私有任务端口代理到公网。

公网模型 API 如需供其他电脑使用，可以单独代理网关的 `/v1/`，须保留网关 Key 认证并提供 TLS。每台客户端应有独立可撤销 Key，不能把 OAuth 账号导出当作远程客户端配置。示例默认没有启用公开模型 API。

网页输入最多保留 64 项/7 天，结果最多 1024 项/7 天；磁盘预留 512 MiB。流式传输不设固定上传字节上限。Worker 任务副本按会话保留，需要部署者制定清理策略。引用缓存保留 30 天，最多 10000 条、16 MiB，附件仍受原文件保留期约束。

## 6. 可选账号与运维适配器

不配置 Relay `cockpit_root` 时账号上传入口关闭。启用时需要 Windows 的 `powershell.exe` 重载适配器、对应版本 Cockpit 原生账号存储，以及绝对路径 `account_reload_runner`。导入按账号身份更新或添加；繁忙时等待维护租约，不修改运行中的任务。

`deploy/accounts/reload-pool.ps1` 与 `deploy/maintenance/` 保留为参考适配器，固定 Windows 布局 `C:\CodexStack`、Linux 布局 `/home/worker/campus-stack`，并包含明确的软件版本/服务名。安装前修改和核验所有路径、实际安装版本、任务名及资产规则；它们不是自动探测通用安装器。

维护计划使用北京时间：07:00 软件检查和账号检查，00:00 前一天用量，15 分钟重试。自定义 Go 程序不会从上游自动更新；维护不升级操作系统、不重启主机、不更改 Clash 配置。账号配额耗尽与网络错误不会被当作永久失效删除。

美元值按已知价格表计算 API 等价估值，未知模型不计价；不是订阅实际消费。价格表在 `internal/maintenance/usage.go`，需要维护者核验后更新。

## 7. 验收

SQLite 迁移与回退步骤见 [存储升级](storage-upgrade.md)。迁移后 JSON 文件只作旧备份，监控和升级工具必须通过私有 API 查询当前状态。

依次验证两端私有 health、微信简单任务、同一会话回忆、模型切换、文件 SHA-256、运行中过程文字和显式补发。日志与任务页属于私有运行数据，不要上传到公开 Issue。测试通过仅证明本地协议和调度逻辑，不能替代手机实际收取和账号网关验证。

## 等待用户回答

Worker 会为每次 Codex 运行临时配置纯 Go MCP stdio 工具 `wechat_questions/ask_user`，新的与恢复的会话均可用，无需修改全局 Codex 配置或新增监听端口。Relay 和 Worker 必须一起升级。提问工具超时为 43200 秒，整个任务仍受 `turn_timeout_seconds` 约束；Worker 的独立租约心跳在等待期间继续运行。工具初始化失败时该次运行失败，避免悄悄禁用提问能力。

Relay 使用私有 `/jobs/questions/publish`、`poll`、`resolve` 接口保存提问和回答。微信消息 ID 仅在接口确认发送后关联；回答通过 owner、任务、执行尝试和问题 ID 校验。公共任务页不公开租约、RPC ID 或用户答案。

配置依据：[OpenAI Codex MCP 文档](https://developers.openai.com/codex/mcp)。
