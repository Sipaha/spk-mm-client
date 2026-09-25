package api

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/adrg/xdg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-mm-client/internal/mm/model"
	"github.com/spk/spk-mm-client/internal/mm/rest"
)

func downloadsIn(dir string) func(string) string {
	return func(k string) string {
		if k == "SPK_MM_CLIENT_DOWNLOADS" {
			return dir
		}
		return ""
	}
}

func TestDownloadSavesIntoDownloadsWithUniqueNames(t *testing.T) {
	f := newChatFixture(t)
	dl := filepath.Join(t.TempDir(), "Загрузки")
	f.svc.getenv = downloadsIn(dl)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	ctx := context.Background()

	r1, err := f.svc.DownloadFile(ctx, id, "f-spec")
	require.NoError(t, err)
	assert.Equal(t, SavedFile{Path: filepath.Join(dl, "spec.pdf")}, r1)
	data, err := os.ReadFile(r1.Path)
	require.NoError(t, err)
	assert.True(t, bytes.HasPrefix(data, []byte("%PDF")))

	r2, err := f.svc.DownloadFile(ctx, id, "f-spec")
	require.NoError(t, err)
	assert.Equal(t, r1, r2, "the same file is not saved twice")
	assert.Equal(t, 1, fake.Hits("GET", "/api/v4/files/f-spec"))

	require.NoError(t, os.WriteFile(r1.Path, []byte("edited"), 0o644))
	r3, err := f.svc.DownloadFile(ctx, id, "f-spec")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dl, "spec (1).pdf"), r3.Path, "a changed copy is kept, the new one gets a free name")
	assertNoPart(t, dl)
}

func assertNoPart(t *testing.T, dir string) {
	t.Helper()
	parts, err := filepath.Glob(filepath.Join(dir, ".*.part"))
	require.NoError(t, err)
	assert.Empty(t, parts, "no partial file is left behind")
}

func TestDownloadNeverFollowsAPlantedSymlink(t *testing.T) {
	f := newChatFixture(t)
	dl := t.TempDir()
	f.svc.getenv = downloadsIn(dl)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	victim := filepath.Join(t.TempDir(), "victim")
	require.NoError(t, os.WriteFile(victim, []byte("keep"), 0o644))
	require.NoError(t, os.Symlink(victim, filepath.Join(dl, "spec.pdf")))
	require.NoError(t, os.Symlink(filepath.Join(dl, "nowhere"), filepath.Join(dl, "spec (1).pdf")))

	r, err := f.svc.DownloadFile(context.Background(), id, "f-spec")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dl, "spec (2).pdf"), r.Path, "a symlink, even a dangling one, takes the name")
	data, err := os.ReadFile(victim)
	require.NoError(t, err)
	assert.Equal(t, "keep", string(data))
	assert.NoFileExists(t, filepath.Join(dl, "nowhere"))
	assertNoPart(t, dl)
}

func TestSaveIntoRejectsASizeMismatch(t *testing.T) {
	for _, body := range []string{"short", "much longer than declared"} {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
		dir := t.TempDir()
		_, err := saveInto(context.Background(), rest.New(ts.URL, "", ts.Client()), dir, "f1", model.FileInfo{ID: "f1", Name: "a.txt", Size: 10})
		ts.Close()
		require.ErrorIs(t, err, errSizeMismatch, body)
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		assert.Empty(t, entries, "neither the file nor its .part stays: %s", body)
	}
}

func TestSaveIntoStreamsTheValidatedID(t *testing.T) {
	var got string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Path
		_, _ = w.Write([]byte("0123456789"))
	}))
	defer ts.Close()
	dir := t.TempDir()
	p, err := saveInto(context.Background(), rest.New(ts.URL, "", ts.Client()), dir, "f1", model.FileInfo{ID: "../other", Name: "a.txt", Size: 10})
	require.NoError(t, err)
	assert.Equal(t, "/api/v4/files/f1", got)
	assert.Equal(t, filepath.Join(dir, "a.txt"), p)
}

func TestOpenFileOpensSafeTypesOnly(t *testing.T) {
	f := newChatFixture(t)
	dl := t.TempDir()
	f.svc.getenv = downloadsIn(dl)
	opened := &RecordingOpener{}
	f.svc.SetFileOpener(opened.Open)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	ctx := context.Background()

	r, err := f.svc.OpenFile(ctx, id, "f-log")
	require.NoError(t, err)
	assert.True(t, r.Opened)
	assert.Equal(t, []string{filepath.Join(dl, "server.log")}, opened.List())

	p := fake.PostFile("c-offtopic", "bob", "run me", "setup.desktop", "application/x-desktop", []byte("[Desktop Entry]\nExec=rm -rf ~\n"))
	r, err = f.svc.OpenFile(ctx, id, p.Metadata.Files[0].ID)
	require.NoError(t, err)
	assert.False(t, r.Opened, "launchers are saved, never opened")
	assert.FileExists(t, r.Path)
	assert.Len(t, opened.List(), 1)

	p = fake.PostFile("c-offtopic", "bob", "no ext", "README", "text/plain", []byte("#!/bin/sh\nrm -rf ~\n"))
	r, err = f.svc.OpenFile(ctx, id, p.Metadata.Files[0].ID)
	require.NoError(t, err)
	assert.False(t, r.Opened, "without an extension the system handler is picked by sniffing the content")
	assert.FileExists(t, r.Path)
	assert.Len(t, opened.List(), 1)
}

func TestOpenFileAllowsInertTypesOnly(t *testing.T) {
	f := newChatFixture(t)
	dl := t.TempDir()
	f.svc.getenv = downloadsIn(dl)
	opened := &RecordingOpener{}
	f.svc.SetFileOpener(opened.Open)
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	for name, want := range map[string]bool{
		"doc.pdf": true, "Photo.JPG": true, "notes.md": true, "sheet.xlsx": true, "clip.webm": true, "src.tar.gz": true,
		"page.html": false, "page.htm": false, "pic.svg": false, "help.chm": false, "SETUP.EXE": false,
		"macro.docm": false, "old.doc": false, "link.url": false, "disk.iso": false, "thing.xyz": false,
		"trailing.exe.": false, "noext": false,
	} {
		p := fake.PostFile("c-offtopic", "bob", "file", name, "application/octet-stream", []byte("data"))
		r, err := f.svc.OpenFile(context.Background(), id, p.Metadata.Files[0].ID)
		require.NoError(t, err, name)
		assert.Equal(t, want, r.Opened, name)
		assert.FileExists(t, r.Path, name)
		assert.Equal(t, want, slices.Contains(opened.List(), r.Path), name)
	}
	assert.FileExists(t, filepath.Join(dl, "trailing.exe"), "the trailing dot is stripped; .exe is still not opened")
}

func TestSavedRecordIsBounded(t *testing.T) {
	s := &Service{saved: map[string]string{}}
	for i := 0; i < maxSaved+50; i++ {
		s.remember(fmt.Sprint(i), "p")
	}
	assert.Len(t, s.saved, maxSaved)
	assert.Equal(t, "p", s.saved[fmt.Sprint(maxSaved+49)], "the newest entry is kept")
}

func TestDownloadErrors(t *testing.T) {
	f := newChatFixture(t)
	f.svc.getenv = downloadsIn(t.TempDir())
	fake := startFake(t)
	id := f.signIn(fake, "alice")
	ctx := context.Background()
	_, err := f.svc.DownloadFile(ctx, id, "f-nope")
	assert.Equal(t, CodeNoFile, codeOf(err))
	_, err = f.svc.DownloadFile(ctx, id, "../etc")
	assert.Equal(t, CodeNoFile, codeOf(err))
	assert.Zero(t, fake.Hits("GET", "/api/v4/files/../etc/info"))
	_, err = f.svc.DownloadFile(ctx, 999, "f-spec")
	assert.Equal(t, CodeNotFound, codeOf(err))
}

func TestSanitizeName(t *testing.T) {
	for in, want := range map[string]string{
		"../../.bashrc":                   ".bashrc",
		`a\b\c.txt`:                       "c.txt",
		"x\x00y.txt":                      "x_y.txt",
		`re:port?.pdf`:                    "re_port_.pdf",
		"  spaced.txt ":                   "spaced.txt",
		"":                                "file",
		"..":                              "file",
		"...":                             "file",
		"dir/":                            "file",
		"a\u200bb.txt":                    "a_b.txt",
		"\ufeffx.txt":                     "_x.txt",
		"w\u2060j.txt":                    "w_j.txt",
		"CON":                             "_CON",
		"con.txt":                         "_con.txt",
		"Lpt9.tar.gz":                     "_Lpt9.tar.gz",
		"nul ":                            "_nul",
		"CONSOLE.txt":                     "CONSOLE.txt",
		"COM10.txt":                       "COM10.txt",
		"report.pdf. ":                    "report.pdf",
		"end.":                            "end",
		strings.Repeat("a", 199) + ".txt": strings.Repeat("a", 199),
		"evil\u202etxt.exe":               "evil_txt.exe",
		"bad\xffutf8.txt":                 "bad\uFFFDutf8.txt",
		strings.Repeat("я", 150):          strings.Repeat("я", 100),
	} {
		assert.Equal(t, want, sanitizeName(in), in)
	}
}

func TestDownloadsDirFollowsEnvThenXDG(t *testing.T) {
	prev := xdg.UserDirs.Download
	t.Cleanup(func() { xdg.UserDirs.Download = prev })
	xdg.UserDirs.Download = "/xdg/Загрузки"
	d, err := downloadsDir(func(string) string { return "/x/dl" })
	require.NoError(t, err)
	assert.Equal(t, "/x/dl", d)
	d, err = downloadsDir(func(string) string { return "" })
	require.NoError(t, err)
	assert.Equal(t, "/xdg/Загрузки", d)
	xdg.UserDirs.Download = ""
	d, err = downloadsDir(func(string) string { return "" })
	require.NoError(t, err)
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, "Downloads"), d)
}

func TestDownloadsDirNeverTheHomeDirectory(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	prev := xdg.UserDirs.Download
	t.Cleanup(func() { xdg.UserDirs.Download = prev })
	xdg.UserDirs.Download = home + "/" // XDG_DOWNLOAD_DIR="$HOME/": the directory is disabled
	d, err := downloadsDir(func(string) string { return "" })
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, "Downloads"), d, "a dotfile from the server must not land next to ~/.profile")
}
