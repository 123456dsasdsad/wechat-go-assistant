package main

import (
	"context"
	"errors"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"io"
	"net/http"
	"strings"
	"time"
)

func downloadAttachment(ctx context.Context, relayURL, key, jobID, lease, dir string, ref files.Ref) (string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(relayURL, "/")+"/jobs/"+jobID+"/files/"+ref.ID, nil)
	if err != nil {
		return "", err
	}
	r.Header.Set("Authorization", "Bearer "+key)
	r.Header.Set("X-Job-Lease", lease)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 30 * time.Second
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(r)
	if err != nil {
		return "", errors.New("attachment_download_failed")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", errors.New("attachment_download_failed")
	}
	if res.ContentLength != ref.Size || res.Header.Get("X-File-SHA256") != ref.SHA256 {
		return "", errors.New("attachment_integrity_failed")
	}
	timer := time.AfterFunc(2*time.Minute, cancel)
	defer timer.Stop()
	return files.PrepareContext(ctx, dir, ref, &progressReader{reader: res.Body, timer: timer})
}

type progressReader struct {
	reader io.Reader
	timer  *time.Timer
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if err != nil {
		r.timer.Stop()
	} else if n > 0 {
		r.timer.Reset(2 * time.Minute)
	}
	return n, err
}
func attachmentError(err error) string {
	switch err.Error() {
	case "attachment_storage_full", "attachment_download_failed", "attachment_integrity_failed", "archive_format_unsupported", "invalid_zip", "zip_entry_limit", "zip_unsafe_path", "zip_duplicate_path", "zip_unsupported_entry", "zip_expansion_limit", "zip_entry_unreadable", "zip_path_conflict":
		return err.Error()
	}
	return "attachment_prepare_failed"
}
