package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
)

func TestVoiceMediaRecognizesSILKWithoutCodecMetadata(t *testing.T) {
	for _, header := range []string{"#!SILK_V3", "\x02#!SILK_V3"} {
		for _, codec := range []int{0, 1, 2, 4, 6, 99} {
			data := append([]byte(header), 3, 0, 1, 2, 3)
			item := weixin.Item{Type: weixin.VoiceType, Voice: &weixin.VoiceItem{EncodeType: codec}}
			name, got, err := voiceMedia(item, data, 0)
			if err != nil || !strings.HasSuffix(name, ".silk") || !bytes.Equal(got, data) {
				t.Fatalf("SILK codec=%d header=%q: name=%q err=%v", codec, header, name, err)
			}
		}
	}
}

func TestVoiceMediaDoesNotWrapWAVTwice(t *testing.T) {
	pcm := []byte{1, 0, 2, 0}
	item := weixin.Item{Type: weixin.VoiceType, Voice: &weixin.VoiceItem{EncodeType: 1, BitsPerSample: 16, SampleRate: 24000}}
	_, wav, err := voiceMedia(item, pcm, 0)
	if err != nil || len(wav) != 44+len(pcm) || binary.LittleEndian.Uint32(wav[24:]) != 24000 {
		t.Fatal("valid PCM was not wrapped", err)
	}
	item.Voice = &weixin.VoiceItem{EncodeType: 1}
	name, got, err := voiceMedia(item, wav, 0)
	if err != nil || !strings.HasSuffix(name, ".wav") || !bytes.Equal(got, wav) {
		t.Fatal("existing WAV requires PCM metadata or was wrapped twice", name, err)
	}
}

func TestVoiceMediaRejectsUnidentifiedBytesAndInvalidPCM(t *testing.T) {
	for _, item := range []*weixin.VoiceItem{
		nil,
		{},
		{EncodeType: 2},
		{EncodeType: 99},
		{EncodeType: 1, BitsPerSample: 8, SampleRate: 24000},
		{EncodeType: 1, BitsPerSample: 16, SampleRate: 1},
		{EncodeType: 1, BitsPerSample: 16, SampleRate: 24000},
	} {
		if _, _, err := voiceMedia(weixin.Item{Type: weixin.VoiceType, Voice: item}, []byte{1, 2, 3}, 0); err == nil {
			t.Fatalf("unidentified or invalid PCM accepted: %+v", item)
		}
	}
}

func TestNativeVoiceWithoutCodecEnqueuesPreservedAudio(t *testing.T) {
	defer metadb.CloseAll()
	in := inboundFixture(t)
	data := append([]byte("\x02#!SILK_V3"), 3, 0, 1, 2, 3)
	in.client.(*fakeMessages).download = data
	msg := textMessage("native-voice", "")
	msg.Items = []weixin.Item{{Type: weixin.VoiceType, Voice: &weixin.VoiceItem{Media: &weixin.Media{}}}}
	if err := in.handle(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	j, _ := in.queue.Claim(time.Now())
	if j == nil || len(j.Attachments) != 1 || !strings.HasSuffix(j.Attachments[0].Name, ".silk") || !strings.Contains(j.Input, "本地转写") {
		t.Fatal("native voice did not enter transcription task", in.client.(*fakeMessages).text)
	}
	f, err := in.files.OpenBlob(j.Attachments[0])
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, err := io.ReadAll(f)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal("original voice bytes changed", err)
	}
}
