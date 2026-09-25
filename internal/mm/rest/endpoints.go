package rest

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

type ClientConfig struct {
	SiteName                string `json:"SiteName"`
	SiteURL                 string `json:"SiteURL"`
	Version                 string `json:"Version"`
	EnableSignUpWithGitLab  string `json:"EnableSignUpWithGitLab"`
	CollapsedThreads        string `json:"CollapsedThreads"`
	TeammateNameDisplay     string `json:"TeammateNameDisplay"`
	LockTeammateNameDisplay string `json:"LockTeammateNameDisplay"`
	EnableCustomEmoji       string `json:"EnableCustomEmoji"`
}

func (c ClientConfig) GitLabEnabled() bool { return c.EnableSignUpWithGitLab == "true" }

type User = model.User

// Ping checks the URL is a live Mattermost server.
func (c *Client) Ping(ctx context.Context) error {
	var out struct {
		Status string `json:"status"`
	}
	if _, err := c.do(ctx, http.MethodGet, "/api/v4/system/ping", nil, &out); err != nil {
		return err
	}
	if out.Status != "OK" {
		return &Error{Kind: KindAPI, Status: http.StatusOK, Err: fmt.Errorf("ping status %q", out.Status)}
	}
	return nil
}

func (c *Client) ClientConfig(ctx context.Context) (ClientConfig, error) {
	var cfg ClientConfig
	_, err := c.do(ctx, http.MethodGet, "/api/v4/config/client?format=old", nil, &cfg)
	return cfg, err
}

func (c *Client) Me(ctx context.Context) (User, error) {
	var u User
	_, err := c.do(ctx, http.MethodGet, "/api/v4/users/me", nil, &u)
	return u, err
}

// Login signs in with login ID + password; the session token arrives in the
// "Token" response header.
func (c *Client) Login(ctx context.Context, loginID, password string) (string, User, error) {
	var u User
	h, err := c.WithToken("").do(ctx, http.MethodPost, "/api/v4/users/login",
		map[string]string{"login_id": loginID, "password": password}, &u)
	if err != nil {
		return "", User{}, err
	}
	tok := h.Get("Token")
	if tok == "" {
		return "", User{}, &Error{Kind: KindAPI, Status: http.StatusOK, Err: errors.New("login response has no Token header")}
	}
	return tok, u, nil
}

// Logout revokes the session server-side. 401/403 mean it is already dead —
// that is the outcome we wanted, so it is success.
func (c *Client) Logout(ctx context.Context) error {
	_, err := c.do(ctx, http.MethodPost, "/api/v4/users/logout", nil, nil)
	if IsAuth(err) {
		return nil
	}
	return err
}
