// threadwatch publishes visible progress from one desktop Codex task over SSH.
package main

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/conversations"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/watches"
)

type config struct {
	Rollout      string `json:"rollout"`
	Thread       string `json:"thread"`
	Title        string `json:"title"`
	Conversation string `json:"conversation"`
	SSHTarget    string `json:"ssh_target"`
	RemoteConfig string `json:"remote_config"`
}

func encodedPS(s string) string {
	u := utf16.Encode([]rune(s))
	b := make([]byte, len(u)*2)
	for i, v := range u {
		binary.LittleEndian.PutUint16(b[i*2:], v)
	}
	return base64.StdEncoding.EncodeToString(b)
}
func push(ctx context.Context, c config, u watches.Update) error {
	b, e := json.Marshal(u)
	if e != nil {
		return e
	}
	// Credentials stay on the relay host. Only the selected user-visible text
	// crosses the existing SSH connection; no API key is copied to this PC.
	script := `$ErrorActionPreference='Stop';$OutputEncoding=[Console]::OutputEncoding=[Text.UTF8Encoding]::new($false);$cfg=Get-Content -Raw -Encoding UTF8 '` + strings.ReplaceAll(c.RemoteConfig, "'", "''") + `'|ConvertFrom-Json;$key=[IO.File]::ReadAllText($cfg.key_file).Trim();$bytes=[Convert]::FromBase64String([Console]::In.ReadToEnd().Trim());$v=Invoke-RestMethod ('http://'+$cfg.listen+'/watch/update') -Method POST -Headers @{Authorization=('Bearer '+$key)} -ContentType 'application/json' -Body $bytes -TimeoutSec 15;$v|ConvertTo-Json -Compress`
	cmd := exec.CommandContext(ctx, "ssh", "-T", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=10", c.SSHTarget, "powershell.exe", "-NoProfile", "-NonInteractive", "-EncodedCommand", encodedPS(script))
	cmd.Stdin = strings.NewReader(base64.StdEncoding.EncodeToString(b))
	output, e := cmd.Output()
	if e != nil {
		return errors.New("progress_sync_failed")
	}
	var reply struct {
		OK bool `json:"ok"`
	}
	if json.Unmarshal([]byte(strings.TrimPrefix(strings.TrimSpace(string(output)), "\ufeff")), &reply) != nil || !reply.OK {
		return errors.New("progress_sync_not_accepted")
	}
	return nil
}
func main() {
	path := flag.String("config", "", "private configuration file")
	once := flag.Bool("once", false, "synchronize once and exit")
	flag.Parse()
	b, e := os.ReadFile(*path)
	var c config
	if e != nil || json.Unmarshal(b, &c) != nil || !conversations.ValidID(c.Conversation) || !conversations.ValidThread(c.Thread) || c.SSHTarget == "" || strings.HasPrefix(c.SSHTarget, "-") || c.Title == "" || c.RemoteConfig == "" {
		fmt.Println(`{"ok":false,"error":"invalid_config"}`)
		os.Exit(1)
	}
	r := watches.Reader{Thread: c.Thread, Latest: watches.Update{Conversation: c.Conversation, Thread: c.Thread, Title: c.Title}}
	for {
		e = r.Read(c.Rollout)
		if e == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
			e = push(ctx, c, r.Latest)
			cancel()
		}
		if e != nil {
			fmt.Println(`{"ok":false,"error":"progress_sync_failed"}`)
		} else {
			fmt.Printf("{\"ok\":true,\"sequence\":%d,\"state\":%q}\n", r.Latest.Sequence, r.Latest.State)
		}
		if *once {
			if e != nil {
				os.Exit(1)
			}
			return
		}
		time.Sleep(20 * time.Second)
	}
}
