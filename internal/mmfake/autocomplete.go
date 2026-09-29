package mmfake

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

// Autocomplete and slash commands (composer autocomplete brief
// 2026-09-29): GET users/channels/emoji/commands autocomplete and POST
// commands/execute, with three built-in commands mirroring the real
// server's — /echo (posts the text as the user), /shrug (posts the text
// with ¯\_(ツ)_/¯) and /away (sets the status, answers with an ephemeral
// post) — and a 404 not_found for any other trigger.

// ExecutedCommand is one POST /commands/execute the fake accepted.
type ExecutedCommand struct {
	UserID    string
	ChannelID string
	TeamID    string
	RootID    string
	Command   string
}

func (s *Server) autocompleteRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v4/users/autocomplete", s.handleAuthed(s.usersAutocomplete))
	mux.HandleFunc("GET /api/v4/teams/{tid}/channels/autocomplete", s.handleAuthed(s.channelsAutocomplete))
	mux.HandleFunc("GET /api/v4/emoji/autocomplete", s.handleAuthed(s.emojiAutocomplete))
	mux.HandleFunc("GET /api/v4/teams/{tid}/commands/autocomplete", s.handleAuthed(s.commandsAutocomplete))
	mux.HandleFunc("POST /api/v4/commands/execute", s.handleAuthed(s.executeCommand))
}

// matchesUser: the server matches a prefix of the username, first name,
// last name or nickname, case-insensitively.
func matchesUser(u User, prefix string) bool {
	for _, f := range []string{u.Username, u.FirstName, u.LastName} {
		if strings.HasPrefix(strings.ToLower(f), prefix) {
			return true
		}
	}
	return false
}

func (s *Server) usersAutocomplete(w http.ResponseWriter, r *http.Request, u User) {
	q := r.URL.Query()
	prefix := strings.ToLower(q.Get("name"))
	channelID := q.Get("in_channel")
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if channelID != "" && !s.isMemberLocked(channelID, u.ID) {
		appError(w, 403, "api.context.permissions.app_error", "no permission")
		return
	}
	res := model.UserAutocomplete{Users: []model.User{}}
	users := append([]User(nil), s.opts.Users...)
	sort.Slice(users, func(i, j int) bool { return users[i].Username < users[j].Username })
	for _, x := range users {
		if !matchesUser(x, prefix) {
			continue
		}
		in := channelID == "" || s.isMemberLocked(channelID, x.ID)
		switch {
		case in && len(res.Users) < limit:
			res.Users = append(res.Users, s.userWithPictureLocked(x))
		case !in && len(res.OutOfChannel) < limit:
			res.OutOfChannel = append(res.OutOfChannel, s.userWithPictureLocked(x))
		}
	}
	writeJSON(w, 200, res)
}

func (s *Server) inTeamLocked(teamID string) bool {
	for _, t := range s.chat.teams {
		if t.ID == teamID {
			return true
		}
	}
	return false
}

func (s *Server) channelsAutocomplete(w http.ResponseWriter, r *http.Request, u User) {
	prefix := strings.ToLower(r.URL.Query().Get("name"))
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.inTeamLocked(r.PathValue("tid")) {
		appError(w, 403, "api.context.permissions.app_error", "no permission")
		return
	}
	out := []model.Channel{}
	for _, id := range s.sortedChannelIDsLocked() {
		c := s.chat.channels[id]
		if c.TeamID != r.PathValue("tid") || c.DeleteAt != 0 {
			continue
		}
		if c.Type == model.ChannelPrivate && !s.isMemberLocked(c.ID, u.ID) {
			continue
		}
		if strings.HasPrefix(c.Name, prefix) || strings.HasPrefix(strings.ToLower(c.DisplayName), prefix) {
			out = append(out, *c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, 200, out)
}

func (s *Server) emojiAutocomplete(w http.ResponseWriter, r *http.Request, _ User) {
	if s.opts.DisableCustomEmoji {
		appError(w, 501, "api.emoji.disabled.app_error", "custom emoji disabled")
		return
	}
	prefix := strings.ToLower(r.URL.Query().Get("name"))
	s.mu.Lock()
	out := []model.Emoji{}
	for _, e := range s.chat.emoji {
		if strings.HasPrefix(e.e.Name, prefix) {
			out = append(out, e.e)
		}
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, 200, out)
}

// fakeCommands: the built-ins the fake runs, as the server lists them.
var fakeCommands = []model.Command{
	{Trigger: "away", AutoComplete: true, AutoCompleteDesc: "Set your status away", DisplayName: "away"},
	{Trigger: "leave", AutoComplete: true, AutoCompleteDesc: "Leave the current channel", DisplayName: "leave"},
	{Trigger: "logout", AutoComplete: true, AutoCompleteDesc: "Log out of Mattermost", DisplayName: "logout"},
	{Trigger: "echo", AutoComplete: true, AutoCompleteHint: `"message" [delay in seconds]`, AutoCompleteDesc: "Echo back text from your account", DisplayName: "echo"},
	{Trigger: "shrug", AutoComplete: true, AutoCompleteHint: "[message]", AutoCompleteDesc: `Adds ¯\_(ツ)_/¯ to your message`, DisplayName: "shrug"},
}

func (s *Server) commandsAutocomplete(w http.ResponseWriter, r *http.Request, _ User) {
	s.mu.Lock()
	ok := s.inTeamLocked(r.PathValue("tid"))
	s.mu.Unlock()
	if !ok {
		appError(w, 403, "api.context.permissions.app_error", "no permission")
		return
	}
	writeJSON(w, 200, fakeCommands)
}

func (s *Server) executeCommand(w http.ResponseWriter, r *http.Request, u User) {
	var args model.CommandArgs
	if err := json.NewDecoder(r.Body).Decode(&args); err != nil || len(args.Command) <= 1 || args.Command[0] != '/' {
		appError(w, 400, "api.command.execute_command.start.app_error", "bad command")
		return
	}
	trigger, text, _ := strings.Cut(args.Command[1:], " ")
	trigger, text = strings.ToLower(trigger), strings.TrimSpace(text)
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.isMemberLocked(args.ChannelID, u.ID) {
		appError(w, 403, "api.context.permissions.app_error", "no permission")
		return
	}
	var resp model.CommandResponse
	post := func(msg string) *apiErr {
		_, e := s.createPostLocked(u.ID, model.Post{ChannelID: args.ChannelID, RootID: args.RootID, Message: msg})
		return e
	}
	var e *apiErr
	switch trigger {
	case "echo":
		resp.ResponseType = "in_channel"
		e = post(strings.Trim(text, `"`))
	case "shrug":
		msg := `¯\\\_(ツ)\_/¯`
		if text != "" {
			msg = text + " " + msg
		}
		resp.ResponseType = "in_channel"
		e = post(msg)
	case "away":
		s.chat.status[u.ID] = "away"
		s.publishLocked("status_change", map[string]any{"status": "away", "user_id": u.ID}, wsBroadcast{UserID: u.ID}, []string{u.ID}, nil, nil)
		resp = model.CommandResponse{ResponseType: "ephemeral", Text: "You are now away"}
		s.ephemeralLocked(u.ID, args.ChannelID, args.RootID, resp.Text)
	case "leave":
		// app/slashcommands/command_leave.go: root_id is ignored — the
		// whole channel is left (the webapp refuses it in a thread).
		delete(s.chat.members[args.ChannelID], u.ID)
		s.publishLocked("user_removed", map[string]any{"channel_id": args.ChannelID, "remover_id": u.ID},
			wsBroadcast{UserID: u.ID}, []string{u.ID}, nil, nil)
	case "logout":
		// command_logout.go: "Actual logout is handled client side".
		resp.GotoLocation = "/login"
	default:
		appError(w, 404, "api.command.execute_command.not_found.app_error", "Command with a trigger of '/"+trigger+"' not found.")
		return
	}
	if e != nil {
		writeAPIErr(w, e)
		return
	}
	s.chat.commands = append(s.chat.commands, ExecutedCommand{UserID: u.ID, ChannelID: args.ChannelID, TeamID: args.TeamID,
		RootID: args.RootID, Command: args.Command})
	writeJSON(w, 200, resp)
}

// ephemeralLocked sends userID a post only they see — never stored — the
// way app/post.go SendEphemeralPost does: the ephemeral_message event.
func (s *Server) ephemeralLocked(userID, channelID, rootID, text string) {
	p := model.Post{ID: newID(), ChannelID: channelID, RootID: rootID, UserID: userID, Type: model.PostTypeEphemeral,
		Message: text, CreateAt: s.nowLocked()}
	b, _ := json.Marshal(p)
	s.publishLocked("ephemeral_message", map[string]any{"post": string(b)}, wsBroadcast{UserID: userID, ChannelID: channelID}, []string{userID}, nil, nil)
}

// ExecutedCommands lists the commands the fake ran, oldest first.
func (s *Server) ExecutedCommands() []ExecutedCommand {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ExecutedCommand(nil), s.chat.commands...)
}

func (s *Server) isMember(channelID, userID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.isMemberLocked(channelID, userID)
}
