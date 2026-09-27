package rest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

type FileInfo = model.FileInfo

// fileUploadResponse is api4/file.go's FileUploadResponse: the FileInfo of
// every uploaded file plus the client_id each one arrived with (the simple
// mode used here always uploads exactly one file per request).
type fileUploadResponse struct {
	FileInfos []model.FileInfo `json:"file_infos"`
	ClientIDs []string         `json:"client_ids"`
}

// progressReader calls report with the cumulative bytes read after every
// Read that returns data; report may be nil.
type progressReader struct {
	r      io.Reader
	sent   int64
	report func(sent int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.sent += int64(n)
		if p.report != nil {
			p.report(p.sent)
		}
	}
	return n, err
}

// UploadFile uploads one file in the server's "simple" mode: a raw body
// (never multipart, never JSON/base64) with a required Content-Length,
// channel_id/filename/client_id (client_id omitted when empty) in the
// query. Long transfers should use WithHTTPClient(transfer) — the client
// used for media (no whole-request timeout, ctx bounds it instead).
//
// Like every POST this is never retried on a transport error (do() only
// retries GET): resending an upload after a failure would only orphan the
// first attempt's file, so retry is the caller's decision. progress, when
// not nil, is called after every chunk read from body with the cumulative
// bytes sent so far; it is not throttled — a caller wanting ~4 Hz UI
// updates throttles itself.
func (c *Client) UploadFile(ctx context.Context, channelID, filename, clientID string, body io.Reader, size int64, progress func(sent int64)) (FileInfo, error) {
	q := url.Values{"channel_id": {channelID}, "filename": {filename}}
	if clientID != "" {
		q.Set("client_id", clientID)
	}
	if progress != nil {
		body = &progressReader{r: body, report: progress}
	}
	if c.lim != nil {
		if err := c.lim.Wait(ctx); err != nil {
			return FileInfo{}, &Error{Kind: KindNetwork, Err: err}
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/v4/files?"+q.Encode(), body)
	if err != nil {
		return FileInfo{}, err
	}
	req.ContentLength = size
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return FileInfo{}, &Error{Kind: KindNetwork, Err: ctx.Err()}
		}
		return FileInfo{}, &Error{Kind: KindNetwork, Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return FileInfo{}, classify(resp)
	}
	var out fileUploadResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil && !errors.Is(err, io.EOF) {
		return FileInfo{}, &Error{Kind: KindAPI, Status: resp.StatusCode, Err: err}
	}
	if len(out.FileInfos) == 0 {
		return FileInfo{}, &Error{Kind: KindAPI, Status: resp.StatusCode, Err: errors.New("upload response has no file_infos")}
	}
	return out.FileInfos[0], nil
}
