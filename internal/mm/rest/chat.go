package rest

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

// SinceLimit is the server's hard cap on a posts?since= response; a result
// this large may have skipped posts and must be replaced by a fresh page.
const SinceLimit = 1000

const (
	membersPageSize = 200
	usersChunk      = 100
	statusChunk     = 100
)

func (c *Client) get(ctx context.Context, path string, out any) error {
	_, err := c.do(ctx, http.MethodGet, path, nil, out)
	return err
}

func (c *Client) MyTeams(ctx context.Context) ([]model.Team, error) {
	var out []model.Team
	return out, c.get(ctx, "/api/v4/users/me/teams", &out)
}

// MyChannels returns channels of all teams plus DMs/GMs in one array.
func (c *Client) MyChannels(ctx context.Context) ([]model.Channel, error) {
	var out []model.Channel
	return out, c.get(ctx, "/api/v4/users/me/channels", &out)
}

// MyChannelMembers pages through all of the user's memberships.
func (c *Client) MyChannelMembers(ctx context.Context) ([]model.ChannelMember, error) {
	var all []model.ChannelMember
	for page := 0; ; page++ {
		var out []model.ChannelMember
		q := url.Values{"page": {strconv.Itoa(page)}, "per_page": {strconv.Itoa(membersPageSize)}}
		if err := c.get(ctx, "/api/v4/users/me/channel_members?"+q.Encode(), &out); err != nil {
			return nil, err
		}
		all = append(all, out...)
		if len(out) < membersPageSize {
			return all, nil
		}
	}
}

func (c *Client) Categories(ctx context.Context, teamID string) (model.OrderedCategories, error) {
	var out model.OrderedCategories
	return out, c.get(ctx, "/api/v4/users/me/teams/"+url.PathEscape(teamID)+"/channels/categories", &out)
}

func (c *Client) MyPreferences(ctx context.Context) ([]model.Preference, error) {
	var out []model.Preference
	return out, c.get(ctx, "/api/v4/users/me/preferences", &out)
}

func (c *Client) MyStatus(ctx context.Context) (model.Status, error) {
	var out model.Status
	return out, c.get(ctx, "/api/v4/users/me/status", &out)
}

// UsersByIDs fetches profiles in chunks (the request body is an id array).
func (c *Client) UsersByIDs(ctx context.Context, ids []string) ([]model.User, error) {
	return c.UsersByIDsSince(ctx, ids, 0)
}

// UsersByIDsSince is UsersByIDs returning only the users updated after since
// (ms, the server compares update_at > since; a picture change moves
// update_at); since 0 returns every one.
func (c *Client) UsersByIDsSince(ctx context.Context, ids []string, since int64) ([]model.User, error) {
	path := "/api/v4/users/ids"
	if since > 0 {
		path += "?since=" + strconv.FormatInt(since, 10)
	}
	var all []model.User
	for start := 0; start < len(ids); start += usersChunk {
		end := min(start+usersChunk, len(ids))
		var out []model.User
		if _, err := c.do(ctx, http.MethodPost, path, ids[start:end], &out); err != nil {
			return nil, err
		}
		all = append(all, out...)
	}
	return all, nil
}

// PostsQuery selects one GET /channels/{id}/posts. Only one cursor is sent,
// in the server's own priority (api4/post.go getPostsForChannel): Since
// wins over After, After over Before. Page-mode answers are newest first;
// prev_post_id/next_post_id carry the server's end cursors
// (docs/research/2026-09-24-mattermost-api-facts.md §9).
type PostsQuery struct {
	PerPage          int    // page mode; 0 → 60
	Before           string // page mode: posts older than this id
	After            string // page mode: posts newer than this id (the PerPage oldest of them)
	Since            int64  // ms; >0 switches to since mode (edits/deletes included, ≤ SinceLimit)
	CollapsedThreads bool   // CRT: root posts only
}

func (c *Client) ChannelPosts(ctx context.Context, channelID string, q PostsQuery) (model.PostList, error) {
	// skipFetchThreads=true in both modes: without it the server adds every
	// thread touched by the page to the response (unused — only Order is
	// read) and, without CRT, leaves reply_count at 0 (post_store.go
	// getRootPosts/getParentsPosts, GetPostsSince: the COUNT subquery only
	// comes with skipFetchThreads).
	v := url.Values{
		"collapsedThreads":         {strconv.FormatBool(q.CollapsedThreads)},
		"collapsedThreadsExtended": {"false"},
		"skipFetchThreads":         {"true"},
	}
	if q.Since > 0 {
		v.Set("since", strconv.FormatInt(q.Since, 10))
	} else {
		per := q.PerPage
		if per <= 0 {
			per = 60
		}
		v.Set("page", "0")
		v.Set("per_page", strconv.Itoa(per))
		switch {
		case q.After != "":
			v.Set("after", q.After)
		case q.Before != "":
			v.Set("before", q.Before)
		}
	}
	var out model.PostList
	err := c.get(ctx, "/api/v4/channels/"+url.PathEscape(channelID)+"/posts?"+v.Encode(), &out)
	return out, err
}

// StatusesByIDs fetches presence in chunks. The server answers "offline" for
// users it has no status for, and an empty list if statuses are disabled.
func (c *Client) StatusesByIDs(ctx context.Context, ids []string) ([]model.Status, error) {
	var all []model.Status
	for start := 0; start < len(ids); start += statusChunk {
		end := min(start+statusChunk, len(ids))
		var out []model.Status
		if _, err := c.do(ctx, http.MethodPost, "/api/v4/users/status/ids", ids[start:end], &out); err != nil {
			return nil, err
		}
		all = append(all, out...)
	}
	return all, nil
}

func (c *Client) FileInfo(ctx context.Context, fileID string) (model.FileInfo, error) {
	var out model.FileInfo
	return out, c.get(ctx, "/api/v4/files/"+url.PathEscape(fileID)+"/info", &out)
}

// CustomEmojiPage lists custom emoji by name; perPage ≤ 200 (server cap).
func (c *Client) CustomEmojiPage(ctx context.Context, page, perPage int) ([]model.Emoji, error) {
	q := url.Values{"page": {strconv.Itoa(page)}, "per_page": {strconv.Itoa(perPage)}, "sort": {"name"}}
	var out []model.Emoji
	return out, c.get(ctx, "/api/v4/emoji?"+q.Encode(), &out)
}

// EmojiByName fetches a custom emoji; 404 when there is none with that name.
func (c *Client) EmojiByName(ctx context.Context, name string) (model.Emoji, error) {
	var out model.Emoji
	return out, c.get(ctx, "/api/v4/emoji/name/"+url.PathEscape(name), &out)
}
