# 微信助手升级状态（2026-10-16）

## 已上线

- 发布版本：`2026.10.16-ha1`。
- 主执行节点已运行新版 Worker；发布包与运行文件 SHA256 一致。
- 云端 Relay 已运行新版二进制，Windows 计划任务为 Running，健康接口返回 `ok=true`。
- 节点策略已写入：校园 `priority=10, enabled=false`；ass `priority=20, enabled=true`；ICD `priority=30, enabled=false`；旧任务默认节点为 ass。
- 主执行节点心跳已登记，节点代次、模型列表和 library 能力可见；当前没有待领取任务。
- Worker 已启用会话级 Codex 状态目录、训练 detached 启动和系统进程锁。
- 会话身份、原生线程反向唯一、后台研序会话、记忆/素材/上传作用域、Outbox 原生 Turn 恢复和节点能力选择已纳入代码。
- 旧研序标题明确的活动会话保留原会话 ID；旧数据迁移没有按标题写入项目身份，未知项目不猜测，待校园项目库恢复后再做对账绑定。

## 备份与回滚

- ass 停写快照：`/opt/campus-assistant/backups/upgrade-20261015T193343Z`，123 个文件。
- 云端快照：`C:/CodexStack/wechat-go/backups/upgrade-20261015T193343Z`，342 个文件。
- 快照使用 SQLite 在线备份、SHA256 清单和 quick_check；另有新目录恢复工具，不会覆盖现有运行数据。
- 首次切换因计划任务启动延迟未通过健康检查，已自动恢复旧代码和配置；第二次通过认证健康轮询后完成切换。

## 验证结果

- `go test -mod=vendor ./cmd/... ./internal/... ./weixin`：通过。
- `go vet -mod=vendor ./cmd/... ./internal/... ./weixin`：通过。
- 研序 Python 测试：13 项通过。
- 快照/对账/恢复测试：4 项通过。
- Vue 前端构建：通过。
- 真实执行节点 Codex app-server 探针：通过，独立会话目录生成独立 SQLite 状态文件。

## 尚未完成的边界

- 校园服务器仍离线，不能验收校园→ass 的实际接管与校园恢复后的对账回切。
- ICD `<server-address>:22` 仍无法完成 SSH 握手，按用户要求保持禁用，不把它计入可用防线。
- 目前仍是 单控制面 SQLite；PostgreSQL、仲裁/fencing、独立对象存储、入口和模型网关多活尚未部署。
- 执行容器没有 systemd、Docker、rsync、ffmpeg、pdftotext，且用户命名空间受限；训练 detached 已可用，操作系统级沙箱和离线转写能力仍需补充运行环境。
- 旧会话中缺少项目 ID 的研序历史没有强行归类；当前管理 API 的本机授权入口返回 404，未通过旁路改库，校园恢复后需用项目数据库和原生会话快照生成差异报告，再补绑定。
