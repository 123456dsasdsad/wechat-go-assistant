// Package assistant stores user-managed preferences and explicit task schedules.
package assistant

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/models"
)

var Beijing = time.FixedZone("Asia/Shanghai", 8*3600)

type Store struct {
	mu sync.Mutex
	db *sql.DB
}
type Plan struct {
	ID, Owner, Conversation, Input, Model, Effort, Status string
	Due                                                   int64
	Daily                                                 bool
	LastJob                                               string
	Memory                                                string
}

func Open(path string) (*Store, error) {
	db, e := metadb.Open(path)
	if e != nil {
		return nil, e
	}
	metadb.KeepOpen(db)
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS preferences(owner TEXT NOT NULL,name TEXT NOT NULL,value TEXT NOT NULL,PRIMARY KEY(owner,name))`,
		`CREATE TABLE IF NOT EXISTS plans(owner TEXT NOT NULL,id TEXT NOT NULL,due INTEGER NOT NULL,status TEXT NOT NULL,document BLOB NOT NULL,PRIMARY KEY(owner,id))`,
		`CREATE INDEX IF NOT EXISTS plans_due ON plans(status,due)`,
		`CREATE TABLE IF NOT EXISTS receipts(owner TEXT NOT NULL,source TEXT NOT NULL,created INTEGER NOT NULL,reply TEXT NOT NULL,PRIMARY KEY(owner,source))`,
		`CREATE TABLE IF NOT EXISTS revisions(id TEXT PRIMARY KEY,created INTEGER NOT NULL,version TEXT NOT NULL,previous TEXT NOT NULL,backup TEXT NOT NULL,validation TEXT NOT NULL)`,
	} {
		if _, e = db.Exec(q); e != nil {
			db.Close()
			return nil, e
		}
	}
	return &Store{db: db}, nil
}
func (s *Store) Handle(owner, source, input, cid string, choice models.Choice, now time.Time) (bool, string, error) {
	input = strings.TrimSpace(input)
	kind := ""
	for _, prefix := range []string{"记忆列表", "偏好列表", "记忆帮助", "定时帮助", "定时列表", "定时任务 ", "每日任务 ", "取消定时 ", "记住 ", "忘记 "} {
		if input == strings.TrimSpace(prefix) || strings.HasPrefix(input, prefix) {
			kind = strings.TrimSpace(prefix)
			break
		}
	}
	if kind == "" {
		return false, "", nil
	}
	if owner == "" || source == "" {
		return true, "", errors.New("command_identity_required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.Begin()
	if e != nil {
		return true, "", e
	}
	defer tx.Rollback()
	var reply string
	e = tx.QueryRow("SELECT reply FROM receipts WHERE owner=? AND source=?", owner, source).Scan(&reply)
	if e == nil {
		return true, reply, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return true, "", e
	}
	argument := strings.TrimSpace(strings.TrimPrefix(input, kind))
	switch kind {
	case "记忆帮助":
		reply = "记住 <名称> <内容>\n记忆列表\n忘记 <名称>\n这些偏好会用于后续任务，可随时查看和删除。"
	case "定时帮助":
		reply = "定时任务 2026-10-09 09:00 <任务内容>\n每日任务 09:00 <任务内容>\n定时列表\n取消定时 <编号>\n使用北京时间，到点进入任务队列；执行时会调用创建计划时选择的模型。取消计划不终止已开始的任务。"
	case "记忆列表", "偏好列表":
		rows, err := tx.Query("SELECT name,value FROM preferences WHERE owner=? ORDER BY name LIMIT 32", owner)
		if err != nil {
			return true, "", err
		}
		var lines []string
		for rows.Next() {
			var name, value string
			if e = rows.Scan(&name, &value); e != nil {
				rows.Close()
				return true, "", e
			}
			lines = append(lines, name+"："+value)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return true, "", e
		}
		reply = strings.Join(lines, "\n")
		if reply == "" {
			reply = "还没有保存偏好。发送“记住 <名称> <内容>”。"
		}
	case "记住":
		parts := strings.SplitN(argument, " ", 2)
		if len(parts) != 2 || len([]rune(parts[0])) > 32 || strings.TrimSpace(parts[1]) == "" || len(parts[1]) > 2048 {
			reply = "请发送“记住 <名称> <内容>”；名称最多 32 字，内容最多 2 KiB。"
			break
		}
		var count int
		e = tx.QueryRow("SELECT count(*) FROM preferences WHERE owner=? AND name<>?", owner, parts[0]).Scan(&count)
		if e != nil {
			return true, "", e
		}
		if count >= 32 {
			reply = "偏好最多保存 32 项，请先用“忘记 <名称>”删除一项。"
			break
		}
		_, e = tx.Exec("INSERT INTO preferences VALUES(?,?,?) ON CONFLICT(owner,name) DO UPDATE SET value=excluded.value", owner, parts[0], strings.TrimSpace(parts[1]))
		if e != nil {
			return true, "", e
		}
		reply = "已保存偏好：" + parts[0] + "。后续任务生效。"
	case "忘记":
		result, err := tx.Exec("DELETE FROM preferences WHERE owner=? AND name=?", owner, argument)
		if err != nil {
			return true, "", err
		}
		n, _ := result.RowsAffected()
		reply = "未找到这项偏好。"
		if n > 0 {
			reply = "已删除偏好：" + argument
		}
	case "定时列表":
		rows, err := tx.Query("SELECT document FROM plans WHERE owner=? ORDER BY due DESC LIMIT 10", owner)
		if err != nil {
			return true, "", err
		}
		var lines []string
		for rows.Next() {
			var raw []byte
			var p Plan
			if e = rows.Scan(&raw); e != nil {
				rows.Close()
				return true, "", e
			}
			if e = json.Unmarshal(raw, &p); e != nil {
				rows.Close()
				return true, "", e
			}
			label := map[string]string{"active": "待执行", "completed": "已进入任务队列", "cancelled": "已取消"}[p.Status]
			if p.Daily {
				label += "，每日"
			}
			lines = append(lines, fmt.Sprintf("%s · %s · %s\n%s", p.ID, time.Unix(p.Due, 0).In(Beijing).Format("01-02 15:04"), label, p.Input))
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return true, "", e
		}
		reply = strings.Join(lines, "\n\n")
		if reply == "" {
			reply = "还没有定时计划。发送“定时帮助”查看格式。"
		}
	case "取消定时":
		var raw []byte
		var p Plan
		e = tx.QueryRow("SELECT document FROM plans WHERE owner=? AND id=?", owner, argument).Scan(&raw)
		if errors.Is(e, sql.ErrNoRows) {
			reply = "未找到计划编号。发送“定时列表”查看。"
			break
		}
		if e != nil {
			return true, "", e
		}
		if e = json.Unmarshal(raw, &p); e != nil {
			return true, "", e
		}
		p.Status = "cancelled"
		raw, _ = json.Marshal(p)
		if _, e = tx.Exec("UPDATE plans SET status=?,document=? WHERE owner=? AND id=?", p.Status, raw, owner, p.ID); e != nil {
			return true, "", e
		}
		reply = "已取消计划 " + p.ID + "；已进入队列的任务继续执行。"
	case "定时任务", "每日任务":
		parts := strings.Fields(argument)
		daily := kind == "每日任务"
		fields := 2
		if daily {
			fields = 1
		}
		if len(parts) <= fields {
			reply = "格式：定时任务 2026-10-09 09:00 内容；或 每日任务 09:00 内容。"
			break
		}
		var due time.Time
		var err error
		if daily {
			date := now.In(Beijing).Format("2006-01-02")
			due, err = time.ParseInLocation("2006-01-02 15:04", date+" "+parts[0], Beijing)
			if err == nil && !due.After(now) {
				due = due.Add(24 * time.Hour)
			}
		} else {
			due, err = time.ParseInLocation("2006-01-02 15:04", strings.Join(parts[:2], " "), Beijing)
		}
		body := strings.Join(parts[fields:], " ")
		if err != nil || !due.After(now) || len(body) > 4096 || !models.ValidID(choice.Model) || !models.ValidEffort(choice.Effort) {
			reply = "时间需为未来的北京时间，任务内容最多 4 KiB。"
			break
		}
		var count int
		if e = tx.QueryRow("SELECT count(*) FROM plans WHERE owner=? AND status='active'", owner).Scan(&count); e != nil {
			return true, "", e
		}
		if count >= 32 {
			reply = "最多保留 32 个有效计划，请先取消已有计划。"
			break
		}
		h := sha256.Sum256([]byte(owner + "\x00" + source))
		id := hex.EncodeToString(h[:6])
		p := Plan{ID: id, Owner: owner, Conversation: cid, Input: body, Model: choice.Model, Effort: choice.Effort, Status: "active", Due: due.Unix(), Daily: daily}
		raw, _ := json.Marshal(p)
		if _, e = tx.Exec("INSERT INTO plans VALUES(?,?,?,?,?)", owner, id, p.Due, p.Status, raw); e != nil {
			return true, "", e
		}
		reply = fmt.Sprintf("已保存计划 %s\n北京时间 %s\n模型 %s / %s\n%s", id, due.Format("2006-01-02 15:04"), choice.Model, choice.Effort, body)
		if daily {
			reply += "\n每天执行；发送“取消定时 " + id + "”可取消。"
		}
	}
	if _, e = tx.Exec("INSERT INTO receipts VALUES(?,?,?,?)", owner, source, now.UnixNano(), reply); e != nil {
		return true, "", e
	}
	if _, e = tx.Exec("DELETE FROM receipts WHERE owner=? AND source NOT IN (SELECT source FROM receipts WHERE owner=? ORDER BY created DESC LIMIT 128)", owner, owner); e != nil {
		return true, "", e
	}
	if e = tx.Commit(); e != nil {
		return true, "", e
	}
	return true, reply, nil
}

func (s *Store) MemoryText(owner string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.memoryText(owner)
}
func (s *Store) memoryText(owner string) string {
	rows, e := s.db.Query("SELECT name,value FROM preferences WHERE owner=? ORDER BY name LIMIT 32", owner)
	if e != nil {
		return ""
	}
	defer rows.Close()
	values := map[string]string{}
	for rows.Next() {
		var k, v string
		if rows.Scan(&k, &v) != nil {
			return ""
		}
		values[k] = v
	}
	if len(values) == 0 || rows.Err() != nil {
		return ""
	}
	raw, _ := json.Marshal(values)
	return string(raw)
}

// Dispatch is idempotent even if a crash occurs after enqueue and before receipt.
func (s *Store) Dispatch(now time.Time, enqueue func(Plan, string) (string, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, e := s.db.Query("SELECT document FROM plans WHERE status='active' AND due<=? ORDER BY due LIMIT 32", now.Unix())
	if e != nil {
		return e
	}
	var plans []Plan
	for rows.Next() {
		var raw []byte
		var p Plan
		if e = rows.Scan(&raw); e != nil {
			rows.Close()
			return e
		}
		if e = json.Unmarshal(raw, &p); e != nil {
			rows.Close()
			return e
		}
		plans = append(plans, p)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, p := range plans {
		source := fmt.Sprintf("schedule:%s:%s:%d", p.Owner, p.ID, p.Due)
		p.Memory = s.memoryText(p.Owner)
		job, err := enqueue(p, source)
		if err != nil {
			return err
		}
		p.LastJob = job
		if p.Daily {
			for p.Due <= now.Unix() {
				p.Due += 24 * 3600
			}
		} else {
			p.Status = "completed"
		}
		raw, _ := json.Marshal(p)
		if _, e = s.db.Exec("UPDATE plans SET due=?,status=?,document=? WHERE owner=? AND id=?", p.Due, p.Status, raw, p.Owner, p.ID); e != nil {
			return e
		}
	}
	return nil
}
