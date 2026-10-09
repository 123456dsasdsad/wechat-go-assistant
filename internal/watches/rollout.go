package watches

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"
)

// Reader only extracts assistant messages intended for the user. Tool input,
// tool output, user messages and reasoning never enter the progress mirror.
type Reader struct {
	Offset int64
	Thread string
	Latest Update
}

func (r *Reader) Read(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	stat, e := f.Stat()
	if e != nil {
		return e
	}
	if stat.Size() < r.Offset {
		return errors.New("rollout_truncated")
	}
	if _, e = f.Seek(r.Offset, io.SeekStart); e != nil {
		return e
	}
	b := bufio.NewReader(f)
	for {
		line, e := b.ReadBytes('\n')
		if e == io.EOF {
			return nil
		} // Leave a partial final line for the next poll.
		if e != nil {
			return e
		}
		r.Offset += int64(len(line))
		var row struct {
			Timestamp time.Time       `json:"timestamp"`
			Type      string          `json:"type"`
			Payload   json.RawMessage `json:"payload"`
		}
		if json.Unmarshal(line, &row) != nil {
			return errors.New("invalid_rollout_record")
		}
		switch row.Type {
		case "session_meta":
			var p struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(row.Payload, &p) != nil || p.ID != r.Thread {
				return errors.New("wrong_rollout_thread")
			}
		case "event_msg":
			var p struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(row.Payload, &p) != nil {
				continue
			}
			switch p.Type {
			case "task_started":
				r.Latest.State = "running"
				r.Latest.Text = "新一轮任务已开始，等待进度说明。"
			case "task_complete":
				r.Latest.State = "done"
			case "turn_aborted":
				r.Latest.State = "stopped"
			default:
				continue
			}
			r.Latest.Observed = row.Timestamp
			r.Latest.Sequence = uint64(r.Offset)
		case "response_item":
			var p struct {
				Type    string `json:"type"`
				Role    string `json:"role"`
				Phase   string `json:"phase"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			}
			if json.Unmarshal(row.Payload, &p) != nil || p.Type != "message" || p.Role != "assistant" || (p.Phase != "commentary" && p.Phase != "final") {
				continue
			}
			var parts []string
			for _, c := range p.Content {
				if c.Type == "output_text" {
					parts = append(parts, c.Text)
				}
			}
			text := strings.TrimSpace(strings.Join(parts, "\n"))
			if text == "" {
				continue
			}
			runes := []rune(text)
			if len(runes) > 5000 {
				text = string(runes[:5000]) + "\n…完整回复请在原 Codex 会话查看。"
			}
			r.Latest.Text = text
			r.Latest.Observed = row.Timestamp
			r.Latest.Sequence = uint64(r.Offset)
		}
	}
}
