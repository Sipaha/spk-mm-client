package model

import (
	"encoding/json"
	"net/url"
	"sort"
	"strings"
)

const (
	maxMetadataImages     = 5
	maxMetadataEmbeds     = 5
	maxPermalinkMsgRunes  = 500
	maxOpenGraphImageURLs = 5
	maxEmbedURLBytes      = 2048
)

// PostImage holds the compact image dimensions/format metadata Mattermost
// attaches in metadata.images. Only GIF entries are retained at decode time.
type PostImage struct {
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	Format     string `json:"format,omitempty"`
	FrameCount int    `json:"frame_count,omitempty"`
}

// IsGif reports whether img describes a GIF (explicit format or multi-frame).
func (img PostImage) IsGif() bool {
	return strings.EqualFold(img.Format, "gif") || img.FrameCount > 1
}

// PermalinkPost is the compact subset of a quoted post inside a permalink embed.
type PermalinkPost struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id,omitempty"`
	UserID    string `json:"user_id,omitempty"`
	RootID    string `json:"root_id,omitempty"`
	Message   string `json:"message"`
	CreateAt  int64  `json:"create_at,omitempty"`
	EditAt    int64  `json:"edit_at,omitempty"`
	FileCount int    `json:"file_count,omitempty"`
}

// PermalinkData is the compact payload of a type="permalink" post embed.
type PermalinkData struct {
	PostID             string        `json:"post_id"`
	Post               PermalinkPost `json:"post"`
	TeamName           string        `json:"team_name,omitempty"`
	ChannelDisplayName string        `json:"channel_display_name,omitempty"`
	ChannelName        string        `json:"channel_name,omitempty"`
	ChannelType        string        `json:"channel_type,omitempty"`
}

// PostEmbed is a compact retained embed: either a permalink quote card or a
// GIF image/opengraph reference. Arbitrary OpenGraph text and raw JSON are
// discarded at decode time.
type PostEmbed struct {
	Type      string         `json:"type"`
	URL       string         `json:"url,omitempty"`
	Permalink *PermalinkData `json:"permalink,omitempty"`
	ImageURLs []string       `json:"image_urls,omitempty"`
}

// IsGifURL reports whether raw's path ends with ".gif" (case-insensitive),
// ignoring query parameters and fragments.
func IsGifURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return strings.HasSuffix(strings.ToLower(u.Path), ".gif")
}

func truncateRunes(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	if limit == 1 {
		return "…"
	}
	return string(r[:limit-1]) + "…"
}

type wirePostMetadata struct {
	Files     []FileInfo      `json:"files,omitempty"`
	Reactions []Reaction      `json:"reactions,omitempty"`
	Embeds    json.RawMessage `json:"embeds,omitempty"`
	Images    json.RawMessage `json:"images,omitempty"`
}

type wireEmbed struct {
	Type      string          `json:"type"`
	URL       string          `json:"url,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
	Permalink *PermalinkData  `json:"permalink,omitempty"`
	ImageURLs []string        `json:"image_urls,omitempty"`
}

type wirePermalinkPost struct {
	ID        string   `json:"id"`
	ChannelID string   `json:"channel_id,omitempty"`
	UserID    string   `json:"user_id,omitempty"`
	RootID    string   `json:"root_id,omitempty"`
	Message   string   `json:"message"`
	CreateAt  int64    `json:"create_at,omitempty"`
	EditAt    int64    `json:"edit_at,omitempty"`
	FileIDs   []string `json:"file_ids,omitempty"`
	FileCount int      `json:"file_count,omitempty"`
	Metadata  *struct {
		Files []json.RawMessage `json:"files,omitempty"`
	} `json:"metadata,omitempty"`
}

type wirePermalinkData struct {
	PostID             string            `json:"post_id"`
	Post               wirePermalinkPost `json:"post"`
	TeamName           string            `json:"team_name,omitempty"`
	ChannelDisplayName string            `json:"channel_display_name,omitempty"`
	ChannelName        string            `json:"channel_name,omitempty"`
	ChannelType        string            `json:"channel_type,omitempty"`
}

type wireOpenGraph struct {
	Images []struct {
		URL       string `json:"url"`
		SecureURL string `json:"secure_url"`
		Type      string `json:"type"`
	} `json:"images"`
}

// UnmarshalJSON decodes both Mattermost wire metadata (with embeds[].data) and
// compact SQLite snapshot metadata (with embeds[].permalink / image_urls),
// retaining only files, reactions, permalink embeds and GIF image/embed info.
// Malformed embeds or images are ignored rather than failing the whole post.
func (m *PostMetadata) UnmarshalJSON(data []byte) error {
	var raw wirePostMetadata
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*m = PostMetadata{
		Files:     raw.Files,
		Reactions: raw.Reactions,
	}

	// Decode images first so image/opengraph embeds can recognize GIF URLs
	// that lack a ".gif" suffix (e.g. CDN/Tenor/Giphy URLs with format=gif).
	var gifImages map[string]PostImage
	if len(raw.Images) > 0 {
		var imgs map[string]PostImage
		if json.Unmarshal(raw.Images, &imgs) == nil && len(imgs) > 0 {
			keys := make([]string, 0, len(imgs))
			for u, img := range imgs {
				u = strings.TrimSpace(u)
				if u == "" {
					continue
				}
				if img.IsGif() || IsGifURL(u) {
					keys = append(keys, u)
				}
			}
			sort.Strings(keys)
			if len(keys) > maxMetadataImages {
				keys = keys[:maxMetadataImages]
			}
			if len(keys) > 0 {
				gifImages = make(map[string]PostImage, len(keys))
				for _, k := range keys {
					gifImages[k] = imgs[k]
				}
				m.Images = gifImages
			}
		}
	}

	if len(raw.Embeds) > 0 {
		var embeds []wireEmbed
		if json.Unmarshal(raw.Embeds, &embeds) == nil && len(embeds) > 0 {
			out := make([]PostEmbed, 0, min(len(embeds), maxMetadataEmbeds))
			ogRemaining := maxOpenGraphImageURLs
			for _, e := range embeds {
				if len(out) >= maxMetadataEmbeds {
					break
				}
				switch e.Type {
				case "permalink":
					pl := decodePermalinkEmbed(e)
					if pl != nil {
						out = append(out, PostEmbed{
							Type:      "permalink",
							URL:       strings.TrimSpace(e.URL),
							Permalink: pl,
						})
					}
				case "image":
					u := strings.TrimSpace(e.URL)
					if u != "" && (IsGifURL(u) || hasImageKey(gifImages, u)) {
						out = append(out, PostEmbed{Type: "image", URL: u})
					} else if urls := filterGifURLs(e.ImageURLs, gifImages); len(urls) > 0 {
						out = append(out, PostEmbed{Type: "image", ImageURLs: urls})
					}
				case "opengraph":
					if ogRemaining <= 0 {
						continue
					}
					urls := decodeOpenGraphGifURLs(e, gifImages, ogRemaining)
					if len(urls) > 0 {
						ogRemaining -= len(urls)
						out = append(out, PostEmbed{Type: "opengraph", URL: strings.TrimSpace(e.URL), ImageURLs: urls})
					}
				}
			}
			if len(out) > 0 {
				m.Embeds = out
			}
		}
	}
	return nil
}

func hasImageKey(imgs map[string]PostImage, u string) bool {
	if len(imgs) == 0 {
		return false
	}
	_, ok := imgs[u]
	return ok
}

func decodePermalinkEmbed(e wireEmbed) *PermalinkData {
	if e.Permalink != nil {
		pl := *e.Permalink
		if pl.PostID == "" {
			pl.PostID = pl.Post.ID
		}
		if pl.Post.ID == "" {
			pl.Post.ID = pl.PostID
		}
		if pl.PostID == "" {
			return nil
		}
		pl.Post.Message = truncateRunes(pl.Post.Message, maxPermalinkMsgRunes)
		return &pl
	}
	if len(e.Data) == 0 {
		return nil
	}
	var wd wirePermalinkData
	if err := json.Unmarshal(e.Data, &wd); err != nil {
		return nil
	}
	postID := wd.PostID
	if postID == "" {
		postID = wd.Post.ID
	}
	if postID == "" {
		return nil
	}
	id := wd.Post.ID
	if id == "" {
		id = postID
	}
	fc := wd.Post.FileCount
	if n := len(wd.Post.FileIDs); n > fc {
		fc = n
	}
	if wd.Post.Metadata != nil {
		if n := len(wd.Post.Metadata.Files); n > fc {
			fc = n
		}
	}
	return &PermalinkData{
		PostID: postID,
		Post: PermalinkPost{
			ID:        id,
			ChannelID: wd.Post.ChannelID,
			UserID:    wd.Post.UserID,
			RootID:    wd.Post.RootID,
			Message:   truncateRunes(wd.Post.Message, maxPermalinkMsgRunes),
			CreateAt:  wd.Post.CreateAt,
			EditAt:    wd.Post.EditAt,
			FileCount: fc,
		},
		TeamName:           wd.TeamName,
		ChannelDisplayName: wd.ChannelDisplayName,
		ChannelName:        wd.ChannelName,
		ChannelType:        wd.ChannelType,
	}
}

func isSafeHTTPURL(raw string) bool {
	if raw == "" || len(raw) > maxEmbedURLBytes {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.User != nil || u.Host == "" {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

func decodeOpenGraphGifURLs(e wireEmbed, gifImages map[string]PostImage, limit int) []string {
	if limit <= 0 {
		return nil
	}
	// Wire Mattermost OpenGraph embeds carry their payload in `data` and never
	// set `image_urls`; decode and filter them strictly by GIF type/extension/images.
	if len(e.Data) > 0 {
		var og wireOpenGraph
		if err := json.Unmarshal(e.Data, &og); err != nil || len(og.Images) == 0 {
			return nil
		}
		var out []string
		seen := map[string]bool{}
		for _, img := range og.Images {
			u := strings.TrimSpace(img.SecureURL)
			if !isSafeHTTPURL(u) {
				u = strings.TrimSpace(img.URL)
			}
			if !isSafeHTTPURL(u) || seen[u] {
				continue
			}
			if strings.EqualFold(img.Type, "image/gif") || IsGifURL(u) || hasImageKey(gifImages, u) {
				seen[u] = true
				out = append(out, u)
				if len(out) >= limit {
					break
				}
			}
		}
		return out
	}
	// Compact SQLite snapshot format: `image_urls` already contains only
	// GIF-verified URLs from wire decoding (including no-extension URLs whose
	// OpenGraph image had type="image/gif"). Re-validate http(s) scheme, dedup
	// and cap without requiring a ".gif" path suffix.
	if len(e.ImageURLs) > 0 {
		var out []string
		seen := map[string]bool{}
		for _, raw := range e.ImageURLs {
			u := strings.TrimSpace(raw)
			if !isSafeHTTPURL(u) || seen[u] {
				continue
			}
			seen[u] = true
			out = append(out, u)
			if len(out) >= limit {
				break
			}
		}
		return out
	}
	return nil
}

func filterGifURLs(urls []string, gifImages map[string]PostImage) []string {
	var out []string
	seen := map[string]bool{}
	for _, u := range urls {
		u = strings.TrimSpace(u)
		if u == "" || seen[u] {
			continue
		}
		if IsGifURL(u) || hasImageKey(gifImages, u) {
			seen[u] = true
			out = append(out, u)
			if len(out) >= maxOpenGraphImageURLs {
				break
			}
		}
	}
	return out
}
