package settings

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/models"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type receipt struct{ ID, Reply string }
type state struct {
	Version  int           `json:"version"`
	Choice   models.Choice `json:"choice"`
	Receipts []receipt     `json:"receipts"`
}
type Store struct {
	mu      sync.Mutex
	path    string
	catalog models.Catalog
	state   state
}

func Open(path string, catalog models.Catalog) (*Store, error) {
	if path == "" {
		return nil, errors.New("settings_path_required")
	}
	if err := catalog.Validate(); err != nil {
		return nil, err
	}
	initial, err := catalog.Resolve("gpt-6-sol", "high")
	if err != nil {
		return nil, errors.New("initial_model_missing")
	}
	s := &Store{path: path, catalog: catalog, state: state{Version: 1, Choice: initial}}
	b, err := os.ReadFile(path)
	if err == nil {
		if len(b) > 1024*1024 || json.Unmarshal(b, &s.state) != nil || s.state.Version != 1 {
			return nil, errors.New("invalid_settings")
		}
		if _, err = catalog.Resolve(s.state.Choice.Model, s.state.Choice.Effort); err != nil {
			return nil, errors.New("saved_model_not_available")
		}
	} else if !os.IsNotExist(err) {
		return nil, errors.New("settings_unreadable")
	}
	if err = s.save(s.state); err != nil {
		return nil, err
	}
	return s, nil
}
func (s *Store) save(next state) error {
	b, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".settings-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), s.path); err != nil {
		return err
	}
	s.state = next
	return nil
}
func (s *Store) Current() models.Choice { s.mu.Lock(); defer s.mu.Unlock(); return s.state.Choice }
func (s *Store) list() string {
	var b strings.Builder
	b.WriteString("已验证可选模型：\n")
	for n, m := range s.catalog.Models {
		fmt.Fprintf(&b, "%d. %s（推理：%s）\n", n+1, m.ID, strings.Join(m.Efforts, "/"))
	}
	b.WriteString("发送：默认模型 <编号或型号>\n临时指定：使用 <型号>：任务内容")
	return b.String()
}
func (s *Store) Handle(source, input string) (bool, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	input = strings.TrimSpace(input)
	kind, value, handled := parseCommand(input)
	if !handled {
		return false, "", nil
	}
	if source == "" {
		return true, "", errors.New("command_id_required")
	}
	sum := sha256.Sum256([]byte(source))
	id := hex.EncodeToString(sum[:12])
	for _, r := range s.state.Receipts {
		if r.ID == id {
			return true, r.Reply, nil
		}
	}
	next := s.state
	reply := ""
	switch kind {
	case "模型列表":
		reply = s.list()
	case "当前模型", "当前设置":
		reply = fmt.Sprintf("当前默认模型：%s\n推理强度：%s\n校园 Codex CLI；任务目录只读。新任务使用此设置，已有任务保持原选择。", next.Choice.Model, next.Choice.Effort)
	case "模型帮助", "帮助":
		reply = "模型列表\n当前设置\n默认模型 <编号或型号>\n推理强度 <等级>\n使用 <型号>：任务内容\n切换默认设置只影响后续任务。"
	default:
		if kind == "invalid" || len(strings.Fields(value)) != 1 {
			reply = "请每条消息发送一条设置指令：默认模型gpt-6-luna；推理强度low。空格或冒号也可以。发送“模型列表”查看可选项。"
			break
		}
		if kind == "推理强度" {
			effort := value
			aliases := map[string]string{"低": "low", "中": "medium", "高": "high", "超高": "xhigh", "最大": "max", "极高": "ultra"}
			if a := aliases[effort]; a != "" {
				effort = a
			}
			choice, err := s.catalog.Resolve(next.Choice.Model, effort)
			if err != nil {
				reply = "该模型不支持这个推理等级。\n" + s.list()
			} else {
				next.Choice = choice
				reply = fmt.Sprintf("已保存：%s，推理强度 %s。后续任务生效。", choice.Model, choice.Effort)
			}
		} else {
			choice, err := s.catalog.Resolve(value, next.Choice.Effort)
			if err != nil {
				choice, err = s.catalog.Resolve(value, "")
			}
			if err != nil {
				reply = "该模型未接入或未通过验证，设置未改变。\n" + s.list()
			} else {
				next.Choice = choice
				reply = fmt.Sprintf("已保存默认模型：%s\n推理强度：%s\n后续任务生效，已有任务保持原选择。", choice.Model, choice.Effort)
			}
		}
	}
	next.Receipts = append(append([]receipt(nil), s.state.Receipts...), receipt{id, reply})
	if len(next.Receipts) > 128 {
		next.Receipts = next.Receipts[len(next.Receipts)-128:]
	}
	if err := s.save(next); err != nil {
		return true, "", errors.New("settings_save_failed")
	}
	return true, reply, nil
}
func (s *Store) ChoiceForTask(input string) (models.Choice, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	input = strings.TrimSpace(input)
	if strings.HasPrefix(input, "使用 ") || strings.HasPrefix(input, "使用gpt-") {
		rest := strings.TrimSpace(strings.TrimPrefix(input, "使用"))
		index := strings.IndexAny(rest, ":：")
		if index < 0 {
			return models.Choice{}, "", errors.New("temporary_model_syntax")
		}
		id := strings.TrimSpace(rest[:index])
		body := strings.TrimSpace(rest[index:])
		body = strings.TrimSpace(strings.TrimLeft(body, ":："))
		if body == "" {
			return models.Choice{}, "", errors.New("temporary_task_empty")
		}
		choice, err := s.catalog.Resolve(id, s.state.Choice.Effort)
		if err != nil {
			choice, err = s.catalog.Resolve(id, "")
		}
		if err != nil {
			return models.Choice{}, "", err
		}
		return choice, body, nil
	}
	return s.state.Choice, input, nil
}
