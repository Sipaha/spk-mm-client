// Package appfiles embeds static app assets (icons).
package appfiles

import _ "embed"

//go:embed icons/icon.png
var IconPNG []byte
