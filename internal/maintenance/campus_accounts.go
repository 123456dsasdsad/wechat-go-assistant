package maintenance

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type AccountProbe struct {
	OK     bool   `json:"ok"`
	HTTP   int    `json:"http,omitempty"`
	Models int    `json:"models,omitempty"`
	Error  string `json:"error,omitempty"`
}

type CampusAccounts struct {
	Day         string       `json:"day"`
	Checked     time.Time    `json:"checked"`
	Source      *Report      `json:"source,omitempty"`
	SourceError string       `json:"source_error,omitempty"`
	Stale       bool         `json:"stale"`
	Relay       AccountProbe `json:"relay"`
	Gateway     AccountProbe `json:"gateway"`
}

func accountProbeGET(ctx context.Context, c HTTPDoer, endpoint, key string, target any) AccountProbe {
	if endpoint == "" || key == "" {
		return AccountProbe{Error: "未配置检查入口或凭据"}
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if e != nil {
		return AccountProbe{Error: "检查入口配置无效"}
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, e := c.Do(req)
	if e != nil {
		return AccountProbe{Error: "网络连接失败"}
	}
	defer resp.Body.Close()
	p := AccountProbe{HTTP: resp.StatusCode}
	if resp.StatusCode != http.StatusOK {
		p.Error = "接口未接受检查"
		return p
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if e != nil || len(b) > 65536 || json.Unmarshal(b, target) != nil {
		p.Error = "检查响应无效"
		return p
	}
	p.OK = true
	return p
}

// CheckCampusAccounts only reads the shared pool snapshot and local tunnel
// endpoints. It never infers, refreshes account tokens, or changes the pool.
func CheckCampusAccounts(ctx context.Context, c HTTPDoer, relayURL, relayKey, gatewayURL, gatewayKey, day string, now time.Time) CampusAccounts {
	r := CampusAccounts{Day: day, Checked: now.UTC()}
	var snapshot struct {
		Report Report `json:"report"`
	}
	p := accountProbeGET(ctx, c, strings.TrimRight(relayURL, "/")+"/maintenance/reports?host=cloud&kind=accounts", relayKey, &snapshot)
	if !p.OK {
		r.SourceError = fmt.Sprintf("%s（HTTP %d）", p.Error, p.HTTP)
	} else if !snapshot.Report.valid() || snapshot.Report.Host != "cloud" || snapshot.Report.Kind != "accounts" || snapshot.Report.Created.IsZero() || snapshot.Report.Created.After(now.Add(5*time.Minute)) {
		r.SourceError = "共享账号检查响应无效"
	} else {
		r.Source = &snapshot.Report
		r.Stale = r.Source.Day != day || now.Sub(r.Source.Created) > 26*time.Hour
	}
	var health struct {
		OK bool `json:"ok"`
	}
	r.Relay = accountProbeGET(ctx, c, strings.TrimRight(relayURL, "/")+"/health", relayKey, &health)
	if r.Relay.OK && !health.OK {
		r.Relay.OK = false
		r.Relay.Error = "通道健康状态未通过"
	}
	var models struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	endpoint := ""
	if gatewayURL != "" {
		endpoint = strings.TrimRight(gatewayURL, "/") + "/models"
	}
	r.Gateway = accountProbeGET(ctx, c, endpoint, gatewayKey, &models)
	if r.Gateway.OK {
		seen := map[string]bool{}
		for _, m := range models.Data {
			if strings.TrimSpace(m.ID) != "" {
				seen[m.ID] = true
			}
		}
		r.Gateway.Models = len(seen)
		if len(seen) == 0 {
			r.Gateway.OK = false
			r.Gateway.Error = "模型目录为空或无效"
		}
	}
	return r
}

// SameResult ignores probe time and notification receipts so a scheduled retry
// only publishes changed source data, freshness, or connectivity.
func (r CampusAccounts) SameResult(other CampusAccounts) bool {
	if r.Day != other.Day || r.Stale != other.Stale || r.SourceError != other.SourceError || r.Relay != other.Relay || r.Gateway != other.Gateway {
		return false
	}
	if r.Source == nil || other.Source == nil {
		return r.Source == nil && other.Source == nil
	}
	return r.Source.ID == other.Source.ID && r.Source.Created.Equal(other.Source.Created) && r.Source.Text == other.Source.Text
}

func (r CampusAccounts) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "【校园账号检查｜%s】\n检查时间：%s（北京时间）\n校园使用阿里云共享账号池，以下列出该池的具体检查结果；不重复清理或重复计算账号。", r.Day, r.Checked.In(Beijing).Format("2006-01-02 15:04:05"))
	probe := func(label string, p AccountProbe) {
		if p.OK {
			fmt.Fprintf(&b, "\n%s：正常（HTTP %d）", label, p.HTTP)
			if p.Models > 0 {
				fmt.Fprintf(&b, "，可读取 %d 个模型", p.Models)
			}
		} else {
			fmt.Fprintf(&b, "\n%s：未通过，%s（HTTP %d）", label, p.Error, p.HTTP)
		}
	}
	probe("校园→阿里云私有通道", r.Relay)
	probe("校园→共享模型入口", r.Gateway)
	b.WriteString("\n模型目录检查不调用 AI，也不等同于逐账号推理测试。")
	if r.Source == nil {
		fmt.Fprintf(&b, "\n未取得共享账号检查明细：%s。账号可用性和清理结果待核实，不能据通道正常判定账号有效。", r.SourceError)
	} else {
		fmt.Fprintf(&b, "\n来源检查时间：%s（北京时间）", r.Source.Created.In(Beijing).Format("2006-01-02 15:04:05"))
		if r.Stale {
			b.WriteString("\n注意：以下为旧快照，今日最新检查尚未取得；更新后会自动补充。")
		}
		b.WriteString("\n\n")
		b.WriteString(AccountParagraphs(r.Source.Text))
	}
	text := []rune(b.String())
	if len(text) > 5000 {
		text = append(text[:4900], []rune("\n明细较长，以上已截断；发送“账号状态”查看共享池报告。")...)
	}
	return string(text)
}
