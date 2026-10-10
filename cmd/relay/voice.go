package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
	"path/filepath"
	"strings"
)

func voiceMedia(item weixin.Item, data []byte, index int) (name string, audio []byte, err error) {
	if item.Type == weixin.VideoType {
		return fmt.Sprintf("微信视频-%d.mp4", index+1), data, nil
	}
	if item.Voice == nil {
		return "", nil, errors.New("语音缺少编码信息，请上传原录音。")
	}
	rawBytes := len(data)
	defer func() {
		// Codec diagnostics contain no voice content, CDN URLs or credentials.
		fmt.Printf("{\"type\":\"voice_media_format\",\"encode_type\":%d,\"format\":%q,\"bytes\":%d,\"failed\":%t}\n", item.Voice.EncodeType, strings.TrimPrefix(filepath.Ext(name), "."), rawBytes, err != nil)
	}()
	if len(data) == 0 {
		return "", nil, errors.New("语音音频为空，请重新发送原录音。")
	}
	// encode_type is optional in iLink. Identify self-describing audio before
	// consulting it; native WeChat SILK can arrive without codec metadata.
	isWAV := len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WAVE"))
	ext := ""
	switch {
	case bytes.HasPrefix(data, []byte("#!SILK_V3")), bytes.HasPrefix(data, []byte("\x02#!SILK_V3")):
		ext = "silk"
	case isWAV:
		ext = "wav"
	default:
		ext = map[int]string{1: "wav", 5: "amr", 6: "silk", 7: "mp3", 8: "ogg"}[item.Voice.EncodeType]
	}
	if ext == "" {
		return "", nil, errors.New("这条语音编码暂不支持，请上传 WAV、MP3、M4A 或 SILK 录音。")
	}
	if ext == "wav" && !isWAV {
		v := item.Voice
		if v.BitsPerSample != 16 || v.SampleRate < 8000 || v.SampleRate > 48000 || len(data)%2 != 0 {
			return "", nil, errors.New("PCM语音缺少有效采样参数，请上传原录音。")
		}
		b := make([]byte, 44+len(data))
		copy(b, "RIFF")
		binary.LittleEndian.PutUint32(b[4:], uint32(36+len(data)))
		copy(b[8:], "WAVEfmt ")
		binary.LittleEndian.PutUint32(b[16:], 16)
		binary.LittleEndian.PutUint16(b[20:], 1)
		binary.LittleEndian.PutUint16(b[22:], 1)
		binary.LittleEndian.PutUint32(b[24:], uint32(v.SampleRate))
		binary.LittleEndian.PutUint32(b[28:], uint32(v.SampleRate*2))
		binary.LittleEndian.PutUint16(b[32:], 2)
		binary.LittleEndian.PutUint16(b[34:], 16)
		copy(b[36:], "data")
		binary.LittleEndian.PutUint32(b[40:], uint32(len(data)))
		copy(b[44:], data)
		data = b
	}
	return fmt.Sprintf("微信语音-%d.%s", index+1, ext), data, nil
}
