package desktop

import "net/http"

// withMedia routes /media/ to the media cache and everything else to the UI
// assets: the webview loads pictures from the UI's own origin, so nothing in
// the webview ever talks to a Mattermost server. media nil → assets only.
func withMedia(assets, media http.Handler) http.Handler {
	if media == nil {
		return assets
	}
	mux := http.NewServeMux()
	mux.Handle("/media/", media)
	mux.Handle("/", assets)
	return mux
}
