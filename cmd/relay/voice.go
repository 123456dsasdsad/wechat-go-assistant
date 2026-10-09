package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
)

func voiceMedia(item weixin.Item, data []byte, index int) (string, []byte, error) {
	if item.Type == weixin.VideoType {
		return fmt.Sprintf("微信视频-%d.mp4", index+1), data, nil
	}
	if item.Voice == nil {
		return "", nil, errors.New("语音缺少编码信息，请上传原录音。")
	}
	ext := map[int]string{1: "wav", 5: "amr", 6: "silk", 7: "mp3", 8: "ogg"}[item.Voice.EncodeType]
	if ext == "" {
		return "", nil, errors.New("这条语音编码暂不支持，请上传 WAV、MP3、M4A 或 SILK 录音。")
	}
	if item.Voice.EncodeType == 1 {
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
