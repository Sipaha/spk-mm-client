package rest

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

const (
	// ThreadPageDefault is PerPage's fallback: never call the thread
	// endpoint without perPage — omitting it makes the server return the
	// whole thread (docs/research/2026-09-24-mattermost-api-facts.md §8).
	ThreadPageDefault = 60
	// ThreadPageMax mirrors the server's PerPageMaximum.
	ThreadPageMax = 200
)

// ThreadQuery is GET .../posts/{root}/thread's parameters. Direction is
// "up" (older replies, newest first) unless Down: "down" pages to newer
// replies, oldest first (a thread opened around one reply). PerPage 0
// becomes ThreadPageDefault and is capped at ThreadPageMax, so the server
// is never asked for perPage=0 (whole-thread) by construction.
type ThreadQuery struct {
	PerPage          int
	FromCreateAt     int64
	FromPost         string
	CollapsedThreads bool
	Down             bool
}

// PostThread fetches one page of a thread: the root (always order[0]) plus
// up to PerPage replies beyond the cursor FromCreateAt/FromPost — older
// ones newest first, or with Down newer ones oldest first. has_next tells
// whether more exist in that direction.
func (c *Client) PostThread(ctx context.Context, rootID string, q ThreadQuery) (model.PostList, error) {
	per := q.PerPage
	if per <= 0 {
		per = ThreadPageDefault
	}
	if per > ThreadPageMax {
		per = ThreadPageMax
	}
	dir := "up"
	if q.Down {
		dir = "down"
	}
	v := url.Values{
		"perPage":          {strconv.Itoa(per)},
		"direction":        {dir},
		"collapsedThreads": {strconv.FormatBool(q.CollapsedThreads)},
	}
	if q.FromCreateAt > 0 {
		v.Set("fromCreateAt", strconv.FormatInt(q.FromCreateAt, 10))
	}
	if q.FromPost != "" {
		v.Set("fromPost", q.FromPost)
	}
	var out model.PostList
	err := c.get(ctx, "/api/v4/posts/"+url.PathEscape(rootID)+"/thread?"+v.Encode(), &out)
	return out, err
}

// MarkThreadRead is PUT .../threads/{root}/read/{ts}: no request body. A
// 404 means the caller (us) is not subscribed to the thread — callers
// classify that themselves (rest.Error.Status), the client does not retry.
func (c *Client) MarkThreadRead(ctx context.Context, teamID, rootID string, ts int64) error {
	_, err := c.do(ctx, http.MethodPut,
		"/api/v4/users/me/teams/"+url.PathEscape(teamID)+"/threads/"+url.PathEscape(rootID)+"/read/"+strconv.FormatInt(ts, 10),
		nil, nil)
	return err
}

// TeamsUnread is GET /users/me/teams/unread. With includeCollapsed, each
// team's Thread* fields cover its own subscribed threads only — DM/GM
// threads (team_id "") are never included here; see ThreadTotals for those.
func (c *Client) TeamsUnread(ctx context.Context, includeCollapsed bool) ([]model.TeamUnread, error) {
	v := url.Values{"include_collapsed_threads": {strconv.FormatBool(includeCollapsed)}}
	var out []model.TeamUnread
	return out, c.get(ctx, "/api/v4/users/me/teams/unread?"+v.Encode(), &out)
}

// ThreadTotals is GET .../teams/{team}/threads?totalsOnly=true: subscribed
// thread totals for one team. excludeDirect drops DM/GM threads (team_id
// "") from the count — used to isolate the DM/GM contribution by diffing
// two calls (docs/research/2026-09-24-mattermost-api-facts.md §8).
func (c *Client) ThreadTotals(ctx context.Context, teamID string, excludeDirect bool) (model.ThreadTotals, error) {
	v := url.Values{"totalsOnly": {"true"}}
	if excludeDirect {
		v.Set("excludeDirect", "true")
	}
	var out model.ThreadTotals
	err := c.get(ctx, "/api/v4/users/me/teams/"+url.PathEscape(teamID)+"/threads?"+v.Encode(), &out)
	return out, err
}
