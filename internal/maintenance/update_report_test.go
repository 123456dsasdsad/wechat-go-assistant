package maintenance

import "testing"

func TestUpdateReportsDistinguishDeferredAndCompletedInstallation(t *testing.T) {
	for _, tc := range []struct {
		states []string
		title  string
	}{
		{nil, "软件更新检查"},
		{[]string{"已是最新稳定版"}, "软件更新检查"},
		{[]string{"AI 任务运行中，延后自动安装", "已是最新稳定版"}, "软件更新待安装"},
		{[]string{"已自动更新，健康检查通过", "已是最新稳定版"}, "软件更新完成"},
		{[]string{"已自动更新，健康检查通过", "AI 任务运行中，延后自动安装"}, "软件更新待安装"},
		{[]string{"已自动更新，健康检查通过", "安装失败，保留现版"}, "软件更新检查"},
	} {
		var updates []Update
		for _, state := range tc.states {
			updates = append(updates, Update{State: state})
		}
		if got := UpdateReportTitle(updates); got != tc.title {
			t.Fatalf("states=%v: got %q, want %q", tc.states, got, tc.title)
		}
	}
}
