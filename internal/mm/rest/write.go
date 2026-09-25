package rest

import (
	"context"
	"net/http"
	"net/url"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

// createPostReq is the subset of a Post the server needs; sending the whole
// model.Post would post zero-valued fields (props, create_at) we never set.
type createPostReq struct {
	ChannelID     string `json:"channel_id"`
	Message       string `json:"message"`
	RootID        string `json:"root_id,omitempty"`
	PendingPostID string `json:"pending_post_id,omitempty"`
	UserID        string `json:"user_id,omitempty"`
}

// CreatePost is never retried on transport errors (do() only retries GET);
// resending with the same pending_post_id is safe — the server returns the
// already-created post for 30 s.
func (c *Client) CreatePost(ctx context.Context, p model.Post) (model.Post, error) {
	var out model.Post
	_, err := c.do(ctx, http.MethodPost, "/api/v4/posts",
		createPostReq{ChannelID: p.ChannelID, Message: p.Message, RootID: p.RootID, PendingPostID: p.PendingPostID, UserID: p.UserID}, &out)
	return out, err
}

func (c *Client) PatchPost(ctx context.Context, postID, message string) (model.Post, error) {
	var out model.Post
	_, err := c.do(ctx, http.MethodPut, "/api/v4/posts/"+url.PathEscape(postID)+"/patch", map[string]string{"message": message}, &out)
	return out, err
}

func (c *Client) DeletePost(ctx context.Context, postID string) error {
	_, err := c.do(ctx, http.MethodDelete, "/api/v4/posts/"+url.PathEscape(postID), nil, nil)
	return err
}

func (c *Client) ViewChannel(ctx context.Context, channelID string) error {
	_, err := c.do(ctx, http.MethodPost, "/api/v4/channels/members/me/view",
		map[string]any{"channel_id": channelID, "prev_channel_id": "", "collapsed_threads_supported": true}, nil)
	return err
}

func (c *Client) SetUnread(ctx context.Context, postID string) (model.ChannelUnreadAt, error) {
	var out model.ChannelUnreadAt
	_, err := c.do(ctx, http.MethodPost, "/api/v4/users/me/posts/"+url.PathEscape(postID)+"/set_unread",
		map[string]bool{"collapsed_threads_supported": true}, &out)
	return out, err
}

func (c *Client) SavePreferences(ctx context.Context, prefs []model.Preference) error {
	_, err := c.do(ctx, http.MethodPut, "/api/v4/users/me/preferences", prefs, nil)
	return err
}
