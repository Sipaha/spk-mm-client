import { act, renderHook, waitFor } from '@testing-library/react'
import { vi } from 'vitest'
import type { EmojiDTO } from '../api/types'
import { forgetRecent, invalidateRecent, useQuickReactions } from './recent'

const dto = (o: Partial<EmojiDTO> = {}): EmojiDTO => ({ recent: [], custom: [], custom_enabled: true, ...o })

// Every test uses server id 1 or 2 — drop both so no state leaks between
// tests (recent.ts's cache is module-level, like emoji/index.ts's index).
afterEach(() => {
  forgetRecent(1)
  forgetRecent(2)
})

test('the first 3 known names of recent; an unknown name is dropped, not left as a hole', async () => {
  const load = vi.fn().mockResolvedValue(dto({ recent: ['tada', 'gone_custom', 'fire', 'rocket'], custom: [] }))
  const { result, unmount } = renderHook(() => useQuickReactions(1, load))
  await waitFor(() => expect(result.current).toEqual(['tada', 'fire', 'rocket']))
  expect(load).toHaveBeenCalledTimes(1)
  unmount()
})

test('an empty recent list means no quick reactions, same as the webapp', async () => {
  const load = vi.fn().mockResolvedValue(dto())
  const { result, unmount } = renderHook(() => useQuickReactions(1, load))
  await waitFor(() => expect(load).toHaveBeenCalled())
  expect(result.current).toEqual([])
  unmount()
})

test('a custom emoji still counts once it is in dto.custom, even before the standard set confirms it is not standard', async () => {
  const load = vi.fn().mockResolvedValue(dto({ recent: ['partyparrot'], custom: ['partyparrot'] }))
  const { result, unmount } = renderHook(() => useQuickReactions(2, load))
  await waitFor(() => expect(result.current).toEqual(['partyparrot']))
  unmount()
})

test('a custom emoji removed from the server (no longer in custom) is dropped from quick reactions, no gap in its place', async () => {
  // 4 recent names, one of them a custom emoji the server no longer has:
  // the next valid name (rocket) fills the third slot instead of a hole.
  const load = vi.fn().mockResolvedValue(dto({ recent: ['tada', 'deleted_custom', 'fire', 'rocket'], custom: ['other_custom'] }))
  const { result, unmount } = renderHook(() => useQuickReactions(1, load))
  await waitFor(() => expect(result.current).toEqual(['tada', 'fire', 'rocket']))
  unmount()
})

test('hovering several posts on the same server makes only one emojiInfo() request (no per-hover fetch)', async () => {
  const load = vi.fn().mockResolvedValue(dto({ recent: ['tada'] }))
  const a = renderHook(() => useQuickReactions(1, load))
  const b = renderHook(() => useQuickReactions(1, load))
  const c = renderHook(() => useQuickReactions(1, load))
  await waitFor(() => expect(a.result.current).toEqual(['tada']))
  await waitFor(() => expect(b.result.current).toEqual(['tada']))
  await waitFor(() => expect(c.result.current).toEqual(['tada']))
  expect(load).toHaveBeenCalledTimes(1)
  a.unmount()
  b.unmount()
  c.unmount()
})

test('invalidateRecent marks the cache stale: the old list stays until the next show lands a fresh one', async () => {
  const load = vi.fn().mockResolvedValueOnce(dto({ recent: ['tada'] })).mockResolvedValueOnce(dto({ recent: ['fire'] }))
  const { result, unmount } = renderHook(() => useQuickReactions(1, load))
  await waitFor(() => expect(result.current).toEqual(['tada']))
  act(() => invalidateRecent(1))
  expect(result.current).toEqual(['tada']) // no flicker: kept until the fresh DTO arrives
  await waitFor(() => expect(result.current).toEqual(['fire']))
  expect(load).toHaveBeenCalledTimes(2)
  unmount()
})

test('invalidating a server that was never shown is a no-op (the next show already starts fresh)', () => {
  expect(() => invalidateRecent(99)).not.toThrow()
})

// Final-review finding (UI pass 2026-09-28): a fetch that was already in
// flight when invalidateRecent fires must not be cached as fresh with an
// older list than the invalidate meant to force a re-read of — the panel
// would otherwise show last add's list, one behind. The still-mounted
// toolbar must refetch on its own once that stale result lands, without
// needing another hover/show.
test('a fetch already in flight when invalidateRecent fires is not cached as fresh: the toolbar refetches on its own', async () => {
  let resolveFirst: ((d: EmojiDTO) => void) | undefined
  const first = new Promise<EmojiDTO>((resolve) => { resolveFirst = resolve })
  const load = vi.fn().mockReturnValueOnce(first).mockResolvedValueOnce(dto({ recent: ['fire'] }))
  const { result, unmount } = renderHook(() => useQuickReactions(1, load))
  await waitFor(() => expect(load).toHaveBeenCalledTimes(1)) // the first fetch started, still pending

  act(() => invalidateRecent(1)) // one of our reactions landed while it was in flight
  await act(async () => resolveFirst?.(dto({ recent: ['tada'] }))) // …and only now does the *old* list arrive

  // The stale result must not be the end of it: a second fetch fires on
  // its own, and its (fresher) list is what the panel actually ends up on.
  await waitFor(() => expect(load).toHaveBeenCalledTimes(2))
  await waitFor(() => expect(result.current).toEqual(['fire']))
  unmount()
})

test('forgetRecent drops a removed server: the next show fetches again from scratch', async () => {
  const load = vi.fn().mockResolvedValue(dto({ recent: ['tada'] }))
  const first = renderHook(() => useQuickReactions(1, load))
  await waitFor(() => expect(first.result.current).toEqual(['tada']))
  first.unmount()
  forgetRecent(1)
  const second = renderHook(() => useQuickReactions(1, load))
  await waitFor(() => expect(second.result.current).toEqual(['tada']))
  expect(load).toHaveBeenCalledTimes(2)
  second.unmount()
})

test('a failed load leaves an empty, stale list; retried at the next show but not more than once per 30s', async () => {
  let now = 1_000_000
  const nowSpy = vi.spyOn(Date, 'now').mockImplementation(() => now)
  const load = vi.fn().mockRejectedValueOnce(new Error('boom')).mockResolvedValue(dto({ recent: ['tada'] }))

  const a = renderHook(() => useQuickReactions(1, load))
  await waitFor(() => expect(load).toHaveBeenCalledTimes(1))
  expect(a.result.current).toEqual([])
  a.unmount()

  // a second show 5s later (< 30s since the failed attempt): throttled
  now += 5_000
  const b = renderHook(() => useQuickReactions(1, load))
  await act(async () => {})
  expect(load).toHaveBeenCalledTimes(1)
  expect(b.result.current).toEqual([])
  b.unmount()

  // a third show 31s after the failed attempt: retried, and succeeds
  now += 26_000
  const c = renderHook(() => useQuickReactions(1, load))
  await waitFor(() => expect(load).toHaveBeenCalledTimes(2))
  await waitFor(() => expect(c.result.current).toEqual(['tada']))
  c.unmount()
  nowSpy.mockRestore()
})
