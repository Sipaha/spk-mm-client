//go:build wails

package transport

import (
	"context"

	"github.com/spk/spk-mm-client/internal/api"
)

// API is the Wails service. Bindings are addressed by Go FQN:
//
//	github.com/spk/spk-mm-client/internal/api/transport.API.<Method>
//
// (mirrored in frontend/src/api/client.ts).
type API struct{ a api.API }

func NewAPI(a api.API) *API { return &API{a: a} }

func (w *API) ListServers() ([]api.ServerDTO, error) { return w.a.ListServers(context.Background()) }
func (w *API) AddServer(url string) (api.ServerDTO, error) {
	return w.a.AddServer(context.Background(), url)
}
func (w *API) RemoveServer(id int64) error     { return w.a.RemoveServer(context.Background(), id) }
func (w *API) StartGitLabLogin(id int64) error { return w.a.StartGitLabLogin(context.Background(), id) }
func (w *API) LoginWithPassword(id int64, login, password string) (api.ServerDTO, error) {
	return w.a.LoginWithPassword(context.Background(), id, login, password)
}
func (w *API) Logout(id int64) error         { return w.a.Logout(context.Background(), id) }
func (w *API) AppInfo() (api.AppInfo, error) { return w.a.AppInfo(context.Background()) }
func (w *API) SelectServer(id int64) error   { return w.a.SelectServer(context.Background(), id) }
func (w *API) SetFocused(focused bool) error { return w.a.SetFocused(context.Background(), focused) }
func (w *API) NetworkChanged() error         { return w.a.NetworkChanged(context.Background()) }
func (w *API) OpenURL(url string) error      { return w.a.OpenURL(context.Background(), url) }
func (w *API) Sidebar(id int64, teamID string) (api.SidebarDTO, error) {
	return w.a.Sidebar(context.Background(), id, teamID)
}
func (w *API) OpenChannel(id int64, channelID string) (api.ChannelDTO, error) {
	return w.a.OpenChannel(context.Background(), id, channelID)
}
func (w *API) GetChannel(id int64, channelID string) (api.ChannelDTO, error) {
	return w.a.GetChannel(context.Background(), id, channelID)
}
func (w *API) LoadOlder(id int64, channelID string) error {
	return w.a.LoadOlder(context.Background(), id, channelID)
}
func (w *API) OpenThread(id int64, channelID, rootID string) (api.ThreadDTO, error) {
	return w.a.OpenThread(context.Background(), id, channelID, rootID)
}
func (w *API) GetThread(id int64, rootID string) (api.ThreadDTO, error) {
	return w.a.GetThread(context.Background(), id, rootID)
}
func (w *API) CloseThread(id int64) error { return w.a.CloseThread(context.Background(), id) }
func (w *API) LoadOlderReplies(id int64, rootID string) error {
	return w.a.LoadOlderReplies(context.Background(), id, rootID)
}
func (w *API) SendPost(id int64, channelID, message string, attachmentIDs []string) error {
	return w.a.SendPost(context.Background(), id, channelID, message, attachmentIDs)
}
func (w *API) SendReply(id int64, channelID, rootID, message string, attachmentIDs []string) error {
	return w.a.SendReply(context.Background(), id, channelID, rootID, message, attachmentIDs)
}
func (w *API) RetryPost(id int64, channelID, pendingID string) error {
	return w.a.RetryPost(context.Background(), id, channelID, pendingID)
}
func (w *API) DiscardPost(id int64, channelID, pendingID string) error {
	return w.a.DiscardPost(context.Background(), id, channelID, pendingID)
}
func (w *API) EditPost(id int64, postID, message string) error {
	return w.a.EditPost(context.Background(), id, postID, message)
}
func (w *API) DeletePost(id int64, postID string) error {
	return w.a.DeletePost(context.Background(), id, postID)
}
func (w *API) MarkUnread(id int64, postID string) error {
	return w.a.MarkUnread(context.Background(), id, postID)
}
func (w *API) SetPostSaved(id int64, postID string, saved bool) error {
	return w.a.SetPostSaved(context.Background(), id, postID, saved)
}
func (w *API) SaveDraft(id int64, channelID, text string) error {
	return w.a.SaveDraft(context.Background(), id, channelID, text)
}
func (w *API) SaveThreadDraft(id int64, rootID, text string) error {
	return w.a.SaveThreadDraft(context.Background(), id, rootID, text)
}

func (w *API) DownloadFile(id int64, fileID string) (api.SavedFile, error) {
	return w.a.DownloadFile(context.Background(), id, fileID)
}

func (w *API) OpenFile(id int64, fileID string) (api.SavedFile, error) {
	return w.a.OpenFile(context.Background(), id, fileID)
}

func (w *API) AddReaction(id int64, postID, emoji string) error {
	return w.a.AddReaction(context.Background(), id, postID, emoji)
}

func (w *API) RemoveReaction(id int64, postID, emoji string) error {
	return w.a.RemoveReaction(context.Background(), id, postID, emoji)
}

func (w *API) EmojiInfo(id int64) (api.EmojiDTO, error) {
	return w.a.EmojiInfo(context.Background(), id)
}

// MediaStreamBase returns a URL with the loopback server's token: keep Wails'
// log level below Debug in shipped builds (it logs binding results there).
func (w *API) MediaStreamBase() (string, error) {
	return w.a.MediaStreamBase(context.Background())
}

func (w *API) Downloads() ([]api.DownloadView, error) { return w.a.Downloads(context.Background()) }

func (w *API) OpenDownload(id int64) (bool, error) {
	return w.a.OpenDownload(context.Background(), id)
}

func (w *API) RevealDownload(id int64) error { return w.a.RevealDownload(context.Background(), id) }

func (w *API) RemoveDownload(id int64) error { return w.a.RemoveDownload(context.Background(), id) }

func (w *API) ClearDownloads() error { return w.a.ClearDownloads(context.Background()) }

func (w *API) Attachments(id int64, channelID, rootID string) ([]api.AttachmentView, error) {
	return w.a.Attachments(context.Background(), id, channelID, rootID)
}

func (w *API) RemoveAttachment(id int64, attachmentID string) error {
	return w.a.RemoveAttachment(context.Background(), id, attachmentID)
}

func (w *API) RetryAttachment(id int64, attachmentID string) error {
	return w.a.RetryAttachment(context.Background(), id, attachmentID)
}

func (w *API) AttachFromClipboard(id int64, channelID, rootID string) (int, error) {
	return w.a.AttachFromClipboard(context.Background(), id, channelID, rootID)
}

func (w *API) PickAttachments(id int64, channelID, rootID string) (int, error) {
	return w.a.PickAttachments(context.Background(), id, channelID, rootID)
}
