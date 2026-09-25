package mmfake

import (
	"encoding/json"
	"net/http"
	"regexp"
	"slices"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

var emojiNameRe = regexp.MustCompile(`^[a-zA-Z0-9+_-]{1,64}$`)

func (s *Server) reactionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v4/reactions", s.handleAuthed(s.saveReaction))
	mux.HandleFunc("DELETE /api/v4/users/{uid}/posts/{pid}/reactions/{name}", s.handleAuthed(s.deleteReaction))
}

// reactLocked adds or removes userID's reaction like the server: the post's
// update_at moves and the channel gets the event. A no-op (adding what is
// there, removing what is not) changes nothing and sends nothing, and
// returns the existing reaction (its real create_at, not a fresh
// timestamp) so callers never fabricate one.
func (s *Server) reactLocked(userID, postID, name string, add bool) (model.Reaction, *apiErr) {
	p := s.chat.byID[postID]
	if p == nil || p.DeleteAt != 0 {
		return model.Reaction{}, &apiErr{404, "app.post.get.app_error"}
	}
	if !s.isMemberLocked(p.ChannelID, userID) {
		return model.Reaction{}, &apiErr{403, "api.context.permissions.app_error"}
	}
	if s.chat.channels[p.ChannelID].DeleteAt != 0 {
		id := "api.reaction.save.archived_channel.app_error"
		if !add {
			id = "api.reaction.delete.archived_channel.app_error"
		}
		return model.Reaction{}, &apiErr{403, id}
	}
	if !emojiNameRe.MatchString(name) {
		return model.Reaction{}, &apiErr{400, "api.reaction.save_reaction.invalid.app_error"}
	}
	var list []model.Reaction
	var files []model.FileInfo
	if p.Metadata != nil {
		list, files = slices.Clone(p.Metadata.Reactions), p.Metadata.Files
	}
	i := slices.IndexFunc(list, func(r model.Reaction) bool { return r.UserID == userID && r.EmojiName == name })
	if add == (i >= 0) {
		if i >= 0 {
			return list[i], nil
		}
		return model.Reaction{}, nil
	}
	now := s.nowLocked()
	r := model.Reaction{UserID: userID, PostID: postID, EmojiName: name, CreateAt: now}
	if add {
		list = append(list, r)
	} else {
		r = list[i]
		list = slices.Delete(list, i, i+1)
	}
	// A fresh Metadata: copies of the post handed out earlier keep theirs.
	p.Metadata = &model.PostMetadata{Files: files, Reactions: list}
	p.UpdateAt = now
	b, _ := json.Marshal(r)
	ev := "reaction_added"
	if !add {
		ev = "reaction_removed"
	}
	s.publishLocked(ev, map[string]any{"reaction": string(b)}, wsBroadcast{ChannelID: p.ChannelID}, s.memberIDsLocked(p.ChannelID), nil)
	return r, nil
}

func (s *Server) saveReaction(w http.ResponseWriter, r *http.Request, u User) {
	var in model.Reaction
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		appError(w, 400, "api.reaction.save_reaction.invalid.app_error", err.Error())
		return
	}
	if in.UserID != u.ID {
		appError(w, 403, "api.reaction.save_reaction.user_id.app_error", "can only react as yourself")
		return
	}
	s.mu.Lock()
	out, e := s.reactLocked(u.ID, in.PostID, in.EmojiName, true)
	s.mu.Unlock()
	if e != nil {
		writeAPIErr(w, e)
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) deleteReaction(w http.ResponseWriter, r *http.Request, u User) {
	if r.PathValue("uid") != u.ID {
		appError(w, 403, "api.context.permissions.app_error", "not your reaction")
		return
	}
	s.mu.Lock()
	_, e := s.reactLocked(u.ID, r.PathValue("pid"), r.PathValue("name"), false)
	s.mu.Unlock()
	if e != nil {
		writeAPIErr(w, e)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "OK"})
}

// ---- test controls ----

func (s *Server) ReactAs(username, postID, emoji string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, e := s.reactLocked(s.userIDByName(username), postID, emoji, true); e != nil {
		panic("mmfake: ReactAs: " + e.id)
	}
}

func (s *Server) UnreactAs(username, postID, emoji string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, e := s.reactLocked(s.userIDByName(username), postID, emoji, false); e != nil {
		panic("mmfake: UnreactAs: " + e.id)
	}
}

func (s *Server) Reactions(postID string) []model.Reaction {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.chat.byID[postID]; p != nil && p.Metadata != nil {
		return slices.Clone(p.Metadata.Reactions)
	}
	return nil
}

// FindPost returns the id of the latest visible post of a channel with
// exactly this message ("" if none).
func (s *Server) FindPost(channelID, message string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	posts := s.chat.posts[channelID]
	for i := len(posts) - 1; i >= 0; i-- {
		if p := posts[i]; visible(p, false) && p.Message == message {
			return p.ID
		}
	}
	return ""
}

// Preference reads a saved preference of username ("" if unset).
func (s *Server) Preference(username, category, name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.chat.prefs[s.userIDByName(username)] {
		if p.Category == category && p.Name == name {
			return p.Value
		}
	}
	return ""
}
