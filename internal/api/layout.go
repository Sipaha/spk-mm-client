package api

import (
	"context"
	"strconv"
)

// UI-preference keys (internal/store's ui_prefs table, one row per key,
// app-wide -- never per server; theme brief 2026-09-28 scope 3a).
const (
	prefSidebarWidth = "layout.sidebar_width"
	prefThreadWidth  = "layout.thread_width"
)

// LayoutDTO is the persisted sidebar/thread-panel splitter widths. 0 means
// "never saved" -- the frontend applies its own default in that case, so a
// fresh install (or a value that failed to parse) never blocks on this.
type LayoutDTO struct {
	SidebarWidth int `json:"sidebar_width"`
	ThreadWidth  int `json:"thread_width"`
}

func (s *Service) layoutInt(ctx context.Context, key string) (int, error) {
	v, err := s.st.GetUIPref(ctx, key)
	if err != nil {
		return 0, err
	}
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, nil // corrupt value: treat as unset, never fail the read
	}
	return n, nil
}

// GetLayout implements API.
func (s *Service) GetLayout(ctx context.Context) (LayoutDTO, error) {
	sw, err := s.layoutInt(ctx, prefSidebarWidth)
	if err != nil {
		return LayoutDTO{}, coded(CodeInternal, err)
	}
	tw, err := s.layoutInt(ctx, prefThreadWidth)
	if err != nil {
		return LayoutDTO{}, coded(CodeInternal, err)
	}
	return LayoutDTO{SidebarWidth: sw, ThreadWidth: tw}, nil
}

// SetSidebarWidth implements API: called once per drag/keyboard step commit
// (pointerup, not every frame -- see Splitter.tsx). A non-positive width is
// ignored rather than erroring: the caller already clamped, so this only
// guards against a stray/racy call.
func (s *Service) SetSidebarWidth(ctx context.Context, width int) error {
	if width <= 0 {
		return nil
	}
	if err := s.st.SetUIPref(ctx, prefSidebarWidth, strconv.Itoa(width)); err != nil {
		return coded(CodeInternal, err)
	}
	return nil
}

// SetThreadWidth implements API, the thread panel's counterpart of SetSidebarWidth.
func (s *Service) SetThreadWidth(ctx context.Context, width int) error {
	if width <= 0 {
		return nil
	}
	if err := s.st.SetUIPref(ctx, prefThreadWidth, strconv.Itoa(width)); err != nil {
		return coded(CodeInternal, err)
	}
	return nil
}
