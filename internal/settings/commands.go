package settings

import (
	"strings"
	"unicode"
)

// parseCommand recognizes explicit controls, without interpreting ordinary
// questions about models as settings. The catalog validates the value later.
func parseCommand(input string) (kind, value string, handled bool) {
	input = strings.TrimSpace(input)
	switch input {
	case "模型列表", "当前模型", "当前设置", "模型帮助", "帮助":
		return input, "", true
	}
	if lines := strings.Split(input, "\n"); len(lines) > 1 {
		if _, _, ok := parseCommand(lines[0]); ok {
			return "invalid", "", true
		}
	}
	for _, prefix := range []string{"请将模型切换为", "请把模型改为", "将模型切换为", "把默认模型改为", "把模型改为", "模型切换到"} {
		if strings.HasPrefix(input, prefix) {
			return "默认模型", commandValue(strings.TrimPrefix(input, prefix)), true
		}
	}
	for _, prefix := range []string{"默认模型", "切换模型", "推理强度", "/model", "模型"} {
		if !strings.HasPrefix(input, prefix) {
			continue
		}
		rest := strings.TrimPrefix(input, prefix)
		if rest == "" {
			return prefix, "", true
		}
		first := []rune(rest)[0]
		explicit := unicode.IsSpace(first) || first == ':' || first == '：' || first == '=' || first < 128
		for _, op := range []string{"改为", "改成", "换成", "切换为", "切换到", "设为", "为", "到"} {
			if strings.HasPrefix(rest, op) {
				rest = strings.TrimPrefix(rest, op)
				explicit = true
				break
			}
		}
		if prefix == "推理强度" {
			for _, alias := range []string{"低", "中", "高", "超高", "最大", "极高"} {
				if strings.TrimSpace(rest) == alias {
					explicit = true
				}
			}
		}
		if explicit {
			return prefix, commandValue(rest), true
		}
	}
	return "", "", false
}

func commandValue(rest string) string {
	rest = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(rest), ":：="))
	return strings.TrimSpace(strings.TrimRight(rest, "。！!"))
}
