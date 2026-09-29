package mmsync

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/mm/rest"
)

// Composer autocomplete (brief 2026-09-29): @users, ~channels, :emoji: and
// /commands from the server, only on demand (nothing at startup), each
// request under its caller's context (a stale one is cancelled by the UI),
// answers kept in a small per-worker LRU (aclru.go). Offline nothing is
// asked: users/channels/commands come back empty and emoji from the custom
// emoji index only.

// Autocomplete kinds.
const (
	ACUsers    = "users"
	ACChannels = "channels"
	ACEmoji    = "emoji"
	ACCommands = "commands"
)

// Caps on what one answer carries.
const (
	acMaxUsers    = 25 // per list (in the channel / outside it)
	acMaxChannels = 40
	acMaxEmoji    = 25
	acMaxCommands = 50
	acMaxStatuses = 50
)

var (
	// ErrNoChannel is a channel this worker does not hold (we are not in it).
	ErrNoChannel = errors.New("mmsync: unknown channel")
	// ErrCommandNotFound means the server has no command with that
	// trigger — the webapp then offers to send the text as a message.
	ErrCommandNotFound = errors.New("mmsync: command not found")
)

// ACUser is a user suggestion. Avatar is the picture version for
// /media/<srv>/avatar/<id>; Status "" when unknown.
type ACUser struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	FullName string `json:"full_name,omitempty"`
	Nickname string `json:"nickname,omitempty"`
	Avatar   string `json:"avatar,omitempty"`
	Status   string `json:"status,omitempty"`
	Bot      bool   `json:"bot,omitempty"`
	Me       bool   `json:"me,omitempty"`
}

// ACChannel is a channel suggestion; Name is what ~ inserts.
type ACChannel struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Type        string `json:"type"`
	Joined      bool   `json:"joined,omitempty"`
}

// ACCommand is a slash command suggestion.
type ACCommand struct {
	Trigger     string `json:"trigger"`
	Hint        string `json:"hint,omitempty"`
	Description string `json:"description,omitempty"`
}

// Autocomplete is one answer: only the kind asked for is filled. Users are
// the channel's members, Others the team's members outside it; Channels
// joined ones first; Emoji custom names (the UI adds the standard set).
type Autocomplete struct {
	Users    []ACUser    `json:"users"`
	Others   []ACUser    `json:"others"`
	Channels []ACChannel `json:"channels"`
	Emoji    []string    `json:"emoji"`
	Commands []ACCommand `json:"commands"`
}

// Autocomplete answers the composer's popup for channelID (the thread
// composer passes its channel). prefix is what follows the trigger
// character, already checked by the caller (length, characters).
func (w *Worker) Autocomplete(ctx context.Context, kind, channelID, prefix string) (Autocomplete, error) {
	team, ok := w.st.AutocompleteScope(channelID)
	if !ok {
		return Autocomplete{}, ErrNoChannel
	}
	prefix = strings.ToLower(prefix)
	out := Autocomplete{Users: []ACUser{}, Others: []ACUser{}, Channels: []ACChannel{}, Emoji: []string{}, Commands: []ACCommand{}}
	switch kind {
	case ACUsers:
		v, err := acFetch(ctx, w, kind+"\x00"+channelID+"\x00"+prefix, func(ctx context.Context) (acUsers, error) {
			return w.fetchUsers(ctx, team, channelID, prefix)
		})
		if err != nil {
			return out, err
		}
		out.Users, out.Others = w.acUsers(v.res.Users, v.status, prefix), w.acUsers(v.res.OutOfChannel, v.status, prefix)
	case ACChannels:
		v, err := acFetch(ctx, w, kind+"\x00"+team+"\x00"+prefix, func(ctx context.Context) ([]model.Channel, error) {
			return w.rc.AutocompleteChannels(ctx, team, prefix)
		})
		if err != nil {
			return out, err
		}
		out.Channels = w.acChannels(v)
	case ACEmoji:
		var server []model.Emoji
		if w.st.CustomEmojiEnabled() {
			v, err := acFetch(ctx, w, kind+"\x00\x00"+prefix, func(ctx context.Context) ([]model.Emoji, error) {
				return w.rc.AutocompleteEmoji(ctx, prefix)
			})
			if err != nil && !errors.Is(err, errOffline) {
				return out, err
			}
			server = v
		}
		out.Emoji = w.acEmoji(prefix, server)
	case ACCommands:
		v, err := acFetch(ctx, w, kind+"\x00"+team, func(ctx context.Context) ([]model.Command, error) {
			return w.rc.AutocompleteCommands(ctx, team)
		})
		if err != nil {
			return out, err
		}
		out.Commands = acCommands(v, prefix)
	}
	return out, nil
}

var errOffline = errors.New("mmsync: offline")

// acFetch answers from the cache, else — only while live — asks the server
// and caches the answer; offline and uncached it is (zero, nil) for all
// but emoji (which needs to know: errOffline is swallowed by the caller).
func acFetch[T any](ctx context.Context, w *Worker, key string, fetch func(context.Context) (T, error)) (T, error) {
	var zero T
	if v, ok := w.ac.get(key); ok {
		return v.(T), nil
	}
	if w.Status() != StatusLive {
		if strings.HasPrefix(key, ACEmoji+"\x00") {
			return zero, errOffline
		}
		return zero, nil
	}
	v, err := fetch(ctx)
	if err != nil {
		if sessionExpired(err) {
			w.signalAuth()
		}
		return zero, err
	}
	if w.life.Err() == nil { // a stopped worker keeps nothing
		w.ac.put(key, v)
	}
	return v, nil
}

type acUsers struct {
	res    model.UserAutocomplete
	status map[string]string
}

func (w *Worker) fetchUsers(ctx context.Context, team, channelID, prefix string) (acUsers, error) {
	res, err := w.rc.AutocompleteUsers(ctx, team, channelID, prefix, acMaxUsers)
	if err != nil {
		return acUsers{}, err
	}
	res.Users, res.OutOfChannel = capUsers(res.Users), capUsers(res.OutOfChannel)
	var ids []string
	for _, u := range slices.Concat(res.Users, res.OutOfChannel) {
		ids = append(ids, u.ID)
	}
	known := w.st.Presences(ids)
	var missing []string
	for _, id := range ids {
		if _, ok := known[id]; !ok && len(missing) < acMaxStatuses {
			missing = append(missing, id)
		}
	}
	status := map[string]string{}
	if len(missing) > 0 {
		// The dots are a nicety: a failed read leaves them out.
		if list, err := w.rc.StatusesByIDs(ctx, missing); err == nil {
			for _, s := range list {
				status[s.UserID] = s.Status
			}
		} else if ctx.Err() != nil {
			return acUsers{}, ctx.Err()
		}
	}
	return acUsers{res: res, status: status}, nil
}

func capUsers(us []model.User) []model.User {
	us = slices.DeleteFunc(us, func(u model.User) bool { return u.DeleteAt > 0 })
	return us[:min(len(us), acMaxUsers)]
}

// acUsers orders like the webapp's at-mention provider: usernames starting
// with the prefix first, then by username.
func (w *Worker) acUsers(us []model.User, fetched map[string]string, prefix string) []ACUser {
	me := w.st.Me().ID
	ids := make([]string, 0, len(us))
	for _, u := range us {
		ids = append(ids, u.ID)
	}
	known := w.st.Presences(ids)
	out := make([]ACUser, 0, len(us))
	for _, u := range us {
		st := known[u.ID]
		if st == "" {
			st = fetched[u.ID]
		}
		if u.IsBot {
			st = ""
		}
		a := ACUser{ID: u.ID, Username: u.Username, FullName: u.FullName(), Nickname: u.Nickname, Status: st, Bot: u.IsBot, Me: u.ID == me}
		if u.LastPictureUpdate != 0 {
			a.Avatar = strconv.FormatInt(u.LastPictureUpdate, 10)
		}
		out = append(out, a)
	}
	sort.SliceStable(out, func(i, j int) bool {
		pi, pj := strings.HasPrefix(out[i].Username, prefix), strings.HasPrefix(out[j].Username, prefix)
		if pi != pj {
			return pi
		}
		return out[i].Username < out[j].Username
	})
	return out
}

// acChannels: the channels we are in first (the webapp's "My Channels"),
// then the others; archived ones only when joined; by display name.
func (w *Worker) acChannels(list []model.Channel) []ACChannel {
	ids := make([]string, 0, len(list))
	for _, c := range list {
		ids = append(ids, c.ID)
	}
	joined := w.st.Joined(ids)
	out := make([]ACChannel, 0, len(list))
	for _, c := range list {
		if c.DeleteAt > 0 && !joined[c.ID] {
			continue
		}
		if c.Type != model.ChannelOpen && c.Type != model.ChannelPrivate {
			continue
		}
		out = append(out, ACChannel{ID: c.ID, Name: c.Name, DisplayName: c.DisplayName, Type: c.Type, Joined: joined[c.ID]})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Joined != out[j].Joined {
			return out[i].Joined
		}
		return strings.ToLower(out[i].DisplayName) < strings.ToLower(out[j].DisplayName)
	})
	return out[:min(len(out), acMaxChannels)]
}

// acEmoji: custom names from the index matching anywhere (the webapp's
// substring match) and the server's prefix matches; prefix matches first.
func (w *Worker) acEmoji(prefix string, server []model.Emoji) []string {
	seen := map[string]bool{}
	var names []string
	for _, n := range w.st.CustomEmojiNames() {
		if strings.Contains(n, prefix) && !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	for _, e := range server {
		if e.DeleteAt == 0 && e.Name != "" && !seen[e.Name] {
			seen[e.Name] = true
			names = append(names, e.Name)
		}
	}
	sort.SliceStable(names, func(i, j int) bool {
		pi, pj := strings.HasPrefix(names[i], prefix), strings.HasPrefix(names[j], prefix)
		if pi != pj {
			return pi
		}
		return names[i] < names[j]
	})
	if names == nil {
		return []string{}
	}
	return names[:min(len(names), acMaxEmoji)]
}

func acCommands(list []model.Command, prefix string) []ACCommand {
	out := []ACCommand{}
	for _, c := range list {
		if !c.AutoComplete || c.DeleteAt > 0 || c.Trigger == "" || !strings.HasPrefix(strings.ToLower(c.Trigger), prefix) {
			continue
		}
		out = append(out, ACCommand{Trigger: c.Trigger, Hint: c.AutoCompleteHint, Description: c.AutoCompleteDesc})
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Trigger) < strings.ToLower(out[j].Trigger) })
	return out[:min(len(out), acMaxCommands)]
}

// ExecuteCommand runs a slash command typed in channelID's composer (a
// thread's with rootID) — only ever on an explicit send. The text is
// normalised as the webapp does: the trigger lowercased, the rest trimmed.
// An ephemeral answer arrives as an ephemeral_message event.
func (w *Worker) ExecuteCommand(ctx context.Context, channelID, rootID, command string) error {
	team, ok := w.st.AutocompleteScope(channelID)
	if !ok {
		return ErrNoChannel
	}
	trigger, text, _ := strings.Cut(strings.TrimSpace(command), " ")
	msg := strings.ToLower(trigger)
	if r := strings.TrimSpace(text); r != "" {
		msg += " " + r
	}
	_, err := w.rc.ExecuteCommand(ctx, model.CommandArgs{ChannelID: channelID, TeamID: team, RootID: rootID, Command: msg})
	var re *rest.Error
	switch {
	case errors.As(err, &re) && re.ID == "api.command.execute_command.not_found.app_error":
		return ErrCommandNotFound
	case sessionExpired(err):
		w.signalAuth()
	}
	return err
}
