package state

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/spk/spk-mm-client/internal/mm/model"
)

const (
	maxPostGifs = 5
	maxGifURL   = 2048
)

// GifPreviewView is a deduplicated GIF preview extracted from a post's
// markdown, bare URLs or server metadata. The UI always loads it via
// /media/<srv>/postgif/<postID>?k=<ID> — never from the remote URL directly.
type GifPreviewView struct {
	ID     string `json:"id"`
	URL    string `json:"url"`
	Alt    string `json:"alt,omitempty"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
	Inline bool   `json:"inline,omitempty"`
}

// PermalinkPreviewView is a compact quoted-message card built from a server
// metadata.embeds entry with type="permalink".
type PermalinkPreviewView struct {
	PostID             string `json:"post_id"`
	ChannelID          string `json:"channel_id,omitempty"`
	RootID             string `json:"root_id,omitempty"`
	UserID             string `json:"user_id,omitempty"`
	Author             string `json:"author,omitempty"`
	Avatar             string `json:"avatar,omitempty"`
	Message            string `json:"message"`
	CreateAt           int64  `json:"create_at,omitempty"`
	EditAt             int64  `json:"edit_at,omitempty"`
	TeamName           string `json:"team_name,omitempty"`
	ChannelDisplayName string `json:"channel_display_name,omitempty"`
	ChannelName        string `json:"channel_name,omitempty"`
	ChannelType        string `json:"channel_type,omitempty"`
	FileCount          int    `json:"file_count,omitempty"`
}

// GifKey returns the stable 16-hex identifier of a GIF URL used in ?k=.
func GifKey(rawURL string) string {
	sum := sha256.Sum256([]byte(rawURL))
	return hex.EncodeToString(sum[:8])
}

// PostGifURL looks up the remote/server GIF URL for a held post by its 16-hex
// key. Only held posts are eligible (never an open proxy).
func (s *Server) PostGifURL(postID, key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.findPostLocked(postID)
	if !ok {
		return "", false
	}
	for _, g := range extractPostGifs(p.Message, p.Metadata) {
		if g.ID == key {
			return g.URL, true
		}
	}
	return "", false
}

var (
	mdImageRe = regexp.MustCompile(`!\[([^\]\r\n]*)\]\(([^)\r\n]+)\)`)
	mdSizeRe  = regexp.MustCompile(`^=(\d+)(?:x(\d+))?$`)
	bareURLRe = regexp.MustCompile(`https?://[^\s<>()\[\]"']+`)
)

func extractPostGifs(message string, meta *model.PostMetadata) []GifPreviewView {
	var out []GifPreviewView
	seen := map[string]bool{}
	var imgs map[string]model.PostImage
	if meta != nil {
		imgs = meta.Images
	}

	add := func(rawURL, alt string, w, h int, inline bool) {
		if len(out) >= maxPostGifs {
			return
		}
		u, ok := cleanGifCandidateURL(rawURL)
		if !ok || seen[u] {
			return
		}
		if img, exists := imgs[u]; exists {
			if w == 0 {
				w = img.Width
			}
			if h == 0 {
				h = img.Height
			}
		}
		seen[u] = true
		out = append(out, GifPreviewView{
			ID:     GifKey(u),
			URL:    u,
			Alt:    strings.TrimSpace(alt),
			Width:  w,
			Height: h,
			Inline: inline,
		})
	}

	cleaned := stripMarkdownCode(message)

	// (a) Markdown images ![alt](url) outside code blocks/spans.
	if cleaned != "" && strings.Contains(cleaned, "![") {
		for _, m := range mdImageRe.FindAllStringSubmatch(cleaned, -1) {
			alt := m[1]
			u, w, h := parseMarkdownImageDest(m[2])
			if u == "" {
				continue
			}
			if model.IsGifURL(u) || hasMetaGifImage(imgs, u) {
				add(u, alt, w, h, true)
			}
		}
	}

	// (b) Server metadata.embeds (image / opengraph) and metadata.images.
	if meta != nil {
		for _, e := range meta.Embeds {
			switch e.Type {
			case "image":
				if e.URL != "" && (model.IsGifURL(e.URL) || hasMetaGifImage(imgs, e.URL)) {
					add(e.URL, "", 0, 0, false)
				}
				for _, u := range e.ImageURLs {
					if model.IsGifURL(u) || hasMetaGifImage(imgs, u) {
						add(u, "", 0, 0, false)
					}
				}
			case "opengraph":
				for _, u := range e.ImageURLs {
					add(u, "", 0, 0, false)
				}
			}
		}
		if len(imgs) > 0 {
			keys := make([]string, 0, len(imgs))
			for u, img := range imgs {
				if img.IsGif() || model.IsGifURL(u) {
					keys = append(keys, u)
				}
			}
			sort.Strings(keys)
			for _, u := range keys {
				add(u, "", 0, 0, false)
			}
		}
	}

	// (c) Bare .gif URLs in message (outside code). Any URL already matched in
	// (a) or (b) is skipped via seen.
	if cleaned != "" && (strings.Contains(cleaned, "http://") || strings.Contains(cleaned, "https://")) {
		for _, raw := range bareURLRe.FindAllString(cleaned, -1) {
			u := strings.TrimRight(raw, ".,;:!?\"'}")
			if model.IsGifURL(u) {
				add(u, "", 0, 0, false)
			}
		}
	}

	return out
}

func hasMetaGifImage(imgs map[string]model.PostImage, u string) bool {
	if len(imgs) == 0 {
		return false
	}
	img, ok := imgs[u]
	return ok && (img.IsGif() || model.IsGifURL(u))
}

func cleanGifCandidateURL(raw string) (string, bool) {
	u := strings.TrimSpace(raw)
	if u == "" || len(u) > maxGifURL {
		return "", false
	}
	parsed, err := url.Parse(u)
	if err != nil || parsed.Opaque != "" || parsed.User != nil {
		return "", false
	}
	switch parsed.Scheme {
	case "http", "https":
		if parsed.Host == "" {
			return "", false
		}
		return u, true
	case "":
		if strings.HasPrefix(u, "/") && !strings.HasPrefix(u, "//") {
			return u, true
		}
		return "", false
	default:
		return "", false
	}
}

// parseMarkdownImageDest extracts the URL and optional Mattermost =WxH size
// suffix from the destination inside ![alt](dest).
func parseMarkdownImageDest(dest string) (rawURL string, w, h int) {
	dest = strings.TrimSpace(dest)
	if dest == "" {
		return "", 0, 0
	}
	var rest string
	if strings.HasPrefix(dest, "<") {
		if end := strings.IndexByte(dest, '>'); end > 1 {
			rawURL = strings.TrimSpace(dest[1:end])
			rest = strings.TrimSpace(dest[end+1:])
		} else {
			return "", 0, 0
		}
	} else {
		fields := strings.Fields(dest)
		if len(fields) == 0 {
			return "", 0, 0
		}
		rawURL = fields[0]
		if len(fields) > 1 {
			rest = strings.Join(fields[1:], " ")
		}
	}
	for _, tok := range strings.Fields(rest) {
		if m := mdSizeRe.FindStringSubmatch(tok); m != nil {
			w, _ = strconv.Atoi(m[1])
			if m[2] != "" {
				h, _ = strconv.Atoi(m[2])
			}
		}
	}
	return rawURL, w, h
}

// stripMarkdownCode blanks fenced code blocks (``` / ~~~) and inline code
// spans (`...`) so URLs or image syntax inside code never produce GIF previews.
func stripMarkdownCode(s string) string {
	if !strings.ContainsAny(s, "`~") {
		return s
	}
	lines := strings.Split(s, "\n")
	var out strings.Builder
	out.Grow(len(s))
	var fenceChar byte
	fenceLen := 0
	for i, line := range lines {
		if i > 0 {
			out.WriteByte('\n')
		}
		trimmed := strings.TrimLeft(line, " ")
		indent := len(line) - len(trimmed)
		if fenceChar != 0 {
			if indent <= 3 && isClosingFence(trimmed, fenceChar, fenceLen) {
				fenceChar, fenceLen = 0, 0
			}
			continue
		}
		if indent <= 3 {
			if ch, n := openingFence(trimmed); n >= 3 {
				fenceChar, fenceLen = ch, n
				continue
			}
		}
		out.WriteString(stripInlineCode(line))
	}
	return out.String()
}

func openingFence(trimmed string) (byte, int) {
	if len(trimmed) < 3 || (trimmed[0] != '`' && trimmed[0] != '~') {
		return 0, 0
	}
	ch := trimmed[0]
	n := 0
	for n < len(trimmed) && trimmed[n] == ch {
		n++
	}
	if ch == '`' && strings.ContainsRune(trimmed[n:], '`') {
		return 0, 0 // inline code on one line, not a fenced block
	}
	return ch, n
}

func isClosingFence(trimmed string, ch byte, minLen int) bool {
	n := 0
	for n < len(trimmed) && trimmed[n] == ch {
		n++
	}
	return n >= minLen && strings.TrimSpace(trimmed[n:]) == ""
}

func stripInlineCode(line string) string {
	if !strings.ContainsRune(line, '`') {
		return line
	}
	var out strings.Builder
	out.Grow(len(line))
	for i := 0; i < len(line); {
		if line[i] != '`' {
			out.WriteByte(line[i])
			i++
			continue
		}
		ticks := 0
		for i+ticks < len(line) && line[i+ticks] == '`' {
			ticks++
		}
		// Find matching closing run of exact length `ticks`.
		j := i + ticks
		found := -1
		for j < len(line) {
			if line[j] != '`' {
				j++
				continue
			}
			run := 0
			for j+run < len(line) && line[j+run] == '`' {
				run++
			}
			if run == ticks {
				found = j + run
				break
			}
			j += run
		}
		if found < 0 {
			out.WriteString(line[i : i+ticks])
			i += ticks
		} else {
			out.WriteByte(' ')
			i = found
		}
	}
	return out.String()
}
