package api

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/spk/spk-mm-client/internal/mm/rest"
	"github.com/spk/spk-mm-client/internal/mmsync"
)

// Composer autocomplete and slash commands (brief 2026-09-29). Go makes
// every server call; the UI passes only a kind, ids and the typed prefix,
// all checked here.

// Autocomplete DTOs: one popup's answer and its rows (mmsync).
type (
	// AutocompleteDTO is one popup's answer.
	AutocompleteDTO = mmsync.Autocomplete
	// ACUser is a user row.
	ACUser = mmsync.ACUser
	// ACChannel is a channel row.
	ACChannel = mmsync.ACChannel
	// ACCommand is a slash command row.
	ACCommand = mmsync.ACCommand
)

const (
	// MaxAutocompletePrefix caps the word after the trigger: a longer one
	// suggests nothing (no request).
	MaxAutocompletePrefix = 64
	// MaxCommandRunes caps a slash command: the server's post limit
	// (PostMessageMaxRunesV2).
	MaxCommandRunes = 16383
)

var idShape = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func validID(id string) bool { return idShape.MatchString(id) }

// suggestable: a prefix worth asking about — no whitespace or control
// characters (a word being typed), valid UTF-8, not over the cap.
func suggestable(prefix string) bool {
	if !utf8.ValidString(prefix) || utf8.RuneCountInString(prefix) > MaxAutocompletePrefix {
		return false
	}
	return !strings.ContainsFunc(prefix, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) })
}

func (s *Service) Autocomplete(ctx context.Context, id int64, kind, channelID, rootID, prefix string) (AutocompleteDTO, error) {
	switch kind {
	case mmsync.ACUsers, mmsync.ACChannels, mmsync.ACEmoji, mmsync.ACCommands:
	default:
		return AutocompleteDTO{}, coded(CodeInvalidArgument, errors.New("kind"))
	}
	if !validID(channelID) || (rootID != "" && !validID(rootID)) {
		return AutocompleteDTO{}, coded(CodeInvalidArgument, errors.New("id"))
	}
	w, err := s.worker(ctx, id)
	if err != nil {
		return AutocompleteDTO{}, err
	}
	if !suggestable(prefix) {
		if _, ok := w.State().AutocompleteScope(channelID); !ok {
			return AutocompleteDTO{}, coded(CodeNoChannel, nil)
		}
		return AutocompleteDTO{}, nil
	}
	rctx, cancel := s.bounded(ctx)
	defer cancel()
	res, err := w.Autocomplete(rctx, kind, channelID, prefix)
	if errors.Is(err, mmsync.ErrNoChannel) {
		return AutocompleteDTO{}, coded(CodeNoChannel, nil)
	}
	if err != nil {
		return AutocompleteDTO{}, actionError(err)
	}
	return res, nil
}

func (s *Service) ExecuteCommand(ctx context.Context, id int64, channelID, rootID, command string) error {
	if !validID(channelID) || (rootID != "" && !validID(rootID)) {
		return coded(CodeInvalidArgument, errors.New("id"))
	}
	cmd := strings.TrimSpace(command)
	if len(cmd) < 2 || cmd[0] != '/' || !utf8.ValidString(cmd) || utf8.RuneCountInString(cmd) > MaxCommandRunes {
		return coded(CodeInvalidArgument, errors.New("command"))
	}
	w, err := s.writer(ctx, id)
	if err != nil {
		return err
	}
	if rootID != "" && !w.State().ThreadHeld(channelID, rootID) {
		return coded(CodeNoPost, nil)
	}
	rctx, cancel := s.bounded(ctx)
	defer cancel()
	err = w.ExecuteCommand(rctx, channelID, rootID, cmd)
	switch {
	case err == nil:
	case errors.Is(err, mmsync.ErrNoChannel):
		return coded(CodeNoChannel, nil)
	case errors.Is(err, mmsync.ErrCommandNotFound):
		return coded(CodeCommandNotFound, nil)
	case errors.Is(err, mmsync.ErrUnsupportedInThread):
		return coded(CodeCommandUnsupportedInThread, nil)
	case rest.IsNetwork(err):
		// Never retried and never offered as if nothing had happened: the
		// POST may have reached the server.
		return coded(CodeCommandUncertain, err)
	default:
		return actionError(err)
	}
	// The server's /logout only answers "go to /login" (command_logout.go:
	// "Actual logout is handled client side"): sign out the way the
	// sidebar's "Sign out" does.
	if mmsync.CommandTrigger(mmsync.NormalizeCommand(cmd)) == "/logout" {
		return s.Logout(ctx, id)
	}
	return nil
}
