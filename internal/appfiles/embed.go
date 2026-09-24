// Package appfiles embeds static app assets (icons).
package appfiles

import _ "embed"

//go:embed icons/icon.png
var IconPNG []byte

//go:embed icons/icon-unread.png
var IconUnreadPNG []byte

//go:embed icons/icon-mention.png
var IconMentionPNG []byte
