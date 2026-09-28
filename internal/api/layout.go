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

// Absolute splitter bounds (review follow-up 2026-09-29): the frontend also
// caps the thread panel at 50% of the window, but Go has no window size to
// check that against, so these are the server-side floor/ceiling -- applied
// on every write (SetSidebarWidth/SetThreadWidth) and on every read
// (GetLayout, in case a value was persisted before these bounds existed or
// was edited by hand), so a bad value never reaches the UI either way.
const (
	sidebarWidthMin = 180
	sidebarWidthMax = 480
	threadWidthMin  = 320
	threadWidthMax  = 800
)

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

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

// GetLayout implements API. Persisted values are re-clamped to the absolute
// bounds on the way out (not just on write) so a value stored before the
// bounds existed, or edited by hand in the DB, never reaches the UI as is.
// 0 ("never saved") is left alone -- it is not a width to clamp, it is the
// frontend's cue to apply its own built-in default.
func (s *Service) GetLayout(ctx context.Context) (LayoutDTO, error) {
	sw, err := s.layoutInt(ctx, prefSidebarWidth)
	if err != nil {
		return LayoutDTO{}, coded(CodeInternal, err)
	}
	if sw > 0 {
		sw = clampInt(sw, sidebarWidthMin, sidebarWidthMax)
	}
	tw, err := s.layoutInt(ctx, prefThreadWidth)
	if err != nil {
		return LayoutDTO{}, coded(CodeInternal, err)
	}
	if tw > 0 {
		tw = clampInt(tw, threadWidthMin, threadWidthMax)
	}
	return LayoutDTO{SidebarWidth: sw, ThreadWidth: tw}, nil
}

// SetSidebarWidth implements API: called once per drag/keyboard step commit
// (pointerup, not every frame -- see Splitter.tsx). A non-positive width is
// ignored rather than erroring: the caller already clamped, so this only
// guards against a stray/racy call; anything else is clamped to
// [sidebarWidthMin, sidebarWidthMax] -- the frontend clamps too, but the
// server never trusts that on its own (the 50%-of-window ceiling in
// particular is client-only, since Go has no window size to check it
// against).
func (s *Service) SetSidebarWidth(ctx context.Context, width int) error {
	if width <= 0 {
		return nil
	}
	width = clampInt(width, sidebarWidthMin, sidebarWidthMax)
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
	width = clampInt(width, threadWidthMin, threadWidthMax)
	if err := s.st.SetUIPref(ctx, prefThreadWidth, strconv.Itoa(width)); err != nil {
		return coded(CodeInternal, err)
	}
	return nil
}
