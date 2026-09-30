package mmsync

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sort"
	"strings"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/state"
)

// Message search (spec «Поиск», «Факты API» and Секция 3): the current
// team's POST /teams/{id}/posts/search, one page per call, under the
// caller's context like Autocomplete — never retried (a POST), never asked
// offline. Paging is the same for both server engines
// (docs/research/2026-09-24-mattermost-api-facts.md §9.1): more may follow
// while a raw page holds ≥ SearchPerPage; at most SearchMaxPages pages a
// search session (memory), the last one saying LimitReached instead of
// HasNext.

const (
	// SearchPerPage is the page size asked for (the webapp's).
	SearchPerPage = 20
	// SearchMaxPages bounds a search session: pages 0…SearchMaxPages-1.
	SearchMaxPages = 25
)

var (
	// ErrUnknownTeam is a team this server's user is not on.
	ErrUnknownTeam = errors.New("mmsync: unknown team")
	// ErrSearchPage is a page outside 0…SearchMaxPages-1.
	ErrSearchPage = errors.New("mmsync: search page out of range")
	// ErrSuggestKind is a suggestion kind search does not offer.
	ErrSuggestKind = errors.New("mmsync: unknown suggestion kind")
	// ErrOffline means the worker is not live; nothing was asked.
	ErrOffline = errOffline
)

// SearchHit is a found post as the feed shows it, with its channel.
// ChannelName is what in: takes (without a DM/GM's "@"): a team channel's
// slug, a DM partner's username, a GM's members' "a,b,c"; ChannelDisplay
// the sidebar's name. A channel the state does not hold (its metadata may
// lag): Jumpable false, ChannelName/Display/Type empty. Matches: the
// words the server matched in this very post (Elasticsearch only; empty
// on Bleve and the database engine).
type SearchHit struct {
	state.PostView
	ChannelID      string   `json:"channel_id"`
	ChannelName    string   `json:"channel_name"`
	ChannelDisplay string   `json:"channel_display"`
	ChannelType    string   `json:"channel_type"`
	Jumpable       bool     `json:"jumpable"`
	Matches        []string `json:"matches"`
}

// SearchPage is one page of hits, newest first. HasNext: ask page+1;
// LimitReached: there is more, but the session's last page was reached.
type SearchPage struct {
	Hits         []SearchHit `json:"hits"`
	HasNext      bool        `json:"has_next"`
	LimitReached bool        `json:"limit_reached"`
}

// HasTeam reports whether teamID is a team of this server's user.
func (w *Worker) HasTeam(teamID string) bool {
	return slices.Contains(w.st.TeamIDs(), teamID)
}

// SearchPosts runs terms (the server parses from:, in:, dates, phrases…)
// in teamID and returns page (0…SearchMaxPages-1). tzOffset: seconds east
// of UTC (the day bounds of on:/before:/after:).
func (w *Worker) SearchPosts(ctx context.Context, teamID, terms string, page, tzOffset int) (SearchPage, error) {
	if page < 0 || page >= SearchMaxPages {
		return SearchPage{}, ErrSearchPage
	}
	if !w.HasTeam(teamID) {
		return SearchPage{}, ErrUnknownTeam
	}
	if w.Status() != StatusLive {
		return SearchPage{}, ErrOffline
	}
	ctx, cancel := w.withLife(ctx)
	defer cancel()
	raw, err := w.rc.SearchPosts(ctx, teamID, model.SearchParams{Terms: terms, TimeZoneOffset: tzOffset, Page: page, PerPage: SearchPerPage})
	if err != nil {
		if sessionExpired(err) {
			w.signalAuth()
		}
		return SearchPage{}, err
	}
	posts := make([]model.Post, 0, len(raw.Order))
	var authors, channels []string
	for _, id := range raw.Order {
		p, ok := raw.Posts[id]
		if !ok || p.DeleteAt != 0 {
			continue
		}
		posts = append(posts, p)
		authors = append(authors, p.UserID)
		channels = append(channels, p.ChannelID)
	}
	w.loadProfiles(ctx, authors)
	if err := ctx.Err(); err != nil {
		return SearchPage{}, err
	}
	views := w.st.PostViews(posts)
	labels := w.st.ChannelLabels(channels)
	out := SearchPage{Hits: make([]SearchHit, 0, len(posts)), HasNext: len(raw.Order) >= SearchPerPage}
	if out.HasNext && page == SearchMaxPages-1 {
		out.HasNext, out.LimitReached = false, true
	}
	for i, p := range posts {
		hit := SearchHit{PostView: views[i], ChannelID: p.ChannelID, Matches: []string{}}
		if l, ok := labels[p.ChannelID]; ok {
			hit.ChannelName, hit.ChannelDisplay, hit.ChannelType, hit.Jumpable = l.Name, l.Display, l.Type, true
		}
		if m := raw.Matches[p.ID]; len(m) > 0 {
			hit.Matches = append(hit.Matches, m...)
		}
		out.Hits = append(out.Hits, hit)
	}
	return out, nil
}

// loadProfiles reads the profiles of ids the state lacks, in one batch
// under usersMu (like ReactionUsers); a failure leaves them unnamed.
func (w *Worker) loadProfiles(ctx context.Context, ids []string) {
	missing := w.st.MissingAmong(uniq(ids))
	if len(missing) == 0 {
		return
	}
	w.usersMu.Lock()
	defer w.usersMu.Unlock()
	users, err := w.rc.UsersByIDs(ctx, missing)
	if err != nil {
		if sessionExpired(err) {
			w.signalAuth()
		}
		slog.Warn("profiles unavailable", "srv", w.srv.ID, "err", err)
		return
	}
	if len(users) > 0 {
		w.st.SetUsers(users)
	}
}

func uniq(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// acMaxDirect caps the DMs/GMs of one in: answer.
const acMaxDirect = 25

// SearchSuggest answers the search box's from: (ACUsers) and in:
// (ACChannels) suggestions in teamID — not tied to the open channel:
// Users are the team's members (Others whatever the server puts apart —
// nothing, without a channel); Channels the team's (by slug) and then our
// DMs as "@username" and GMs as "@a,b,c", the Names in: takes. prefix may
// start with "~" (a channel) or "@" (DMs/GMs only). Cached and asked only
// while live, like Autocomplete; offline the DMs/GMs still come from the
// state.
func (w *Worker) SearchSuggest(ctx context.Context, teamID, kind, prefix string) (Autocomplete, error) {
	if kind != ACUsers && kind != ACChannels {
		return Autocomplete{}, ErrSuggestKind
	}
	if !w.HasTeam(teamID) {
		return Autocomplete{}, ErrUnknownTeam
	}
	prefix = strings.ToLower(prefix)
	ctx, cancel := w.withLife(ctx)
	defer cancel()
	out := Autocomplete{Users: []ACUser{}, Others: []ACUser{}, Channels: []ACChannel{}, Emoji: []string{}, Commands: []ACCommand{}}
	if kind == ACUsers {
		prefix = strings.TrimPrefix(prefix, "@")
		v, err := acFetch(ctx, w, acUsersKey("", teamID, prefix), func(ctx context.Context) (acUsers, error) {
			return w.fetchUsers(ctx, teamID, "", prefix)
		})
		if err != nil {
			return out, err
		}
		out.Users, out.Others = w.acUsers(v.res.Users, v.status, prefix), w.acUsers(v.res.OutOfChannel, v.status, prefix)
		return out, nil
	}
	directOnly := strings.HasPrefix(prefix, "@")
	prefix = strings.TrimLeft(prefix, "~@")
	if !directOnly {
		v, err := acFetch(ctx, w, ACChannels+"\x00"+teamID+"\x00"+prefix, func(ctx context.Context) ([]model.Channel, error) {
			return w.rc.AutocompleteChannels(ctx, teamID, prefix)
		})
		if err != nil {
			return out, err
		}
		out.Channels = w.acChannels(v)
	}
	direct, err := w.directSuggestions(ctx, prefix)
	if err != nil {
		return out, err
	}
	out.Channels = append(out.Channels, direct...)
	return out, nil
}

// directSuggestions: our DMs and GMs matching prefix — a DM by its
// partner's username or name, a GM by its "a,b,c" or any member's
// username — DMs first, by name. Partners whose profile is missing are
// read first; a DM still without one (or a GM whose name the server cut)
// is left out: in: could not name it.
func (w *Worker) directSuggestions(ctx context.Context, prefix string) ([]ACChannel, error) {
	list := w.st.DirectChannels()
	var partners []string
	for _, d := range list {
		if d.Type == model.ChannelDirect && d.Name == "" {
			partners = append(partners, d.PartnerID)
		}
	}
	if len(partners) > 0 && w.Status() == StatusLive {
		w.loadProfiles(ctx, partners)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		list = w.st.DirectChannels()
	}
	out := []ACChannel{}
	for _, d := range list {
		if d.Name == "" || !directMatches(d, prefix) {
			continue
		}
		out = append(out, ACChannel{ID: d.ID, Name: "@" + d.Name, DisplayName: d.Display, Type: d.Type, Joined: true})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type == model.ChannelDirect
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out[:min(len(out), acMaxDirect)], nil
}

func directMatches(d state.DirectChannel, prefix string) bool {
	if prefix == "" || strings.HasPrefix(strings.ToLower(d.Name), prefix) {
		return true
	}
	if d.Type == model.ChannelGroup {
		for n := range strings.SplitSeq(strings.ToLower(d.Name), ",") {
			if strings.HasPrefix(n, prefix) {
				return true
			}
		}
		return false
	}
	return strings.HasPrefix(strings.ToLower(d.Display), prefix)
}
