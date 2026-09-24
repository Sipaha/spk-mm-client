package model

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Flag is a bool that also accepts the strings "true"/"false" (Mattermost
// props mix both).
type Flag bool

func (f *Flag) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	*f = Flag(strings.EqualFold(s, "true"))
	return nil
}

// FlexString accepts a JSON string, number or bool and keeps its text.
type FlexString string

func (s *FlexString) UnmarshalJSON(b []byte) error {
	var str string
	if json.Unmarshal(b, &str) == nil {
		*s = FlexString(str)
		return nil
	}
	t := strings.TrimSpace(string(b))
	if t == "null" {
		*s = ""
		return nil
	}
	if _, err := strconv.ParseFloat(t, 64); err == nil || t == "true" || t == "false" {
		*s = FlexString(t)
		return nil
	}
	*s = ""
	return nil
}

type AttachmentField struct {
	Title string     `json:"title,omitempty"`
	Value FlexString `json:"value,omitempty"`
	Short Flag       `json:"short,omitempty"`
}

type Attachment struct {
	Fallback   string            `json:"fallback,omitempty"`
	Color      string            `json:"color,omitempty"`
	Pretext    string            `json:"pretext,omitempty"`
	AuthorName string            `json:"author_name,omitempty"`
	Title      string            `json:"title,omitempty"`
	TitleLink  string            `json:"title_link,omitempty"`
	Text       string            `json:"text,omitempty"`
	Footer     string            `json:"footer,omitempty"`
	Fields     []AttachmentField `json:"fields,omitempty"`
}

// PostProps keeps the props the client renders. Decoding never fails: a
// field of an unexpected shape is dropped, not the whole post (bots and
// plugins put arbitrary JSON in props).
type PostProps struct {
	FromBot           Flag         `json:"from_bot,omitempty"`
	FromWebhook       Flag         `json:"from_webhook,omitempty"`
	OverrideUsername  FlexString   `json:"override_username,omitempty"`
	ForceNotification Flag         `json:"force_notification,omitempty"`
	Attachments       []Attachment `json:"attachments,omitempty"`
}

func (p *PostProps) UnmarshalJSON(b []byte) error {
	*p = PostProps{}
	var m map[string]json.RawMessage
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	_ = json.Unmarshal(m["from_bot"], &p.FromBot)
	_ = json.Unmarshal(m["from_webhook"], &p.FromWebhook)
	_ = json.Unmarshal(m["override_username"], &p.OverrideUsername)
	_ = json.Unmarshal(m["force_notification"], &p.ForceNotification)
	if raw, ok := m["attachments"]; ok {
		var atts []Attachment
		if json.Unmarshal(raw, &atts) == nil {
			p.Attachments = atts
		}
	}
	return nil
}
