package api

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrg/xdg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		"../../.bashrc":          ".bashrc",
		`a\b\c.txt`:              "c.txt",
		"x\x00y.txt":             "x_y.txt",
		`re:port?.pdf`:           "re_port_.pdf",
		"  spaced.txt ":          "spaced.txt",
		"":                       "file",
		"..":                     "file",
		"...":                    "file",
		"dir/":                   "file",
		"evil\u202etxt.exe":      "evil_txt.exe",
		"bad\xffutf8.txt":        "bad\uFFFDutf8.txt",
		strings.Repeat("я", 150): strings.Repeat("я", 100),
	} {
		assert.Equal(t, want, sanitizeName(in), in)
	}
}

func TestDownloadsDirFollowsEnvThenXDG(t *testing.T) {
	d, err := downloadsDir(func(string) string { return "/x/dl" })
	require.NoError(t, err)
	assert.Equal(t, "/x/dl", d)
	d, err = downloadsDir(func(string) string { return "" })
	require.NoError(t, err)
	assert.Equal(t, xdg.UserDirs.Download, d)
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
