package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
)

// The WeChat menu and workbench use the same directory. Looking up a command
// never executes it, changes the current session, or creates an AI job.
type helpEntry struct {
	Syntax   string `json:"syntax"`
	Purpose  string `json:"purpose"`
	Example  string `json:"example"`
	Keywords string `json:"keywords,omitempty"`
	UsesAI   bool   `json:"uses_ai"`
}
type helpGroup struct {
	Number  int         `json:"number"`
	Name    string      `json:"name"`
	Aliases string      `json:"aliases"`
	Note    string      `json:"note"`
	Entries []helpEntry `json:"entries"`
}
type helpMatch struct {
	Group string `json:"group"`
	helpEntry
}
type helpDirectory struct {
	Groups  []helpGroup `json:"groups"`
	Matches []helpMatch `json:"matches"`
	Query   string      `json:"query"`
}

var commandGroups = []helpGroup{
	{1, "会话", "会话 对话 上下文", "查看会话列表后，10分钟内单独回复编号可以切换。不同会话可并行；同一会话普通消息排队。", []helpEntry{
		{"新建会话 <名称>", "创建独立上下文并选中它。", "新建会话 论文分析", "新会话", false},
		{"会话列表", "查看编号、名称和任务摘要。", "会话列表", "列表 编号", false},
		{"继续会话 <编号或ID>", "选择旧会话，后续任务接着原上下文处理。", "继续会话 1", "切换 选择 上下文", false},
		{"当前会话", "查看当前选中的会话。", "当前会话", "", false},
		{"重命名会话 <编号> <名称>", "修改指定会话的名称；当前会话也可用“会话命名”。", "重命名会话 1 实验分析", "会话命名 改名", false},
		{"搜索会话 <关键词>", "按名称或任务摘要查找会话。", "搜索会话 训练", "查找", false},
		{"置顶会话 <编号>", "置顶常用会话；取消用“取消置顶会话”。", "置顶会话 2", "取消置顶会话", false},
		{"归档会话 <编号>", "保留上下文并归档；恢复用“恢复会话”。先切换再归档当前会话。", "归档会话 2", "恢复会话", false},
	}},
	{2, "模型与设置", "模型 设置 推理", "设置作用于当前会话的新任务，已排队任务保持原设置。实际可用模型以“模型列表”为准。", []helpEntry{
		{"模型列表", "查看可选择的模型及编号。", "模型列表", "模型帮助", false},
		{"当前设置", "查看模型和推理强度。", "当前设置", "当前模型", false},
		{"默认模型 <编号或模型ID>", "切换当前会话后续任务使用的模型。", "默认模型 2", "切换模型", false},
		{"推理强度 <等级>", "选择当前模型支持的推理等级。", "推理强度 low", "low high 思考", false},
		{"使用 <模型ID>：<任务要求>", "只为这一条任务临时选择模型。", "使用 gpt-6-sol：总结附件", "临时模型", true},
	}},
	{3, "任务与提问", "任务 问题 进度 回答", "任务列表编号10分钟内有效，也可使用任务ID前8位。回答问题优先引用那条问题，避免串到其他任务。", []helpEntry{
		{"任务状态", "查看进度和各任务的结果链接；Codex 进度会话中只查看绑定的原任务。", "任务状态", "查看任务 查看进度 进度", false},
		{"任务列表", "查看任务编号和状态，供停止、重试等指令使用。", "任务列表", "任务中心", false},
		{"停止任务 <编号或ID>", "请求终止指定任务，等待执行端确认停止。", "停止任务 1", "取消 终止", false},
		{"重试任务 <编号或ID>", "为失败或已停止的任务创建关联原任务的新任务。", "重试任务 1", "失败 重跑", true},
		{"补发结果 <编号或ID>", "只补发该任务尚未接收的附件，不重新运行AI。", "补发结果 1", "回传任务 图片 发送 文件", false},
		{"补充：<新增要求>", "尝试加入当前会话正在运行的任务；普通消息仍排队。", "补充：图片使用中文标题", "追加 运行中", true},
		{"待回答", "查看尚未回答的问题和选项。", "待回答", "问题列表", false},
		{"回答 <问题编号> <答案>", "不能引用时，用问题编号提交选项或文字。也可直接引用问题后回复。", "回答 <问题编号> 2", "引用 选项 选择", false},
	}},
	{4, "文件与转发", "文件 上传 转发 附件 压缩包", "网页上传无固定大小上限，但受磁盘可用空间限制。普通文件保留7天；每个任务最多4个文件。", []helpEntry{
		{"上传文件", "获取手机上传链接，适合微信转发不了的文件和大压缩包。", "上传文件", "文件上传 ZIP 压缩包", false},
		{"文件列表", "查看最近文件及文件ID。", "文件列表", "查看文件", false},
		{"文件帮助", "查看支持的附件格式；自动解压使用ZIP。", "文件帮助", "RAR 7z 压缩包", false},
		{"分析最新文件：<要求>", "让AI处理最后上传的文件。", "分析最新文件：总结方法与实验", "附件 读取", true},
		{"分析文件 <文件ID>：<要求>", "指定文件创建分析任务。", "分析文件 <文件ID>：检查代码", "", true},
		{"转发内容", "获取粘贴聊天的链接，填写要求后在网页显式提交分析。", "转发内容", "转发消息 聊天转发 粘贴聊天 别人 消息 引用", false},
	}},
	{5, "资料库", "资料库 资料 收录 截图", "连续截图先“开始收录”，发完后“完成收录”。查询、草稿操作不调用AI；提交研究后查文献并自动分类。", []helpEntry{
		{"资料库", "查看各类别的资料数量和综述版本。", "资料库", "资料库列表", false},
		{"开始收录 <资料库名称>", "开始连续收集截图或PDF，暂不研究。", "开始收录 图学习", "截图 批量", false},
		{"收录备注 <说明>", "为当前收录草稿添加研究要求或背景。", "收录备注 重点整理可在CPU复现的方法", "", false},
		{"完成收录", "提交当前草稿，查来源、整理方法并更新全部关联类别的综述。", "完成收录", "提交 分类", true},
		{"取消收录", "退出收录模式，保留草稿。", "取消收录", "取消 草稿", false},
		{"继续收录 <草稿ID>", "恢复未提交的草稿并继续添加资料。", "继续收录 <草稿ID>", "恢复 草稿", false},
		{"收录到 <资料库名称> <链接或说明>", "直接提交一条公开文章、论文链接或随附截图。", "收录到 图学习 https://arxiv.org/abs/1710.10903", "链接 公众号", true},
		{"研究这组截图 <要求>", "随附截图直接创建资料研究任务。", "研究这组截图 查找原论文并整理方法", "图片", true},
		{"查资料 <关键词>", "查询已入库资料。", "查资料 注意力", "搜索 查询", false},
		{"资料 <编号>", "查看方法卡、来源和阅读范围；“文献”“方法”也可用。", "资料 1", "文献 方法", false},
		{"本会话资料库 <类别A,类别B>", "为本会话的后续任务绑定资料类别。", "本会话资料库 图学习,注意力机制", "引用 绑定 上下文", false},
	}},
	{6, "文献与综述", "文献 综述 论文 类别", "同一论文属于多个类别时，每个类别都更新。管理页还能导出Markdown/JSON/BibTeX、保留笔记、锁定章节和恢复历史版本。", []helpEntry{
		{"研究主题 <主题>", "检索真实文献，整理方法卡并更新关联综述。", "研究主题 图注意力在时序预测中的应用", "查文献 论文 搜索", true},
		{"补查文献 <主题>", "按指定主题再检索和研究文献。", "补查文献 图学习 CPU 复现", "论文 搜索", true},
		{"综述列表", "查看类别、篇数和综述版本。", "综述列表", "", false},
		{"综述 <类别>", "查看当前综述，长内容在管理页阅读。", "综述 图学习", "阅读", false},
		{"综述变化 <类别>", "查看该类别最新一版的修改说明。", "综述变化 图学习", "版本 增量", false},
		{"导出综述 <类别>", "获取综述管理入口，在网页选择导出格式。", "导出综述 图学习", "下载 Markdown JSON BibTeX", false},
		{"更新综述 [类别]", "基于已有证据更新指定类别；省略类别更新所有待更新综述。", "更新综述 图学习", "重新整理", true},
		{"移动资料 <编号> 到 <类别A,类别B>", "修改关联类别，并更新原类别和新类别的综述。", "移动资料 1 到 图学习,注意力机制", "分类", true},
		{"资料标签 <编号> <标签A,标签B>", "保存标签，并安排受影响综述更新。", "资料标签 1 CPU,图神经网络", "分类", true},
		{"删除资料 <编号>", "移入回收状态，并更新关联综述。", "删除资料 1", "回收", true},
		{"恢复资料 <编号>", "恢复已删除资料，并更新关联综述。", "恢复资料 1", "回收", true},
	}},
	{7, "模板", "模板 常用任务", "查看、保存和删除模板不调用AI；运行模板才创建任务。", []helpEntry{
		{"模板列表", "查看可复用的任务模板。", "模板列表", "", false},
		{"保存模板 <名称>：<指令>", "保存模板，用{{内容}}插入每次不同的要求。", "保存模板 代码审查：检查{{内容}}的输入校验", "新增", false},
		{"运行模板 <名称>：<要求>", "展开模板，在当前会话执行。", "运行模板 代码审查：上传接口", "执行", true},
		{"删除模板 <名称>", "删除已保存模板。", "删除模板 代码审查", "", false},
	}},
	{8, "偏好", "偏好 记忆 习惯", "偏好供后续任务参考，当前明确要求优先。", []helpEntry{
		{"记住 <名称> <内容>", "保存常用格式或处理偏好。", "记住 绘图 中文坐标标签", "记忆 保存", false},
		{"记忆列表", "查看保存的偏好。", "记忆列表", "偏好列表", false},
		{"忘记 <名称>", "删除一条偏好。", "忘记 绘图", "记忆 删除", false},
	}},
	{9, "定时计划", "定时 计划 每日 提醒", "使用北京时间；创建和查询计划不调用AI，到期执行任务才调用创建时选定的模型。", []helpEntry{
		{"定时任务 <日期> <时间> <要求>", "创建一次性任务，到期进入队列。", "定时任务 2026-10-10 09:00 检查项目运行状态", "一次性", true},
		{"每日任务 <时间> <要求>", "创建每日任务，到期进入队列。", "每日任务 09:00 检查项目运行状态", "每天", true},
		{"定时列表", "查看计划编号和执行时间。", "定时列表", "查询", false},
		{"取消定时 <计划编号>", "取消后续执行，已经入队的任务仍继续。", "取消定时 <计划编号>", "删除 停止", false},
		{"定时帮助", "查看时间格式和计划执行规则。", "定时帮助", "", false},
	}},
	{10, "账号", "账号 帐号 Cockpit", "账号导入与状态查询由程序直接处理。状态为最近一次检查报告，校园检查包含各账号明细。", []helpEntry{
		{"上传账号", "获取Cockpit账号JSON上传链接：已有账号更新，新账号添加。", "上传账号", "导入 JSON 添加", false},
		{"上传账号状态", "查看账号导入结果。", "上传账号状态", "导入", false},
		{"账号状态", "查看最近的账号检查报告。", "账号状态", "失效账号 有效 配额", false},
		{"校园账号检查", "查看校园端最近的逐账号检查明细。", "校园账号检查", "校园账号状态 校园帐号检查 校园帐号状态", false},
	}},
	{11, "软件运维", "软件 运维 更新 升级", "检查和更新不调用AI；请求进入原生更新队列，任务忙时延后，完成后发送报告。", []helpEntry{
		{"软件更新", "安排两端软件更新检查，有更新则安装。", "软件更新", "检查软件更新 执行软件更新 更新软件 升级", false},
		{"校园软件更新", "只安排校园端软件更新。", "校园软件更新", "更新校园软件 升级", false},
		{"云端软件更新", "只安排云端软件更新。", "云端软件更新", "更新云端软件 升级", false},
		{"软件更新状态", "查看最近的软件更新结果和失败原因。", "软件更新状态", "更新状态 升级", false},
		{"运维日报", "查看用量、账号与软件更新的最近报告。", "运维日报", "报告", false},
	}},
	{12, "用量与管理", "用量 管理 token 成本 训练", "美元按已知API价格折算，不是订阅账单。训练控制在管理页操作，需要任务已声明训练与恢复命令。", []helpEntry{
		{"任务用量 <编号或ID>", "查看指定任务的输入、缓存、输出token及API等值美元。", "任务用量 1", "费用 美元 成本", false},
		{"会话用量", "统计当前会话的任务用量。", "会话用量", "费用 美元 成本", false},
		{"用量日报", "查看最近的每日token统计。", "用量日报", "token用量 Token用量 费用 美元", false},
		{"管理页面", "打开手机工作台：会话设置、任务、文件、资料库、指令查询和训练控制。", "管理页面", "手机管理 控制台 训练 停止训练 检查点", false},
	}},
}

func helpGroupFor(value string) (helpGroup, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, g := range commandGroups {
		if value == strconv.Itoa(g.Number) || value == g.Name {
			return g, true
		}
		for _, alias := range strings.Fields(g.Aliases) {
			if value == strings.ToLower(alias) {
				return g, true
			}
		}
	}
	return helpGroup{}, false
}
func lookupCommands(group, query string) helpDirectory {
	d := helpDirectory{Groups: commandGroups, Matches: []helpMatch{}, Query: shortPreview(strings.TrimSpace(query), 80)}
	selected, filtered := helpGroupFor(group)
	terms := strings.Fields(strings.ToLower(d.Query))
	for _, g := range commandGroups {
		if group != "" && (!filtered || selected.Number != g.Number) {
			continue
		}
		for _, e := range g.Entries {
			haystack := strings.ToLower(strings.Join([]string{g.Name, e.Syntax, e.Purpose, e.Example, e.Keywords}, " "))
			matches := true
			for _, term := range terms {
				if !strings.Contains(haystack, term) {
					matches = false
					break
				}
			}
			if matches {
				d.Matches = append(d.Matches, helpMatch{g.Name, e})
			}
		}
	}
	return d
}

func commandHelpText(input string) (string, bool) {
	input = strings.TrimSpace(input)
	search := false
	argument := ""
	handled := false
	for _, prefix := range []string{"指令详情", "指令查询", "查询指令", "查指令", "指令列表", "功能列表", "菜单", "指令", "帮助", "help"} {
		lower := strings.ToLower(input)
		if lower == prefix {
			handled = true
		} else if strings.HasPrefix(lower, prefix) {
			rest := []rune(input[len(prefix):])
			if len(rest) == 0 || (!unicode.IsSpace(rest[0]) && rest[0] != ':' && rest[0] != '：') {
				continue
			}
			argument = strings.TrimSpace(strings.TrimLeftFunc(string(rest), func(r rune) bool { return unicode.IsSpace(r) || r == ':' || r == '：' }))
			handled = true
		}
		if handled {
			search = prefix == "查指令" || prefix == "查询指令" || prefix == "指令详情"
			break
		}
	}
	if !handled {
		return "", false
	}
	var b strings.Builder
	if argument == "" {
		b.WriteString("指令查询\n查询由程序处理，不调用AI。\n")
		for _, g := range commandGroups {
			fmt.Fprintf(&b, "\n%d. %s", g.Number, g.Name)
		}
		b.WriteString("\n\n查看分类：指令 资料库（或“指令 5”）\n按关键词查：查指令 上传\n查某条用法：指令详情 完成收录\n手机页面：管理页面 → 指令查询\n请保留“指令”前缀，单独数字仍用于切换会话。")
		return b.String(), true
	}
	g, found := helpGroupFor(argument)
	d := lookupCommands("", argument)
	if found && !search {
		d = lookupCommands(g.Name, "")
		fmt.Fprintf(&b, "%d. %s\n%s\n", g.Number, g.Name, g.Note)
	} else {
		fmt.Fprintf(&b, "指令搜索：%s\n", d.Query)
	}
	if len(d.Matches) == 0 {
		b.WriteString("未找到匹配指令。发送“指令”查看分类，或尝试“查指令 文件”“查指令 模型”。")
		return b.String(), true
	}
	limit := len(d.Matches)
	if !found || search {
		if limit > 6 {
			limit = 6
		}
	}
	for _, m := range d.Matches[:limit] {
		fmt.Fprintf(&b, "\n%s", m.Syntax)
		if m.UsesAI {
			b.WriteString("【执行时调用AI】")
		}
		fmt.Fprintf(&b, "\n%s\n例：%s\n", m.Purpose, m.Example)
	}
	if limit < len(d.Matches) {
		fmt.Fprintf(&b, "\n共%d条，显示前%d条。加关键词缩小范围，或在管理页查看全部。\n", len(d.Matches), limit)
	}
	b.WriteString("\n查询不调用AI，也不执行上面的例子。发送“指令”返回分类。")
	return b.String(), true
}
func (in *inbound) helpCommand(ctx context.Context, msg weixin.Message, input string) (bool, error) {
	text, handled := commandHelpText(input)
	if !handled {
		return false, nil
	}
	return true, in.reply(ctx, msg, "help", text)
}
