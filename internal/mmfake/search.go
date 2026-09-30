package mmfake

import (
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

// Post search, as the server's database engine (PostgreSQL full-text search)
// answers it — docs/research/2026-09-24-mattermost-api-facts.md §9. The
// query is parsed like model.ParseSearchParams (public/model/search_params.go)
// and matched like SqlPostStore.search (store/sqlstore/post_store.go), with
// two simplifications: words are not stemmed (Postgres' english config
// would match "deploys" to "deploy"), and the MySQL dialect is not modelled.

// SearchCall is one POST /teams/{id}/posts/search the fake answered
// (PerPage as the server used it: 60 when the body had none).
type SearchCall struct {
	TeamID     string `json:"team_id"`
	UserID     string `json:"user_id"`
	Terms      string `json:"terms"`
	IsOrSearch bool   `json:"is_or_search"`
	Page       int    `json:"page"`
	PerPage    int    `json:"per_page"`
}

// SearchCalls lists every answered search, oldest first.
func (s *Server) SearchCalls() []SearchCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]SearchCall{}, s.chat.searches...)
}

func (s *Server) searchRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v4/teams/{tid}/posts/search", s.handleAuthed(s.searchPosts))
}

// sqlSearchLimit is SqlPostStore.search's Limit(100): the most one query
// group returns.
const sqlSearchLimit = 100

// searchPosts mirrors api4/post.go searchPostsInTeam/searchPosts: team
// permission (403) first, then the body (400 on bad JSON, 400 on a missing
// or empty terms), then the search.
func (s *Server) searchPosts(w http.ResponseWriter, r *http.Request, u User) {
	tid := r.PathValue("tid")
	s.mu.Lock()
	inTeam := s.inTeamLocked(tid)
	s.mu.Unlock()
	if !inTeam {
		appError(w, 403, "api.context.permissions.app_error", "no permission")
		return
	}
	var in struct {
		Terms                  *string `json:"terms"`
		IsOrSearch             bool    `json:"is_or_search"`
		TimeZoneOffset         int     `json:"time_zone_offset"`
		IncludeDeletedChannels bool    `json:"include_deleted_channels"`
		Page                   int     `json:"page"`
		PerPage                *int    `json:"per_page"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		appError(w, 400, "api.post.search_posts.invalid_body.app_error", "invalid body")
		return
	}
	if in.Terms == nil || *in.Terms == "" {
		appError(w, 400, "api.context.invalid_param.app_error", "Invalid terms parameter.")
		return
	}
	perPage := 60
	if in.PerPage != nil {
		perPage = *in.PerPage
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.chat.searches = append(s.chat.searches, SearchCall{TeamID: tid, UserID: u.ID, Terms: *in.Terms,
		IsOrSearch: in.IsOrSearch, Page: in.Page, PerPage: perPage})

	channels := s.searchChannelsLocked(u.ID, tid, in.IncludeDeletedChannels)
	hits := map[string]*fpost{}
	if !s.opts.SearchSQLEngine || in.Page == 0 {
		for _, g := range parseSearchGroups(strings.TrimSpace(*in.Terms), in.TimeZoneOffset, in.IsOrSearch) {
			found := s.searchGroupLocked(g, u.ID, tid, channels, in.IncludeDeletedChannels)
			if s.opts.SearchSQLEngine && len(found) > sqlSearchLimit {
				found = found[:sqlSearchLimit]
			}
			for _, p := range found {
				hits[p.ID] = p
			}
		}
	}
	all := make([]*fpost, 0, len(hits))
	for _, p := range hits {
		all = append(all, p)
	}
	sortNewestFirst(all)
	if !s.opts.SearchSQLEngine {
		// A search engine's offset pages: page × per_page.
		per := max(perPage, 0)
		start := min(max(in.Page, 0)*per, len(all))
		all = all[start:min(start+per, len(all))]
	}
	res := model.PostSearchResults{PostList: model.PostList{Order: []string{}, Posts: map[string]model.Post{}}}
	for _, p := range all {
		res.Order = append(res.Order, p.ID)
		res.Posts[p.ID] = p.Post
	}
	writeJSON(w, 200, res) // matches: null — the database engine never fills it
}

func sortNewestFirst(ps []*fpost) {
	sort.SliceStable(ps, func(i, j int) bool {
		if ps[i].CreateAt != ps[j].CreateAt {
			return ps[i].CreateAt > ps[j].CreateAt
		}
		return ps[i].ID > ps[j].ID
	})
}

// searchChannelsLocked is the search's channel scope (post_store.go search,
// inQuery): the caller's channels of this team plus their DMs/GMs (team_id
// ""), archived ones only when asked.
func (s *Server) searchChannelsLocked(userID, teamID string, includeDeleted bool) map[string]bool {
	out := map[string]bool{}
	for id, c := range s.chat.channels {
		if !s.isMemberLocked(id, userID) || (c.TeamID != teamID && c.TeamID != "") {
			continue
		}
		if c.DeleteAt != 0 && !includeDeleted {
			continue
		}
		out[id] = true
	}
	return out
}

// searchGroupLocked answers one query group, newest first.
func (s *Server) searchGroupLocked(g *searchGroup, userID, teamID string, channels map[string]bool, includeDeleted bool) []*fpost {
	if g.onlyExcludedText() {
		// Postgres: to_tsquery(' &!(…)') is a syntax error, which search()
		// logs and answers with an empty list.
		return nil
	}
	inChannels := s.resolveSearchChannelsLocked(g.inChannels, userID, teamID, includeDeleted)
	exChannels := s.resolveSearchChannelsLocked(g.exChannels, userID, teamID, includeDeleted)
	fromUsers := s.resolveSearchUsers(g.fromUsers)
	exUsers := s.resolveSearchUsers(g.exUsers)
	var out []*fpost
	for cid := range channels {
		if (len(inChannels) > 0 && !slices.Contains(inChannels, cid)) || slices.Contains(exChannels, cid) {
			continue
		}
		for _, p := range s.chat.posts[cid] {
			if p.DeleteAt != 0 || p.OriginalID != "" || strings.HasPrefix(p.Type, "system_") {
				continue
			}
			if (len(fromUsers) > 0 && !slices.Contains(fromUsers, p.UserID)) || slices.Contains(exUsers, p.UserID) {
				continue
			}
			if !g.dates.match(p.CreateAt) || !g.matchText(p.Message) {
				continue
			}
			out = append(out, p)
		}
	}
	sortNewestFirst(out)
	return out
}

// resolveSearchChannelsLocked mirrors app/post.go
// parseAndFetchChannelIdByNameFromInFilter: "~" is trimmed; "@user" is the
// caller's DM with user; "@a,b,c" the GM of exactly those users; anything
// else a channel name of the team. A name that resolves to nothing stays
// as it is, and so matches no channel id.
func (s *Server) resolveSearchChannelsLocked(names []string, userID, teamID string, includeDeleted bool) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimLeft(name, "~")
		id := name
		switch {
		case strings.HasPrefix(name, "@"):
			want := map[string]bool{}
			users := strings.Split(name[1:], ",")
			for _, un := range users {
				if uid := s.userIDOf(un); uid != "" {
					want[uid] = true
				}
			}
			typ := model.ChannelGroup
			if len(users) == 1 {
				typ = model.ChannelDirect
				want[userID] = true
			}
			for cid, c := range s.chat.channels {
				if c.Type == typ && sameMembers(s.chat.members[cid], want) {
					id = cid
				}
			}
		default:
			for cid, c := range s.chat.channels {
				if c.TeamID == teamID && c.Name == name && (c.DeleteAt == 0 || includeDeleted) {
					id = cid
				}
			}
		}
		out = append(out, id)
	}
	return out
}

func sameMembers(members map[string]*model.ChannelMember, want map[string]bool) bool {
	if len(members) != len(want) {
		return false
	}
	for uid := range members {
		if !want[uid] {
			return false
		}
	}
	return true
}

// resolveSearchUsers mirrors convertUserNameToUserIds: "@" trimmed, an
// unknown username kept as it is (it then matches nobody).
func (s *Server) resolveSearchUsers(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		n = strings.TrimLeft(n, "@")
		if id := s.userIDOf(n); id != "" {
			out = append(out, id)
		} else {
			out = append(out, n)
		}
	}
	return out
}

func (s *Server) userIDOf(username string) string {
	for _, u := range s.opts.Users {
		if strings.EqualFold(u.Username, username) {
			return u.ID
		}
	}
	return ""
}

// --- query parsing (public/model/search_params.go) ---

var (
	searchTermPuncStart = regexp.MustCompile(`^[^\pL\d\s#"]+`)
	searchTermPuncEnd   = regexp.MustCompile(`[^\pL\p{M}\d\s*"]+$`)
	searchHashtagStart  = regexp.MustCompile(`^#{2,}`)
	searchValidHashtag  = regexp.MustCompile(`^(#\pL[\pL\d\-_.]*[\pL\d])$`)
)

var searchFlagNames = [...]string{"from", "channel", "in", "before", "after", "on", "ext"}

type searchWordTok struct {
	value   string
	exclude bool
}

type searchFlagTok struct {
	name, value string
	exclude     bool
}

// splitSearchWords is search_params.go splitWords: whitespace-separated,
// but a "quoted phrase" (with a leading - kept) is one word.
func splitSearchWords(text string) []string {
	words := []string{}
	foundQuote := false
	location := 0
	for i, char := range text {
		if char != '"' {
			continue
		}
		if foundQuote {
			words = append(words, text[location:i+1])
			foundQuote = false
			location = i + 1
		} else {
			nextStart := i
			if i > 0 && text[i-1] == '-' {
				nextStart = i - 1
			}
			words = append(words, strings.Fields(text[location:nextStart])...)
			foundQuote = true
			location = nextStart
		}
	}
	return append(words, strings.Fields(text[location:])...)
}

// parseSearchFlags is search_params.go parseSearchFlags: name:value (or
// "name:" followed by the value as the next word), -name:value excludes;
// other words lose surrounding punctuation (a trailing * stays).
func parseSearchFlags(input []string) ([]searchWordTok, []searchFlagTok) {
	var words []searchWordTok
	var flags []searchFlagTok
	skipNext := false
	for i, word := range input {
		if skipNext {
			skipNext = false
			continue
		}
		isFlag := false
		if colon := strings.Index(word, ":"); colon != -1 {
			name, exclude := word[:colon], false
			if strings.HasPrefix(word, "-") {
				name, exclude = word[1:colon], true
			}
			value := word[colon+1:]
			for _, f := range searchFlagNames {
				if !strings.EqualFold(name, f) {
					continue
				}
				if value != "" {
					flags = append(flags, searchFlagTok{f, value, exclude})
					isFlag = true
				} else if i < len(input)-1 {
					flags = append(flags, searchFlagTok{f, input[i+1], exclude})
					skipNext, isFlag = true, true
				}
				if isFlag {
					break
				}
			}
		}
		if isFlag {
			continue
		}
		exclude := strings.HasPrefix(word, "-")
		word = searchTermPuncStart.ReplaceAllString(word, "")
		word = searchTermPuncEnd.ReplaceAllString(word, "")
		word = searchHashtagStart.ReplaceAllString(word, "#")
		if word != "" {
			words = append(words, searchWordTok{word, exclude})
		}
	}
	return words, flags
}

// searchGroup is one model.SearchParams of the parsed list: plain words,
// hashtags, or (neither) filters only.
type searchGroup struct {
	hashtag              bool
	or                   bool
	terms, excluded      []searchUnit
	inChannels           []string
	exChannels           []string
	fromUsers, exUsers   []string
	dates                searchDates
	hasTerms, hasExclude bool
}

// searchUnit is one tsquery operand: a word (prefix if it ended in *) or a
// phrase of adjacent words.
type searchUnit struct {
	words  []string // lower-cased
	prefix bool     // the last word is a prefix
}

func (g *searchGroup) onlyExcludedText() bool { return !g.hasTerms && g.hasExclude }

func parseSearchGroups(text string, tz int, or bool) []*searchGroup {
	words, flags := parseSearchFlags(splitSearchWords(text))
	base := searchGroup{or: or, dates: searchDates{tz: tz}}
	for _, f := range flags {
		switch f.name {
		case "in", "channel":
			if f.exclude {
				base.exChannels = append(base.exChannels, f.value)
			} else {
				base.inChannels = append(base.inChannels, f.value)
			}
		case "from":
			if f.exclude {
				base.exUsers = append(base.exUsers, f.value)
			} else {
				base.fromUsers = append(base.fromUsers, f.value)
			}
		case "after":
			setExcludable(f, &base.dates.after, &base.dates.exAfter)
		case "before":
			setExcludable(f, &base.dates.before, &base.dates.exBefore)
		case "on":
			setExcludable(f, &base.dates.on, &base.dates.exOn)
		}
	}
	var plain, exPlain, tags, exTags []string
	for _, w := range words {
		isTag := searchValidHashtag.MatchString(w.value)
		switch {
		case isTag && w.exclude:
			exTags = append(exTags, w.value)
		case isTag:
			tags = append(tags, w.value)
		case w.exclude:
			exPlain = append(exPlain, w.value)
		default:
			plain = append(plain, w.value)
		}
	}
	var out []*searchGroup
	if len(plain) > 0 || len(exPlain) > 0 {
		g := base
		g.terms = plainUnits(removeNonAlphaNumericUnquoted(strings.Join(plain, " ")))
		g.excluded = plainUnits(strings.Join(exPlain, " "))
		g.hasTerms, g.hasExclude = len(g.terms) > 0, len(g.excluded) > 0
		if g.hasTerms || g.hasExclude {
			out = append(out, &g)
		} else if base.hasFilters() {
			// Every word was dropped: search() with no terms and no
			// filters answers nothing; with filters, the filters alone.
			out = append(out, &g)
		}
	}
	if len(tags) > 0 || len(exTags) > 0 {
		g := base
		g.hashtag = true
		for _, t := range tags {
			g.terms = append(g.terms, searchUnit{words: []string{strings.ToLower(t)}})
		}
		for _, t := range exTags {
			g.excluded = append(g.excluded, searchUnit{words: []string{strings.ToLower(t)}})
		}
		g.hasTerms, g.hasExclude = len(g.terms) > 0, len(g.excluded) > 0
		out = append(out, &g)
	}
	if len(words) == 0 && base.hasFilters() {
		g := base
		out = append(out, &g)
	}
	return out
}

func (g *searchGroup) hasFilters() bool {
	return len(g.inChannels) > 0 || len(g.exChannels) > 0 || len(g.fromUsers) > 0 || len(g.exUsers) > 0 || g.dates.any()
}

// removeNonAlphaNumericUnquoted is sqlstore/utils.go
// removeNonAlphaNumericUnquotedTerms: space-separated pieces without a
// letter or digit are dropped (unless quoted).
func removeNonAlphaNumericUnquoted(line string) string {
	var kept []string
	for _, w := range strings.Split(line, " ") {
		quoted := len(w) > 1 && strings.HasPrefix(w, `"`) && strings.HasSuffix(w, `"`)
		if quoted || strings.IndexFunc(w, isWordRune) >= 0 {
			kept = append(kept, strings.TrimSpace(w))
		}
	}
	return strings.Join(kept, " ")
}

// plainUnits turns the store's term string into tsquery operands: special
// characters (< > + - ( ) ~ : @) are spaces (specialSearchChars), a
// "quoted phrase" is one operand of adjacent words, a word ending in * is a
// prefix (wildCardRegex), and to_tsquery splits the rest into words — an
// operand of several words (e.g. "don't") is matched as adjacent words.
func plainUnits(terms string) []searchUnit {
	terms = strings.Map(func(r rune) rune {
		if strings.ContainsRune("<>+-()~:@", r) {
			return ' '
		}
		return r
	}, terms)
	var out []searchUnit
	add := func(piece string, prefix bool) {
		if ws := messageWords(piece); len(ws) > 0 {
			out = append(out, searchUnit{words: ws, prefix: prefix})
		}
	}
	for _, piece := range splitSearchWords(terms) {
		if strings.HasPrefix(piece, `"`) {
			add(strings.Trim(piece, `"`), false)
			continue
		}
		add(piece, strings.HasSuffix(piece, "*"))
	}
	return out
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) }

// messageWords splits text into lower-cased words of letters, marks and
// digits — what to_tsvector indexes, minus stemming.
func messageWords(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !isWordRune(r) })
}

func messageHashtags(text string) []string {
	var out []string
	for _, w := range strings.Fields(text) {
		w = searchTermPuncEnd.ReplaceAllString(w, "")
		if searchValidHashtag.MatchString(w) {
			out = append(out, strings.ToLower(w))
		}
	}
	return out
}

func (g *searchGroup) matchText(msg string) bool {
	if !g.hasTerms && !g.hasExclude {
		return true // filters only
	}
	var words []string
	if g.hashtag {
		words = messageHashtags(msg)
	} else {
		words = messageWords(msg)
	}
	found := func(u searchUnit) bool { return u.in(words) }
	ok := true
	if g.or {
		ok = slices.ContainsFunc(g.terms, found)
	} else {
		for _, u := range g.terms {
			ok = ok && found(u)
		}
	}
	return ok && !slices.ContainsFunc(g.excluded, found)
}

// in reports whether the unit's words occur adjacently in words.
func (u searchUnit) in(words []string) bool {
	n := len(u.words)
	for i := 0; i+n <= len(words); i++ {
		match := true
		for j, w := range u.words {
			got := words[i+j]
			if j == n-1 && u.prefix {
				match = match && strings.HasPrefix(got, w)
			} else {
				match = match && got == w
			}
		}
		if match {
			return true
		}
	}
	return false
}

// searchDates are the before:/after:/on: filters (and their -exclusions),
// as buildCreateDateFilterClause applies them: days in the caller's zone
// (time_zone_offset seconds east of UTC); after: starts the next day,
// before: ends the previous one; on: wins over the rest.
type searchDates struct {
	tz                      int
	after, before, on       string
	exAfter, exBefore, exOn string
}

// setExcludable stores a date flag's value: the last one of a kind wins,
// like ParseSearchParams.
func setExcludable(f searchFlagTok, v, ex *string) {
	if f.exclude {
		*ex = f.value
	} else {
		*v = f.value
	}
}

func (d searchDates) any() bool {
	return d.after != "" || d.before != "" || d.on != "" || d.exAfter != "" || d.exBefore != "" || d.exOn != ""
}

// padDate is model.PadDateStringZeros: 2026-9-3 → 2026-09-03.
func padDate(s string) string {
	parts := strings.Split(s, "-")
	for i, p := range parts {
		if len(p) == 1 {
			parts[i] = "0" + p
		}
	}
	return strings.Join(parts, "-")
}

func (d searchDates) day(date string, hour, minute, sec, nsec int, shift int) (int64, bool) {
	t, err := time.Parse("2006-01-02", padDate(date))
	if err != nil {
		return 0, false
	}
	t = t.AddDate(0, 0, shift)
	loc := time.FixedZone("search", d.tz)
	return time.Date(t.Year(), t.Month(), t.Day(), hour, minute, sec, nsec, loc).UnixMilli(), true
}

func (d searchDates) startOf(date string, shift int) (int64, bool) {
	return d.day(date, 0, 0, 0, 0, shift)
}

func (d searchDates) endOf(date string, shift int) (int64, bool) {
	return d.day(date, 23, 59, 59, 999999999, shift)
}

// afterMillis is GetAfterDateMillis: an unparsable date counts as today.
func (d searchDates) afterMillis(date string) int64 {
	if ms, ok := d.startOf(date, 1); ok {
		return ms
	}
	now := time.Now().AddDate(0, 0, 1)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.FixedZone("search", d.tz)).UnixMilli()
}

func (d searchDates) match(at int64) bool {
	if d.on != "" {
		start, _ := d.startOf(d.on, 0) // unparsable: BETWEEN 0 AND 0
		end, _ := d.endOf(d.on, 0)
		return at >= start && at <= end
	}
	if d.exOn != "" {
		start, _ := d.startOf(d.exOn, 0)
		end, _ := d.endOf(d.exOn, 0)
		if at >= start && at <= end {
			return false
		}
	}
	if d.after != "" && at < d.afterMillis(d.after) {
		return false
	}
	if d.before != "" {
		end, _ := d.endOf(d.before, -1) // unparsable: CreateAt <= 0
		if at > end {
			return false
		}
	}
	if d.exAfter != "" && at >= d.afterMillis(d.exAfter) {
		return false
	}
	if d.exBefore != "" {
		end, _ := d.endOf(d.exBefore, -1)
		if at <= end {
			return false
		}
	}
	return true
}
