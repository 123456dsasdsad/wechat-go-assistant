package library

import (
	"fmt"
	"strings"
)

// Matrix is rendered from saved method cards, preserving each experimental condition.
func Matrix(materials []Material) Section {
	sec := Section{Name: "方法对比表（原始条件）"}
	var b strings.Builder
	b.WriteString("| 文献 | 方法 | 实验条件 | 已报告结果 | 局限 |\n|---|---|---|---|---|\n")
	clean := func(s string) string { s = strings.ReplaceAll(s, "|", "\\|"); return strings.ReplaceAll(s, "\n", " ") }
	for _, m := range materials {
		fields := map[string][]string{}
		for _, c := range m.Claims {
			fields[c.Field] = append(fields[c.Field], c.Text)
		}
		value := func(k string) string {
			v := strings.Join(fields[k], "；")
			if v == "" {
				v = "未取得相关证据"
			}
			return clean(v)
		}
		fmt.Fprintf(&b, "| [资料 %d] %s | %s | %s | %s | %s |\n", m.ID, clean(m.Title), value("idea"), value("experiment"), value("result"), value("limitation"))
		sec.MaterialIDs = append(sec.MaterialIDs, m.ID)
	}
	sec.Text = b.String() + "\n不同数据、拆分、指标和实验协议下的数值不能直接比较。"
	return sec
}
