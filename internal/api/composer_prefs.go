package api

import "context"

// prefFormattingBarHidden persists the composer's Aa toggle (composer brief
// 2026-09-28) -- app-wide, the same ui_prefs mechanism as the layout
// splitters (internal/store's uiprefs.go, see layout.go). Unset ("") means
// "never saved": GetFormattingBarHidden reports false (shown), the
// webapp's own default.
const prefFormattingBarHidden = "composer.formatting_bar_hidden"

// GetFormattingBarHidden implements API.
func (s *Service) GetFormattingBarHidden(ctx context.Context) (bool, error) {
	v, err := s.st.GetUIPref(ctx, prefFormattingBarHidden)
	if err != nil {
		return false, coded(CodeInternal, err)
	}
	return v == "1", nil
}

// SetFormattingBarHidden implements API.
func (s *Service) SetFormattingBarHidden(ctx context.Context, hidden bool) error {
	v := "0"
	if hidden {
		v = "1"
	}
	if err := s.st.SetUIPref(ctx, prefFormattingBarHidden, v); err != nil {
		return coded(CodeInternal, err)
	}
	return nil
}
