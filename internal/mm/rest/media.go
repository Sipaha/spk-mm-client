package rest

import (
	"context"
	"io"
	"net/http"
)

// WithHTTPClient returns a copy that sends through hc, keeping the token and
// the limiter: file transfers use a client without a whole-request timeout
// (the caller's context bounds them).
func (c *Client) WithHTTPClient(hc *http.Client) *Client {
	cp := *c
	cp.hc = hc
	return &cp
}

// Stream GETs path (an API path, query included) and hands the open response
// to the caller, who must close its body: avatars, thumbnails, files. Like do
// it takes a limiter token per attempt and waits out 429; unlike do it never
// decodes the body and never retries transport errors — a half-read stream
// cannot be resumed transparently. A status ≥ 400 comes back as *Error.
func (c *Client) Stream(ctx context.Context, path string, hdr http.Header) (*http.Response, error) {
	for attempt := 1; ; attempt++ {
		if c.lim != nil {
			if err := c.lim.Wait(ctx); err != nil {
				return nil, &Error{Kind: KindNetwork, Err: err}
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
		if err != nil {
			return nil, err
		}
		for k, vs := range hdr {
			for _, v := range vs {
				req.Header.Add(k, v)
			}
		}
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		resp, err := c.hc.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, &Error{Kind: KindNetwork, Err: ctx.Err()}
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
		if resp.StatusCode >= 400 {
			err := classify(resp)
			_ = resp.Body.Close()
			return nil, err
		}
		return resp, nil
	}
}
