package notify

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

func base() Input {
	return Input{
		MeID: "me", MeUsername: "alice", MeFirstName: "Alice",
		UserNotify:   map[string]string{"desktop": "mention", "channel": "true"},
		MemberNotify: map[string]string{"desktop": "default", "mark_unread": "all"},
		Status:       "online", NowMs: 1_000_000,
		Post:        model.Post{ID: "p", ChannelID: "c", UserID: "bob", Message: "hello"},
		ChannelType: "O",
	}
}

func TestDecide(t *testing.T) {
	cases := []struct {
		name   string
		mut    func(*Input)
		want   bool
		reason string
	}{
		{"plain message, level mention", func(*Input) {}, false, "not_mentioned"},
		{"mentioned", func(in *Input) { in.Mentions = []string{"me"} }, true, ""},
		{"DM always", func(in *Input) { in.ChannelType = "D" }, true, ""},
		{"own post", func(in *Input) { in.Post.UserID = "me"; in.Mentions = []string{"me"} }, false, "own_post"},
		{"own webhook post", func(in *Input) { in.Post.UserID = "me"; in.Post.Props.FromWebhook = true; in.ChannelType = "D" }, true, ""},
		{"system post", func(in *Input) { in.Post.Type = "system_join_channel"; in.ChannelType = "D" }, false, "system_message"},
		{"force", func(in *Input) { in.Post.Props.ForceNotification = true; in.MemberNotify["mark_unread"] = "mention" }, true, ""},
		{"muted", func(in *Input) { in.ChannelType = "D"; in.MemberNotify["mark_unread"] = "mention" }, false, "channel_muted"},
		{"dnd", func(in *Input) { in.ChannelType = "D"; in.Status = "dnd" }, false, "user_status"},
		{"dnd expired", func(in *Input) { in.ChannelType = "D"; in.Status = "dnd"; in.DNDEndSec = 999 }, true, ""},
		{"ooo", func(in *Input) { in.ChannelType = "D"; in.Status = "ooo" }, false, "user_status"},
		{"user level all", func(in *Input) { in.UserNotify["desktop"] = "all" }, true, ""},
		{"channel override none", func(in *Input) { in.MemberNotify["desktop"] = "none"; in.Mentions = []string{"me"} }, false, "notify_level_none"},
		{"channel override all", func(in *Input) { in.MemberNotify["desktop"] = "all" }, true, ""},
		{"follower counts as mention", func(in *Input) { in.Followers = []string{"me"} }, true, ""},
		{"GM default+mention becomes all", func(in *Input) { in.ChannelType = "G" }, true, ""},
		{"GM explicit mention level, not mentioned", func(in *Input) { in.ChannelType = "G"; in.MemberNotify["desktop"] = "mention" }, false, "not_explicitly_mentioned"},
		{"GM explicit mention level, @alice", func(in *Input) {
			in.ChannelType = "G"
			in.MemberNotify["desktop"] = "mention"
			in.Post.Message = "hey @Alice, look"
		}, true, ""},
		{"GM @here ignored by member", func(in *Input) {
			in.ChannelType = "G"
			in.MemberNotify["desktop"] = "mention"
			in.MemberNotify["ignore_channel_mentions"] = "on"
			in.Post.Message = "@here standup"
		}, false, "not_explicitly_mentioned"},
		{"GM @here ignored by user setting", func(in *Input) {
			in.ChannelType = "G"
			in.MemberNotify["desktop"] = "mention"
			in.UserNotify["channel"] = "false"
			in.Post.Message = "@channel standup"
		}, false, "not_explicitly_mentioned"},
		{"GM first name when enabled", func(in *Input) {
			in.ChannelType = "G"
			in.MemberNotify["desktop"] = "mention"
			in.UserNotify["first_name"] = "true"
			in.Post.Message = "Alice?"
		}, true, ""},
		{"GM mention key in attachment", func(in *Input) {
			in.ChannelType = "G"
			in.MemberNotify["desktop"] = "mention"
			in.UserNotify["mention_keys"] = "deploy,oncall"
			in.Post.Props.Attachments = []model.Attachment{{Text: "ping ONCALL now"}}
		}, true, ""},
		{"GM username inside a word is not a mention", func(in *Input) {
			in.ChannelType = "G"
			in.MemberNotify["desktop"] = "mention"
			in.Post.Message = "email@alice.example"
		}, false, "not_explicitly_mentioned"},
		{"CRT reply, level all, not following", func(in *Input) {
			in.CRT = true
			in.Post.RootID = "r"
			in.UserNotify["desktop"] = "all"
		}, false, "not_following_thread"},
		{"CRT reply, following", func(in *Input) {
			in.CRT = true
			in.Post.RootID = "r"
			in.UserNotify["desktop"] = "all"
			in.Followers = []string{"me"}
		}, true, ""},
		{"focused on this channel", func(in *Input) { in.ChannelType = "D"; in.Focused = true; in.ActiveChannelID = "c" }, false, "channel_is_open"},
		{"CRT reply in the thread open in the panel", func(in *Input) {
			in.ChannelType, in.CRT, in.Post.RootID = "D", true, "r"
			in.Focused, in.ActiveChannelID, in.ActiveThreadID = true, "c", "r"
		}, false, "thread_is_open"},
		{"CRT reply in another thread than the open one", func(in *Input) {
			in.ChannelType, in.CRT, in.Post.RootID = "D", true, "r"
			in.Focused, in.ActiveChannelID, in.ActiveThreadID = true, "c", "other"
		}, true, ""},
		{"CRT reply, its channel open but no thread", func(in *Input) {
			in.ChannelType, in.CRT, in.Post.RootID = "D", true, "r"
			in.Focused, in.ActiveChannelID = true, "c"
		}, true, ""},
		{"CRT reply in the open thread, window not focused", func(in *Input) {
			in.ChannelType, in.CRT, in.Post.RootID = "D", true, "r"
			in.ActiveChannelID, in.ActiveThreadID = "c", "r"
		}, true, ""},
		{"reply without CRT, its channel open", func(in *Input) {
			in.ChannelType, in.Post.RootID = "D", "r"
			in.Focused, in.ActiveChannelID = true, "c"
		}, false, "channel_is_open"},
		{"not focused on this channel", func(in *Input) { in.ChannelType = "D"; in.ActiveChannelID = "c" }, true, ""},
		{"focused elsewhere", func(in *Input) { in.ChannelType = "D"; in.Focused = true; in.ActiveChannelID = "other" }, true, ""},
		{"no user props → level all", func(in *Input) { in.UserNotify = nil }, true, ""},
	}
	for _, c := range cases {
		in := base()
		in.UserNotify = map[string]string{"desktop": "mention", "channel": "true"}
		in.MemberNotify = map[string]string{"desktop": "default", "mark_unread": "all"}
		c.mut(&in)
		got, reason := Decide(in)
		assert.Equal(t, c.want, got, c.name)
		assert.Equal(t, c.reason, reason, c.name)
	}
}
