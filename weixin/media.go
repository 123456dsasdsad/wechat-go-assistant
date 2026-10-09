package weixin

import (
	"bytes"
	"context"
	"crypto/md5" // Required by the upstream file wire protocol, not a security signature.
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (c *Client) cdnURL(endpoint string, query url.Values) string {
	return strings.TrimRight(c.opts.CDNURL, "/") + "/" + endpoint + "?" + query.Encode()
}

func (c *Client) Upload(ctx context.Context, to string, kind int, plain []byte) (Uploaded, error) {
	var out Uploaded
	if to == "" || (kind != UploadImage && kind != UploadFile) {
		return out, errors.New("upload requires recipient and supported media type")
	}
	if int64(len(plain)) > c.opts.MaxMediaBytes {
		return out, errors.New("file exceeds configured size limit")
	}
	key := make([]byte, 16)
	fileKey := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		return out, err
	}
	if _, err := rand.Read(fileKey); err != nil {
		return out, err
	}
	cipher, err := encryptECB(plain, key)
	if err != nil {
		return out, err
	}
	digest := md5.Sum(plain)
	body := struct {
		FileKey     string   `json:"filekey"`
		MediaType   int      `json:"media_type"`
		To          string   `json:"to_user_id"`
		RawSize     int      `json:"rawsize"`
		MD5         string   `json:"rawfilemd5"`
		CipherSize  int      `json:"filesize"`
		NoThumbnail bool     `json:"no_need_thumb"`
		AESKey      string   `json:"aeskey"`
		Info        BaseInfo `json:"base_info"`
	}{hex.EncodeToString(fileKey), kind, to, len(plain), hex.EncodeToString(digest[:]), len(cipher), true, hex.EncodeToString(key), info()}
	var target struct {
		apiStatus
		URL   string `json:"upload_full_url"`
		Param string `json:"upload_param"`
	}
	if err := c.jsonRequest(ctx, c.opts.BaseURL, "ilink/bot/getuploadurl", http.MethodPost, body, true, c.opts.APITimeout, &target); err != nil {
		return out, err
	}
	if err := target.apiStatus.check("getUploadURL"); err != nil {
		return out, err
	}
	raw := strings.TrimSpace(target.URL)
	if raw == "" {
		if target.Param == "" {
			return out, errors.New("upload URL is missing")
		}
		raw = c.cdnURL("upload", url.Values{"encrypted_query_param": {target.Param}, "filekey": {body.FileKey}})
	}
	if err := c.validateURL(raw, false); err != nil {
		return out, err
	}
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		param, status, err := c.uploadBytes(ctx, raw, cipher)
		if err == nil {
			return Uploaded{param, hex.EncodeToString(key), int64(len(plain)), int64(len(cipher))}, nil
		}
		last = err
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		if status >= 300 && status < 500 {
			return out, err
		}
		if attempt < 2 {
			if err := wait(ctx, time.Duration(attempt+1)*100*time.Millisecond); err != nil {
				return out, err
			}
		}
	}
	return out, last
}

func (c *Client) uploadBytes(ctx context.Context, raw string, cipher []byte) (string, int, error) {
	ctx, cancel := context.WithTimeout(ctx, c.opts.MediaTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, raw, bytes.NewReader(cipher))
	if err != nil {
		return "", 0, errors.New("invalid CDN upload request")
	}
	req.Header.Set("Content-Type", "application/octet-stream") // No bot authorization goes to the CDN.
	res, err := c.http.Do(req)
	if err != nil {
		return "", 0, requestError(ctx, "CDN upload", err)
	}
	defer res.Body.Close()
	io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
	if res.StatusCode != 200 {
		return "", res.StatusCode, fmt.Errorf("CDN upload returned HTTP %d", res.StatusCode)
	}
	param := res.Header.Get("x-encrypted-param")
	if param == "" {
		return "", res.StatusCode, errors.New("CDN upload missing download parameter")
	}
	return param, res.StatusCode, nil
}

func (c *Client) Download(ctx context.Context, item Item) ([]byte, error) {
	var media *Media
	var key []byte
	var err error
	var expectedMD5, expectedSize string
	switch {
	case item.Type == ImageType && item.Image != nil:
		media = item.Image.Media
		if item.Image.AESKeyHex != "" {
			key, err = hex.DecodeString(item.Image.AESKeyHex)
			if err != nil || len(key) != 16 {
				return nil, errors.New("invalid image AES key")
			}
		}
	case item.Type == FileType && item.File != nil:
		media = item.File.Media
		expectedMD5, expectedSize = item.File.MD5, item.File.Length
	case item.Type == VoiceType && item.Voice != nil:
		media = item.Voice.Media
	case item.Type == VideoType && item.Video != nil:
		media = item.Video.Media
		expectedMD5 = item.Video.MD5
		if item.Video.Size > 0 {
			expectedSize = strconv.FormatInt(item.Video.Size, 10)
		}
	default:
		return nil, errors.New("unsupported media download")
	}
	if media == nil {
		return nil, errors.New("message has no media reference")
	}
	if key == nil && media.AESKey != "" {
		key, err = decodeMediaKey(media.AESKey)
		if err != nil {
			return nil, err
		}
	}
	if item.Type != ImageType && key == nil {
		return nil, errors.New("file is missing encryption key")
	}
	if expectedSize != "" {
		n, err := strconv.ParseInt(expectedSize, 10, 64)
		if err != nil || n < 0 || n > c.opts.MaxMediaBytes {
			return nil, errors.New("invalid or oversized declared file length")
		}
	}
	raw := media.FullURL
	if raw == "" {
		if media.DownloadParam == "" {
			return nil, errors.New("download URL is missing")
		}
		raw = c.cdnURL("download", url.Values{"encrypted_query_param": {media.DownloadParam}})
	}
	if err := c.validateURL(raw, false); err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, c.opts.MediaTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, errors.New("invalid CDN download request")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, requestError(callCtx, "CDN download", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("CDN download returned HTTP %d", res.StatusCode)
	}
	data, err := readBounded(res.Body, c.opts.MaxMediaBytes+16)
	if err != nil {
		return nil, err
	}
	if key != nil {
		data, err = decryptECB(data, key)
		if err != nil {
			return nil, err
		}
	}
	if int64(len(data)) > c.opts.MaxMediaBytes {
		return nil, errors.New("download exceeds configured size limit")
	}
	if expectedSize != "" && expectedSize != strconv.Itoa(len(data)) {
		return nil, errors.New("downloaded file length mismatch")
	}
	if expectedMD5 != "" {
		digest := md5.Sum(data)
		if !strings.EqualFold(expectedMD5, hex.EncodeToString(digest[:])) {
			return nil, errors.New("downloaded file MD5 mismatch")
		}
	}
	return data, nil
}

func uploadedMedia(file Uploaded) (*Media, error) {
	key, err := hex.DecodeString(file.AESKeyHex)
	if err != nil || len(key) != 16 || file.DownloadParam == "" || file.Size < 0 || file.CipherSize != (file.Size/16+1)*16 {
		return nil, errors.New("invalid uploaded media metadata")
	}
	return &Media{DownloadParam: file.DownloadParam, AESKey: base64.StdEncoding.EncodeToString([]byte(file.AESKeyHex)), EncryptType: 1}, nil
}

func (c *Client) SendFile(ctx context.Context, reply Reply, name string, file Uploaded) (SendResult, error) {
	if name == "" || strings.ContainsAny(name, "/\\\x00") {
		return SendResult{}, errors.New("file name must be a basename")
	}
	media, err := uploadedMedia(file)
	if err != nil {
		return SendResult{}, err
	}
	return c.send(ctx, reply, Item{Type: FileType, File: &FileItem{Media: media, Name: name, Length: strconv.FormatInt(file.Size, 10)}})
}

func (c *Client) SendImage(ctx context.Context, reply Reply, file Uploaded) (SendResult, error) {
	media, err := uploadedMedia(file)
	if err != nil {
		return SendResult{}, err
	}
	return c.send(ctx, reply, Item{Type: ImageType, Image: &ImageItem{Media: media, CipherSize: file.CipherSize}})
}
