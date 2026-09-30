package api

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/spk/spk-mm-client/internal/mmsync"
)

// Message search and its from:/in: suggestions (spec «Поиск», Секции 2–3).
// The UI passes a team id, the query and a page; all checked here, Go
// makes the request.

type (
	// SearchPageDTO is one page of hits (mmsync.SearchPage).
	SearchPageDTO = mmsync.SearchPage
	// SearchHitDTO is a found post with its channel.
	SearchHitDTO = mmsync.SearchHit
)

const (
	// MaxSearchTerms caps a query in bytes.
	MaxSearchTerms = 1000
	// minTZOffset/maxTZOffset bound time_zone_offset (seconds east of UTC):
	// UTC−12…UTC+14.
	minTZOffset, maxTZOffset = -12 * 3600, 14 * 3600
)

func (s *Service) SearchPosts(ctx context.Context, id int64, teamID, terms string, page, tzOffset int) (SearchPageDTO, error) {
	terms = strings.TrimSpace(terms)
	switch {
	case !validID(teamID):
		return SearchPageDTO{}, coded(CodeInvalidArgument, errors.New("id"))
	case terms == "" || len(terms) > MaxSearchTerms || !utf8.ValidString(terms):
		return SearchPageDTO{}, coded(CodeInvalidArgument, errors.New("terms"))
	case page < 0 || page >= mmsync.SearchMaxPages:
		return SearchPageDTO{}, coded(CodeInvalidArgument, errors.New("page"))
	case tzOffset < minTZOffset || tzOffset > maxTZOffset:
		return SearchPageDTO{}, coded(CodeInvalidArgument, errors.New("time zone offset"))
	}
	w, err := s.worker(ctx, id)
	if err != nil {
		return SearchPageDTO{}, err
	}
	rctx, cancel := s.bounded(ctx)
	defer cancel()
	res, err := w.SearchPosts(rctx, teamID, terms, page, tzOffset)
	if err != nil {
		return SearchPageDTO{}, searchError(err)
	}
	return res, nil
}

func (s *Service) SearchSuggest(ctx context.Context, id int64, teamID, kind, prefix string) (AutocompleteDTO, error) {
	if kind != mmsync.ACUsers && kind != mmsync.ACChannels {
		return AutocompleteDTO{}, coded(CodeInvalidArgument, errors.New("kind"))
	}
	if !validID(teamID) {
		return AutocompleteDTO{}, coded(CodeInvalidArgument, errors.New("id"))
	}
	w, err := s.worker(ctx, id)
	if err != nil {
		return AutocompleteDTO{}, err
	}
	if !suggestable(prefix) {
		if !w.HasTeam(teamID) {
			return AutocompleteDTO{}, coded(CodeInvalidArgument, mmsync.ErrUnknownTeam)
		}
		return AutocompleteDTO{}, nil
	}
	rctx, cancel := s.bounded(ctx)
	defer cancel()
	res, err := w.SearchSuggest(rctx, teamID, kind, prefix)
	if err != nil {
		return AutocompleteDTO{}, searchError(err)
	}
	return res, nil
}

// searchError: a search's error as a code — offline, a wrong team/page/
// kind, a cancelled call and the rest (actionError: 401, 403, network)
// apart.
func searchError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, mmsync.ErrOffline):
		return coded(CodeOffline, nil)
	case errors.Is(err, mmsync.ErrUnknownTeam), errors.Is(err, mmsync.ErrSearchPage), errors.Is(err, mmsync.ErrSuggestKind):
		return coded(CodeInvalidArgument, err)
	case errors.Is(err, context.Canceled):
		return coded(CodeCancelled, err)
	}
	return actionError(err)
}
