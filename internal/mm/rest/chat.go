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
	var all []model.User
	for start := 0; start < len(ids); start += usersChunk {
		end := min(start+usersChunk, len(ids))
		var out []model.User
		if _, err := c.do(ctx, http.MethodPost, "/api/v4/users/ids", ids[start:end], &out); err != nil {
			return nil, err
		}
		all = append(all, out...)
	}
	return all, nil
}

type PostsQuery struct {
	PerPage          int    // page mode; 0 → 60
	Before           string // page mode: posts older than this id
	Since            int64  // ms; >0 switches to since mode (edits/deletes included, ≤ SinceLimit)
	CollapsedThreads bool   // CRT: root posts only
}

func (c *Client) ChannelPosts(ctx context.Context, channelID string, q PostsQuery) (model.PostList, error) {
	v := url.Values{
		"collapsedThreads":         {strconv.FormatBool(q.CollapsedThreads)},
		"collapsedThreadsExtended": {"false"},
	}
	if q.Since > 0 {
		v.Set("since", strconv.FormatInt(q.Since, 10))
		v.Set("skipFetchThreads", "true")
	} else {
		per := q.PerPage
		if per <= 0 {
			per = 60
		}
		v.Set("page", "0")
		v.Set("per_page", strconv.Itoa(per))
		if q.Before != "" {
			v.Set("before", q.Before)
		}
	}
	var out model.PostList
	err := c.get(ctx, "/api/v4/channels/"+url.PathEscape(channelID)+"/posts?"+v.Encode(), &out)
	return out, err
}
