// Package notify decides whether a new post deserves a desktop
// notification, following the server's notification settings the way the
// official webapp does (docs/research/2026-09-24-mattermost-api-facts.md §6).
// Mentions come from the server (the posted event's mentions/followers), not
// from parsing the text — except for group messages at "mention" level,
// which the webapp scans itself.
package notify

import (
	"slices"
	"strings"
	"unicode"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

type Input struct {
	MeID, MeUsername, MeFirstName string
	UserNotify                    map[string]string // user.notify_props
	MemberNotify                  map[string]string // channel member notify_props
	Status                        string
	DNDEndSec                     int64 // seconds; 0 = no end
	NowMs                         int64
	Post                          model.Post
	ChannelType                   string
	Mentions, Followers           []string
	CRT                           bool
	Focused                       bool
	ActiveChannelID               string
	ActiveThreadID                string // the thread open in the panel ("" none)
}

func Decide(in Input) (bool, string) {
	p := in.Post
	if p.UserID == in.MeID && !bool(p.Props.FromWebhook) {
		return false, "own_post"
	}
	if p.IsSystem() {
		return false, "system_message"
	}
	if bool(p.Props.ForceNotification) {
		return true, ""
	}
	if in.MemberNotify["mark_unread"] == "mention" {
		return false, "channel_muted"
	}
	dnd := in.Status == "dnd" && (in.DNDEndSec == 0 || in.NowMs/1000 < in.DNDEndSec)
	if dnd || in.Status == "ooo" {
		return false, "user_status"
	}
	mentioned := slices.Contains(in.Mentions, in.MeID) || slices.Contains(in.Followers, in.MeID)
	chProp := in.MemberNotify["desktop"]
	if chProp == "" {
		chProp = "default"
	}
	level := chProp
	if chProp == "default" {
		level = in.UserNotify["desktop"]
		if level == "" {
			level = "all"
		}
	}
	if in.ChannelType == model.ChannelGroup && chProp == "default" && in.UserNotify["desktop"] == "mention" {
		level = "all"
	}
	crtReply := in.CRT && p.RootID != ""
	switch {
	case level == "none":
		return false, "notify_level_none"
	case in.ChannelType == model.ChannelGroup && level == "mention":
		if !explicitlyMentioned(in) {
			return false, "not_explicitly_mentioned"
		}
	case level == "mention" && !mentioned && in.ChannelType != model.ChannelDirect:
		return false, "not_mentioned"
	case crtReply && level == "all" && !slices.Contains(in.Followers, in.MeID):
		return false, "not_following_thread"
	}
	// On screen: a CRT reply when its thread is open in the panel, any
	// other post when its channel is (without CRT replies show in the feed).
	if in.Focused && crtReply && in.ActiveThreadID == p.RootID {
		return false, "thread_is_open"
	}
	if in.Focused && !crtReply && in.ActiveChannelID == p.ChannelID {
		return false, "channel_is_open"
	}
	return true, ""
}

func mentionKeys(in Input) []string {
	keys := []string{"@" + in.MeUsername}
	if in.UserNotify["first_name"] == "true" && in.MeFirstName != "" {
		keys = append(keys, in.MeFirstName)
	}
	for _, k := range strings.Split(in.UserNotify["mention_keys"], ",") {
		if k = strings.TrimSpace(k); k != "" {
			keys = append(keys, k)
		}
	}
	ignore := in.MemberNotify["ignore_channel_mentions"]
	channelOff := ignore == "on" || ((ignore == "" || ignore == "default") && in.UserNotify["channel"] == "false")
	if !channelOff {
		keys = append(keys, "@channel", "@all", "@here")
	}
	return keys
}

func explicitlyMentioned(in Input) bool {
	texts := []string{in.Post.Message}
	for _, a := range in.Post.Props.Attachments {
		texts = append(texts, a.Pretext, a.Title, a.Text, a.Fallback)
	}
	text := strings.ToLower(strings.Join(texts, "\n"))
	for _, k := range mentionKeys(in) {
		if containsWord(text, strings.ToLower(k)) {
			return true
		}
	}
	return false
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '.' || r == '-' || r == '@'
}

// containsWord finds key in text not glued to other word characters
// ("@alice," matches; "email@alice.example" does not). A trailing '.' is
// sentence punctuation, not part of the word.
func containsWord(text, key string) bool {
	for i := 0; ; {
		j := strings.Index(text[i:], key)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(key)
		before := start == 0 || !isWordRune(lastRune(text[:start]))
		after := end == len(text) || !isWordRune(firstRune(text[end:])) ||
			(text[end] == '.' && (end+1 == len(text) || !isWordRune(firstRune(text[end+1:]))))
		if before && after {
			return true
		}
		i = start + 1
	}
}

func firstRune(s string) rune {
	for _, r := range s {
		return r
	}
	return 0
}

func lastRune(s string) rune {
	r := []rune(s)
	return r[len(r)-1]
}
