// Package weixin implements a standalone Go client for the Tencent iLink bot
// protocol. It has no dependency on the OpenClaw runtime or Node.js.
package weixin

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
)

const (
	DefaultAPI      = "https://ilinkai.weixin.qq.com"
	DefaultCDN      = "https://novac2c.cdn.weixin.qq.com/c2c"
	ProtocolVersion = "2.4.9" // Wire compatibility reference, not this program's version.
	TextType        = 1
	ImageType       = 2
	FileType        = 4
	UploadImage     = 1
	UploadFile      = 3
)

// ID accepts JSON strings and uint64 numbers without float64 conversion.
type ID string

func (id *ID) UnmarshalJSON(raw []byte) error {
	raw = bytes.TrimSpace(raw)
	if bytes.Equal(raw, []byte("null")) {
		*id = ""
		return nil
	}
	if len(raw) > 0 && raw[0] == '"' {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		*id = ID(value)
		return nil
	}
	if _, err := strconv.ParseUint(string(raw), 10, 64); err != nil {
		return errors.New("invalid message identifier")
	}
	*id = ID(raw)
	return nil
}

type BaseInfo struct {
	ChannelVersion string `json:"channel_version"`
	BotAgent       string `json:"bot_agent"`
}

type apiStatus struct {
	Ret     int `json:"ret"`
	ErrCode int `json:"errcode"`
}

type Media struct {
	DownloadParam string `json:"encrypt_query_param,omitempty"`
	AESKey        string `json:"aes_key,omitempty"`
	EncryptType   int    `json:"encrypt_type,omitempty"`
	FullURL       string `json:"full_url,omitempty"`
}

type TextItem struct {
	Text string `json:"text"`
}
type ImageItem struct {
	Media      *Media `json:"media,omitempty"`
	AESKeyHex  string `json:"aeskey,omitempty"`
	CipherSize int64  `json:"mid_size,omitempty"`
}
type FileItem struct {
	Media  *Media `json:"media,omitempty"`
	Name   string `json:"file_name,omitempty"`
	MD5    string `json:"md5,omitempty"`
	Length string `json:"len,omitempty"`
}
type Item struct {
	Type  int         `json:"type"`
	MsgID ID          `json:"msg_id,omitempty"`
	Ref   *RefMessage `json:"ref_msg,omitempty"`
	Text  *TextItem   `json:"text_item,omitempty"`
	Image *ImageItem  `json:"image_item,omitempty"`
	File  *FileItem   `json:"file_item,omitempty"`
}

// RefMessage can contain the original item, a summary, or only a server ID.
type RefMessage struct {
	Item     *Item        `json:"message_item,omitempty"`
	Title    string       `json:"title,omitempty"`
	ServerID ID           `json:"svr_id,omitempty"`
	Partial  *PartialText `json:"partial_text,omitempty"`
}

type PartialText struct {
	Start      string `json:"start"`
	End        string `json:"end"`
	StartIndex int    `json:"startindex"`
	EndIndex   int    `json:"endindex"`
	MD5        string `json:"quotemd5,omitempty"`
}

type Message struct {
	MessageID    ID     `json:"message_id,omitempty"`
	FromUserID   string `json:"from_user_id"`
	ToUserID     string `json:"to_user_id,omitempty"`
	ClientID     string `json:"client_id,omitempty"`
	Type         int    `json:"message_type"`
	State        int    `json:"message_state,omitempty"`
	Items        []Item `json:"item_list,omitempty"`
	ContextToken string `json:"context_token,omitempty"`
	SessionID    string `json:"session_id,omitempty"`
	GroupID      string `json:"group_id,omitempty"`
	RunID        string `json:"run_id,omitempty"`
	CreatedAt    int64  `json:"create_time_ms,omitempty"`
}

func (m Message) Key() string {
	if m.MessageID != "" {
		return string(m.MessageID)
	}
	return m.ClientID
}

type Updates struct {
	apiStatus
	Messages  []Message `json:"msgs"`
	Cursor    string    `json:"get_updates_buf"`
	TimeoutMS int64     `json:"longpolling_timeout_ms,omitempty"`
}

type Reply struct{ ToUserID, ContextToken, ClientID, RunID string }
type SendResult struct {
	apiStatus
	MessageID ID     `json:"message_id,omitempty"`
	ClientID  string `json:"-"`
}

type Account struct {
	BotToken string `json:"bot_token"`
	BotID    string `json:"bot_id"`
	OwnerID  string `json:"owner_id"`
	BaseURL  string `json:"base_url"`
}

type QRCode struct {
	apiStatus
	Code    string `json:"qrcode"`
	Content string `json:"qrcode_img_content"`
}

type QRStatus struct {
	apiStatus
	Status       string `json:"status"`
	BotToken     string `json:"bot_token"`
	BotID        string `json:"ilink_bot_id"`
	OwnerID      string `json:"ilink_user_id"`
	BaseURL      string `json:"baseurl"`
	RedirectHost string `json:"redirect_host"`
}

type Uploaded struct {
	DownloadParam string
	AESKeyHex     string
	Size          int64
	CipherSize    int64
}
