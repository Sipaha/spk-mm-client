// Package rest is a typed client for the Mattermost REST API v4.
package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"
)

const (
	maxAttempts   = 5
	baseBackoff   = 100 * time.Millisecond
	maxBackoff    = 5 * time.Second
	maxRetryAfter = 60 * time.Second
	errBodyLimit  = 64 << 10
)

type Client struct {
	base  string
	token string
	hc    *http.Client
	sleep func(context.Context, time.Duration) error
}

// New builds a client for baseURL (already normalized). A nil hc gets a 30s
// timeout — http.DefaultClient has none, and a hung server once stalled a
// whole sync loop in spk-cockpit.
func New(baseURL, token string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{base: baseURL, token: token, hc: hc, sleep: sleepCtx}
}

// WithToken returns a copy that authenticates with token ("" = anonymous).
func (c *Client) WithToken(token string) *Client {
	cp := *c
	cp.token = token
	return &cp
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func backoff(attempt int) time.Duration {
	d := baseBackoff << (attempt - 1)
	if d > maxBackoff || d <= 0 {
		return maxBackoff
	}
	return d
}

func retryAfter(h http.Header, fallback time.Duration) time.Duration {
	secs, err := strconv.Atoi(h.Get("Retry-After"))
	if err != nil || secs <= 0 {
		return fallback
	}
	d := time.Duration(secs) * time.Second
	if d > maxRetryAfter {
		return maxRetryAfter
	}
	return d
}

// do sends one API call. 429 is retried for every method (the server
// rejected it before doing anything); transport errors only for GET, since
// replaying a POST could post a message twice. out may be nil.
func (c *Client) do(ctx context.Context, method, path string, in, out any) (http.Header, error) {
	var body []byte
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return nil, err
		}
	}
	for attempt := 1; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		resp, err := c.hc.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, &Error{Kind: KindNetwork, Err: ctx.Err()}
			}
			if method == http.MethodGet && attempt < maxAttempts {
				if serr := c.sleep(ctx, backoff(attempt)); serr != nil {
					return nil, &Error{Kind: KindNetwork, Err: serr}
				}
				continue
			}
			return nil, &Error{Kind: KindNetwork, Err: err}
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt < maxAttempts {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, errBodyLimit))
			_ = resp.Body.Close()
			if serr := c.sleep(ctx, retryAfter(resp.Header, backoff(attempt))); serr != nil {
				return nil, &Error{Kind: KindNetwork, Err: serr}
			}
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			return resp.Header, classify(resp)
		}
		if out != nil {
			if err := json.NewDecoder(resp.Body).Decode(out); err != nil && !errors.Is(err, io.EOF) {
				return resp.Header, &Error{Kind: KindAPI, Status: resp.StatusCode, Err: err}
			}
		}
		return resp.Header, nil
	}
}

func classify(resp *http.Response) error {
	e := &Error{Status: resp.StatusCode}
	var body struct {
		ID      string `json:"id"`
		Message string `json:"message"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, errBodyLimit))
	if json.Unmarshal(raw, &body) == nil {
		e.ID, e.Message = body.ID, body.Message
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		e.Kind = KindAuth
	case resp.StatusCode >= 500:
		e.Kind = KindNetwork
	default:
		e.Kind = KindAPI
	}
	return e
}
