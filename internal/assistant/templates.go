package assistant

import (
	"encoding/json"
	"errors"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"os"
	"sort"
	"strings"
	"sync"
)

type Template struct {
	Name string `json:"name"`
	Body string `json:"body"`
}
type Templates struct {
	mu    sync.Mutex
	path  string
	items map[string]Template
}

func OpenTemplates(path string) (*Templates, error) {
	s := &Templates{path: path, items: map[string]Template{}}
	b, e := metadb.ReadJSON(path)
	if e == nil {
		if json.Unmarshal(b, &s.items) != nil || len(s.items) > 64 {
			return nil, errors.New("invalid_templates")
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	} else {
		for _, v := range []Template{{"审查代码", "请实际读取项目代码，检查可复现的错误、边界条件和必要测试，说明文件位置。任务要求：{{内容}}"}, {"运行项目", "请读取项目说明，配置依赖并实际运行，记录真实结果和必要的产物。任务要求：{{内容}}"}, {"分析数据", "请读取数据，核对字段与缺失值，完成分析并导出图表和结果文件。任务要求：{{内容}}"}, {"训练出图", "请检查训练代码并适配当前 CPU 环境；明确训练参数、进度文件及可用的 checkpoint 恢复命令，实际训练并导出图片。任务要求：{{内容}}"}} {
			s.items[v.Name] = v
		}
	}
	return s, metadb.WriteJSON(path, s.items)
}
func (s *Templates) List() []Template {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Template{}
	for _, v := range s.items {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
func (s *Templates) Save(v Template) error {
	if strings.TrimSpace(v.Name) == "" || len(v.Name) > 80 || strings.ContainsAny(v.Name, "\n\r:") || strings.TrimSpace(v.Body) == "" || len(v.Body) > 7000 {
		return errors.New("invalid_template")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := map[string]Template{}
	for k, v := range s.items {
		next[k] = v
	}
	next[v.Name] = v
	if len(next) > 64 {
		return errors.New("templates_full")
	}
	if e := metadb.WriteJSON(s.path, next); e != nil {
		return e
	}
	s.items = next
	return nil
}
func (s *Templates) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := map[string]Template{}
	for k, v := range s.items {
		if k != name {
			next[k] = v
		}
	}
	if e := metadb.WriteJSON(s.path, next); e != nil {
		return e
	}
	s.items = next
	return nil
}
func (s *Templates) Expand(name, body string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.items[name]
	if !ok {
		return "", errors.New("template_not_found")
	}
	out := strings.ReplaceAll(v.Body, "{{内容}}", body)
	if !strings.Contains(v.Body, "{{内容}}") && body != "" {
		out += "\n" + body
	}
	if len(out) > 8192 {
		return "", errors.New("template_task_too_long")
	}
	return out, nil
}
