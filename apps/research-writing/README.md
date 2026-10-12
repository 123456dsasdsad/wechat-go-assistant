# 研序 · 研究写作工作台

Vue 3 响应式工作台，配合 Python 标准库 HTTP 服务、SQLite 和已有校园助手任务队列。界面支持电脑与手机，正式业务数据由服务器保存。

## 功能

- 项目创建、切换、设置、研究阶段、归档及恢复、完整 JSON 导出。
- 文献阅读卡，导入微信资料库已有论文与整理内容。
- 研究问题、创新假设、实验、主张与证据、审稿意见、成员分工的新增及编辑。
- 保留原系统全部 14 个论文章节，服务器草稿、版本历史、恢复历史内容。
- 保存校验章节版本；遇到其他设备更新时保留本机编辑并提示比较。
- 请求编号防止网络重试重复创建草稿和 AI 评审任务。
- 本地规则检查、真实 `gpt-6.1-sol/high` 后台章节评审、原文版本绑定、问题处理状态。
- 投稿检查状态及备注。
- 搜索、键盘操作、手机底部导航、章节选择、弹窗焦点管理。

本地规则只做文本结构和线索检查。AI 评审也不等同于事实验证；文献、实验和指标需要依据原始资料核对。

## 文件结构

| 文件 | 用途 |
| --- | --- |
| `app.py` | 静态页面与研究项目 HTTP API |
| `repository.py` | 兼容迁移、SQLite 存储、草稿版本及评审 |
| `bridge.py` | 既有任务队列授权、提交、后台同步及结果格式校验 |
| `legacy.py` | 原系统兼容表结构、章节定义、规则检查与建议 |
| `frontend/src/App.vue` | 导航、项目操作、编辑状态和错误恢复 |
| `frontend/src/components/` | 总览、记录表单、编辑器、投稿检查 |
| `frontend/src/lib/` | 相对 API 地址、操作编号及字段定义 |
| `tests/test_api.py` | HTTP 集成测试：版本冲突、幂等保存、记录编辑、归档恢复、评审版本绑定 |

## 构建与启动

Node 22.12+ 用于构建；部署后的校园服务不需要 Node 常驻。

```sh
cd frontend
npm ci
npm run build
cd ..
mkdir -p static
cp -R frontend/dist/. static/
python3 app.py
```

默认绑定 `127.0.0.1:8765`。开发测试可以设置 `RESEARCH_PORT`、`RESEARCH_DB`；正式环境保留现有端口及 `data/research.db`。

```sh
python3 -m unittest discover -s tests -v
```

Vue 构建使用相对静态路径。API 地址相对当前应用入口生成，适配阿里云应用前缀。现有转发入口只允许内联样式，构建插件把本项目 CSS 内联进 HTML；沿用已有安全策略和 key 验证。

## 后台 AI 评审

`bridge.py` 读取现有维护配置 `/home/sanlou/campus-stack/maintenance/config.json` 中的私有 relay 地址和 key 文件路径。也可用 `RESEARCH_GATEWAY_CONFIG` 指定同格式的服务器配置。凭据不会传给浏览器或进入源码。

每个项目使用独立的研序评审会话，模型固定 `gpt-6.1-sol`、推理强度 `high`，不混入用户现有微信会话。任务会经现有队列执行，因此 token 用量进入助手既有统计。程序每 8 秒检查活跃评审，页面每 10 秒刷新活跃任务。

提交时把正文与研究记录写成私有 JSON 快照，关联草稿版本。模型只生成本次评审报告。程序检查请求与版本标识、评分及问题格式后保存到数据库；后续正文修改不会覆盖这份评审。关闭网页后服务继续同步，重启后继续读取已保存任务。

## 升级与回退

1. 用 SQLite backup API 备份数据库，同时保存代码与静态文件。
2. 上传辅助模块和静态资源，最后原子替换 `app.py`。
3. 使用既有 `campus-research-writing.service` 重启；运行 systemctl 时设置 `XDG_RUNTIME_DIR=/run/user/1000` 与 `DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus`。
4. 检查本机 `/api/projects`，再从原公网地址验收页面表单及手机布局。
5. 需要回退界面时恢复备份代码和静态文件；新增列及表与原程序兼容，数据库无需回退。只有确认需要恢复旧数据时才恢复数据库备份。

数据、访问 key、JSON 账号文件、私有配置、node_modules 与构建文件不提交到 Git。
