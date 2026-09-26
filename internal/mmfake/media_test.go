package mmfake

import (
	"bytes"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

func (a authed) raw(method, path string, hdr http.Header) (*http.Response, []byte) {
	a.t.Helper()
	req, _ := http.NewRequest(method, a.s.URL()+path, nil)
	for k, vs := range hdr {
		req.Header[k] = vs
	}
	req.Header.Set("Authorization", "Bearer "+a.tok)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(a.t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func TestAvatarETagAndPictureChange(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	resp, body := a.raw("GET", "/api/v4/users/u-bob/image", nil)
	require.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "image/png", resp.Header.Get("Content-Type"))
	assert.True(t, bytes.HasPrefix(body, []byte("\x89PNG")))
	etag := resp.Header.Get("ETag")
	resp, _ = a.raw("GET", "/api/v4/users/u-bob/image", http.Header{"If-None-Match": {etag}})
	assert.Equal(t, http.StatusNotModified, resp.StatusCode)

	at := s.SetPicture("bob")
	resp, _ = a.raw("GET", "/api/v4/users/u-bob/image", http.Header{"If-None-Match": {etag}})
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, strconv.FormatInt(at, 10), resp.Header.Get("ETag"))

	var users []model.User
	require.Equal(t, 200, a.call("POST", "/api/v4/users/ids", []string{"u-bob", "u-carol"}, &users))
	assert.Equal(t, at, users[0].LastPictureUpdate)
	assert.Less(t, users[1].LastPictureUpdate, int64(0), "carol has a generated picture")
}

func TestStatusesByIDs(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	var out []model.Status
	require.Equal(t, 200, a.call("POST", "/api/v4/users/status/ids", []string{"u-bob", "u-carol", "u-nobody"}, &out))
	assert.Equal(t, []model.Status{{UserID: "u-bob", Status: "online"}, {UserID: "u-carol", Status: "away"}, {UserID: "u-nobody", Status: "offline"}}, out)
	s.SetStatus("bob", "dnd")
	require.Equal(t, 200, a.call("POST", "/api/v4/users/status/ids", []string{"u-bob"}, &out))
	assert.Equal(t, "dnd", out[0].Status)
	assert.Equal(t, 400, a.call("POST", "/api/v4/users/status/ids", []string{}, nil))
}

func TestSeededFilesServeDataThumbnailPreviewAndRange(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	var info model.FileInfo
	require.Equal(t, 200, a.call("GET", "/api/v4/files/f-build/info", nil, &info))
	assert.Equal(t, model.FileInfo{ID: "f-build", Name: "build.png", Extension: "png", Size: info.Size, MimeType: "image/png", Width: 960, Height: 540, HasPreviewImage: true}, info)

	resp, body := a.raw("GET", "/api/v4/files/f-build/thumbnail", nil)
	require.Equal(t, 200, resp.StatusCode)
	cfg, format, err := image.DecodeConfig(bytes.NewReader(body))
	require.NoError(t, err)
	assert.Equal(t, "jpeg", format)
	assert.LessOrEqual(t, cfg.Width, 120)
	assert.LessOrEqual(t, cfg.Height, 100)

	resp, body = a.raw("GET", "/api/v4/files/f-build/preview", nil)
	require.Equal(t, 200, resp.StatusCode)
	cfg, _, err = image.DecodeConfig(bytes.NewReader(body))
	require.NoError(t, err)
	assert.Equal(t, 960, cfg.Width)

	resp, body = a.raw("GET", "/api/v4/files/f-log", http.Header{"Range": {"bytes=0-9"}})
	assert.Equal(t, http.StatusPartialContent, resp.StatusCode)
	assert.Len(t, body, 10)
	assert.Contains(t, resp.Header.Get("Content-Range"), "bytes 0-9/")

	resp, _ = a.raw("GET", "/api/v4/files/f-spec/thumbnail", nil)
	assert.Equal(t, 400, resp.StatusCode, "no thumbnail for a pdf")
	resp, _ = a.raw("GET", "/api/v4/files/f-nope/info", nil)
	assert.Equal(t, 404, resp.StatusCode)

	var posts model.PostList
	require.Equal(t, 200, a.call("GET", "/api/v4/channels/c-offtopic/posts?page=0&per_page=60", nil, &posts))
	var names []string
	for _, p := range posts.Ascending() {
		if p.Metadata != nil {
			for _, f := range p.Metadata.Files {
				names = append(names, f.Name)
			}
		}
	}
	assert.Equal(t, []string{"build.png", "flow.png", "arch.png", "server.log", "spec.pdf", "big.log", "README.md",
		"clip.webm", "clip.mp4", "tone.ogg"}, names)
	assert.Equal(t, 1, s.Hits("GET", "/api/v4/files/f-build/info"))

	for id, want := range map[string]string{"f-clip-webm": "video/webm", "f-clip-mp4": "video/mp4", "f-tone-ogg": "audio/ogg"} {
		resp, body := a.raw("GET", "/api/v4/files/"+id, http.Header{"Range": {"bytes=0-3"}})
		assert.Equal(t, 206, resp.StatusCode, id)
		assert.Equal(t, want, resp.Header.Get("Content-Type"), id)
		assert.Len(t, body, 4, id)
	}
	for _, name := range []string{"clip.webm", "clip.mp4", "tone.ogg"} {
		assert.Less(t, len(seedMedia(name)), 200<<10, "seeded clips stay small: %s", name)
	}
}

func TestFilePermissionIsTheChannel(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	s.mu.Lock()
	s.newFileLocked("f-secret", "c-secret", "s.txt", "text/plain", []byte("psst"))
	s.mu.Unlock()
	bob := loginAs(t, s, "bob")
	resp, _ := bob.raw("GET", "/api/v4/files/f-secret", nil)
	assert.Equal(t, 403, resp.StatusCode)
}

func TestPostFileCarriesMetadata(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	p := s.PostFile("c-offtopic", "bob", "look", "notes.txt", "text/plain", []byte("hello"))
	require.NotNil(t, p.Metadata)
	require.Len(t, p.Metadata.Files, 1)
	assert.Equal(t, "notes.txt", p.Metadata.Files[0].Name)
	assert.Equal(t, p.FileIDs, []string{p.Metadata.Files[0].ID})
}

func TestCustomEmoji(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	var cfg map[string]string
	require.Equal(t, 200, a.call("GET", "/api/v4/config/client?format=old", nil, &cfg))
	assert.Equal(t, "true", cfg["EnableCustomEmoji"])
	var list []model.Emoji
	require.Equal(t, 200, a.call("GET", "/api/v4/emoji?page=0&per_page=200&sort=name", nil, &list))
	assert.Equal(t, []model.Emoji{{ID: "e-parrot", Name: "partyparrot", CreatorID: "u-bob"}}, list)
	var e model.Emoji
	require.Equal(t, 200, a.call("GET", "/api/v4/emoji/name/partyparrot", nil, &e))
	assert.Equal(t, "e-parrot", e.ID)
	assert.Equal(t, 404, a.call("GET", "/api/v4/emoji/name/nope", nil, nil))
	resp, body := a.raw("GET", "/api/v4/emoji/e-parrot/image", nil)
	assert.Equal(t, 200, resp.StatusCode)
	assert.True(t, bytes.HasPrefix(body, []byte("\x89PNG")))

	added := s.AddEmoji("shipit")
	require.Equal(t, 200, a.call("GET", "/api/v4/emoji?page=0&per_page=200", nil, &list))
	assert.Len(t, list, 2)
	var got model.Emoji
	require.Equal(t, 200, a.call("GET", "/api/v4/emoji/name/shipit", nil, &got))
	assert.Equal(t, added.ID, got.ID)
	var evs []string
	for _, ev := range s.Events() {
		evs = append(evs, ev.Name)
	}
	assert.Contains(t, evs, "emoji_added")

	off := Start(Options{DisableCustomEmoji: true})
	defer off.Close()
	b := loginAs(t, off, "alice")
	assert.Equal(t, 501, b.call("GET", "/api/v4/emoji?page=0&per_page=200", nil, nil))
	require.Equal(t, 200, b.call("GET", "/api/v4/config/client?format=old", nil, &cfg))
	assert.Equal(t, "false", cfg["EnableCustomEmoji"])
}

func TestPictureChangeIsBroadcast(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	s.SetPicture("carol")
	evs := s.Events()
	last := evs[len(evs)-1]
	assert.Equal(t, "user_updated", last.Name)
	assert.ElementsMatch(t, []string{"u-alice", "u-bob", "u-carol"}, last.To)
}
