package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type transcript struct {
	Engine   string  `json:"engine"`
	Source   string  `json:"source"`
	Language string  `json:"language"`
	Duration float64 `json:"duration"`
	Segments []struct {
		Start float64 `json:"start"`
		End   float64 `json:"end"`
		Text  string  `json:"text"`
	} `json:"segments"`
}

func audioInput(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".wav", ".mp3", ".m4a", ".flac", ".ogg", ".silk", ".amr", ".mp4", ".aac":
		return true
	}
	return false
}
func transcribeInput(ctx context.Context, cfg config, root, relative string) ([]map[string]string, error) {
	if cfg.TranscriptionCommand == "" {
		return nil, errors.New("transcription_engine_unavailable")
	}
	if !filepath.IsAbs(cfg.TranscriptionCommand) {
		return nil, errors.New("transcription_command_invalid")
	}
	src := filepath.Join(root, filepath.FromSlash(relative))
	dst := src + ".transcript.json"
	timeout := cfg.TranscriptionTimeoutSeconds
	if timeout <= 0 {
		timeout = 3600
	}
	c, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(c, cfg.TranscriptionCommand, src, dst)
	if e := cmd.Run(); e != nil {
		return nil, errors.New("audio_transcription_failed")
	}
	stat, e := os.Stat(dst)
	if e != nil || stat.Size() > 8<<20 {
		return nil, errors.New("audio_transcript_invalid")
	}
	raw, e := os.ReadFile(dst)
	var v transcript
	if e != nil || json.Unmarshal(raw, &v) != nil || v.Engine == "" || v.Duration < 0 || math.IsNaN(v.Duration) || len(v.Segments) > 100000 {
		return nil, errors.New("audio_transcript_invalid")
	}
	var b strings.Builder
	b.WriteString("本地语音识别结果（可能有识别错误，原始录音仍保留）：\n")
	last := 0.0
	for _, s := range v.Segments {
		if s.Start < last || s.End < s.Start || s.End > v.Duration+5 || math.IsNaN(s.End) {
			return nil, errors.New("audio_transcript_invalid")
		}
		last = s.Start
		fmt.Fprintf(&b, "[%.2fs–%.2fs] %s\n", s.Start, s.End, s.Text)
	}
	if len(v.Segments) == 0 {
		b.WriteString("没有识别到语音内容。请勿猜测指令。\n")
	}
	text := src + ".transcript.txt"
	if e = os.WriteFile(text, []byte(b.String()), 0600); e != nil {
		return nil, e
	}
	return []map[string]string{{"name": filepath.Base(text), "path": relative + ".transcript.txt"}, {"name": filepath.Base(dst), "path": relative + ".transcript.json"}}, nil
}
