package rest

import (
	"context"
	"net/http"
	"net/url"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

const (
	// SearchPageDefault is SearchParams.PerPage's fallback — the webapp's
	// page size.
	SearchPageDefault = 20
	// SearchPageMax caps PerPage (the server's PerPageMaximum).
	SearchPageMax = 200
)

// SearchPosts is POST /teams/{id}/posts/search. Like every POST it is never
// retried (do: a transport error or 5xx is returned as is). The answer's
// order is newest first. Bleve (the target server's engine) pages by offset
// (page × PerPage); the database fallback ignores PerPage and answers
// page > 0 with nothing (docs/research/2026-09-24-mattermost-api-facts.md
// §9.1) — on both, more may follow only while a raw page holds ≥ PerPage.
func (c *Client) SearchPosts(ctx context.Context, teamID string, p model.SearchParams) (model.PostSearchResults, error) {
	if p.PerPage <= 0 {
		p.PerPage = SearchPageDefault
	}
	p.PerPage = min(p.PerPage, SearchPageMax)
	var out model.PostSearchResults
	_, err := c.do(ctx, http.MethodPost, "/api/v4/teams/"+url.PathEscape(teamID)+"/posts/search", p, &out)
	return out, err
}

// Post is GET /posts/{id}: 404 for an unknown or deleted post, 403 when the
// caller may not read its channel.
func (c *Client) Post(ctx context.Context, id string) (model.Post, error) {
	var out model.Post
	return out, c.get(ctx, "/api/v4/posts/"+url.PathEscape(id), &out)
}
