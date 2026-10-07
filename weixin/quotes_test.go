package weixin

import (
	"encoding/json"
	"testing"
)

func TestReferenceIDsAndInlineContentSurviveWireRoundTrip(t *testing.T) {
	var m Message
	raw := `{"item_list":[{"type":1,"text_item":{"text":"解释它"},"ref_msg":{"svr_id":18446744073709551615,"title":"同学","message_item":{"type":1,"msg_id":18446744073709551614,"text_item":{"text":"原始内容"}},"partial_text":{"start":"原","end":"容","startindex":0,"endindex":0}}}]}`
	if e := json.Unmarshal([]byte(raw), &m); e != nil {
		t.Fatal(e)
	}
	ref := m.Items[0].Ref
	if ref == nil || ref.ServerID != "18446744073709551615" || ref.Item.MsgID != "18446744073709551614" || ref.Item.Text.Text != "原始内容" || ref.Partial.Start != "原" {
		t.Fatal("reference lost")
	}
	encoded, e := json.Marshal(m)
	if e != nil {
		t.Fatal(e)
	}
	var again Message
	if json.Unmarshal(encoded, &again) != nil || again.Items[0].Ref.Item.Text.Text != "原始内容" {
		t.Fatal("pending-batch persistence lost reference")
	}
}
