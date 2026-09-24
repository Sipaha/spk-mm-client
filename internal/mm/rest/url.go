package rest

import (
	"errors"
	"net/url"
	"strings"
)

// NormalizeURL canonicalises a server URL so the same server always compares
// equal: https:// is assumed when no scheme is given; scheme and host are
// lower-cased; trailing slashes, query and fragment are dropped. A subpath
// install (https://host/mattermost) keeps its path.
func NormalizeURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("empty URL")
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", err
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("URL must be http or https")
	}
	if u.Host == "" {
		return "", errors.New("URL has no host")
	}
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	u.User = nil
	return u.String(), nil
}
