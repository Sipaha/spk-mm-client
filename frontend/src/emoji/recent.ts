import { useEffect, useMemo, useSyncExternalStore } from 'react'
import type { EmojiDTO } from '../api/types'
import { emojiChar, useEmojiIndex, type EmojiIndex } from './index'

// recent.ts: per-server cache of the last EmojiDTO fetched through
// api.emojiInfo — the source for the hover bar's quick reactions (Task 2,
// UI pass 2026-09-28, mirrors the webapp's getOneClickReactionEmojis).
//
// PostItem calls useQuickReactions from every hot post's toolbar, so this
// module must turn "many posts hovered in a row" into at most one in-flight
// request per server: a request is asked for only while there is no fresh
// (non-stale) DTO already cached and no request already in flight for that
// server (an emojiInfo() call on every hover was explicitly rejected — see
// the plan's Ruling). Once one of our own reactions is *added*
// (PostItem.tsx's react() calls invalidateRecent only after actions.react's
// promise has settled — not synchronously — so this can't race ahead of
// the backend's own handling of that very call), the cache is marked
// stale — the old list stays on screen (no flicker) until the next show
// re-reads it. A fetch already in flight when invalidateRecent runs never
// gets to mark the cache fresh with a list older than the invalidate (see
// ensureLoaded's gen check) — final-review finding, UI pass 2026-09-28.
//
// A failed request leaves the DTO unset (empty quick-reaction list) and
// stale, retried at the next show but throttled to once per RETRY_MS so a
// server that keeps failing isn't hit on every hover either.

interface Entry {
  dto?: EmojiDTO
  stale: boolean
}

const RETRY_MS = 30_000
const EMPTY_ENTRY: Entry = { stale: true }

// cache/inflight/lastFailAt/gen are plain, not part of Entry: only dto/
// stale are ever handed to a component (via useSyncExternalStore), so only
// they need a fresh object identity on every observable change. inflight/
// lastFailAt/gen are pure request bookkeeping — mutating them in place
// never needs to trigger a re-render.
const cache = new Map<number, Entry>()
const inflight = new Map<number, Promise<void>>()
const lastFailAt = new Map<number, number>()
// gen: bumped by every invalidateRecent call. A fetch that started before
// the latest bump for its server must never mark the cache fresh once it
// resolves — see ensureLoaded's startGen check.
const gen = new Map<number, number>()
const listeners = new Set<() => void>()

function notify(): void {
  listeners.forEach((l) => l())
}

function subscribe(l: () => void): () => void {
  listeners.add(l)
  return () => listeners.delete(l)
}

function snapshot(serverId: number): Entry {
  return cache.get(serverId) ?? EMPTY_ENTRY
}

// ensureLoaded starts a load() call for serverId unless one is already
// in flight, a fresh DTO is already cached, or (after a previous failure)
// less than RETRY_MS has passed since the last attempt. Each attempt's
// result is applied only if it is still the current inflight promise for
// that server — forgetRecent (or a second attempt superseding this one)
// makes a stale, late-arriving response a no-op instead of resurrecting
// dropped state (the same "ignore a late echo" rule the backoff-retried
// reaction toggle follows — see AGENTS.md). A result is also never marked
// *fresh* if invalidateRecent bumped gen for this server while the request
// was in flight — final-review finding: without this, a fetch started
// just before an invalidate could land just after it and cache a list
// older than the invalidate meant to force a re-read of.
function ensureLoaded(serverId: number, load: () => Promise<EmojiDTO>): void {
  if (inflight.has(serverId)) return
  const cached = cache.get(serverId)
  if (cached?.dto && !cached.stale) return
  const failedAt = lastFailAt.get(serverId)
  if (failedAt !== undefined && Date.now() - failedAt < RETRY_MS) return
  const startGen = gen.get(serverId) ?? 0
  const p: Promise<void> = load().then(
    (dto) => {
      if (inflight.get(serverId) !== p) return
      lastFailAt.delete(serverId)
      inflight.delete(serverId)
      // A newer invalidate landed while this was in flight: keep the
      // fresher list on screen (better than the old one) but stay stale,
      // so the still-mounted toolbar's effect fetches again right away
      // instead of waiting for the *next* show.
      cache.set(serverId, { dto, stale: (gen.get(serverId) ?? 0) !== startGen })
      notify()
    },
    () => {
      if (inflight.get(serverId) !== p) return
      lastFailAt.set(serverId, Date.now())
      inflight.delete(serverId)
      cache.set(serverId, { dto: undefined, stale: true })
      notify()
    },
  )
  inflight.set(serverId, p)
}

// invalidateRecent marks serverId's cache stale after one of our own
// reactions was added: the next show re-reads it. A server never shown yet
// has no entry at all — nothing in the cache to mark, but gen still moves
// so a fetch already in flight for it cannot mark itself fresh once this
// returns (see ensureLoaded).
export function invalidateRecent(serverId: number): void {
  gen.set(serverId, (gen.get(serverId) ?? 0) + 1)
  const e = cache.get(serverId)
  if (!e) return
  cache.set(serverId, { ...e, stale: true })
  notify()
}

// forgetRecent drops a removed server's entry — called wherever the front
// end forgets a removed server (store.ts's setServers).
export function forgetRecent(serverId: number): void {
  const had = cache.delete(serverId)
  inflight.delete(serverId)
  lastFailAt.delete(serverId)
  gen.delete(serverId)
  if (had) notify()
}

// quickNames: the first 3 known names of dto.recent (already "most used
// first" from the backend — see EmojiDTO.recent). A name that is neither a
// standard emoji nor a still-existing custom one (e.g. a custom emoji
// deleted from the server since it was reacted with) is dropped, not left
// as a hole — the next known name takes its place instead.
function quickNames(dto: EmojiDTO | undefined, idx: EmojiIndex | null): string[] {
  if (!dto) return []
  return dto.recent.filter((n) => !!emojiChar(n, null) || dto.custom.includes(n) || !!(idx && emojiChar(n, idx))).slice(0, 3)
}

// useQuickReactions is PostItem's hook: mounting it (i.e. the hover bar's
// first show for that post) triggers ensureLoaded, and it returns up to 3
// emoji names once they are known to be standard or still-existing custom
// emoji.
export function useQuickReactions(serverId: number, load: () => Promise<EmojiDTO>): string[] {
  const entry = useSyncExternalStore(subscribe, () => snapshot(serverId))
  const needsIndex = entry.dto?.recent.some((n) => !emojiChar(n, null)) ?? false
  const idx = useEmojiIndex(needsIndex)
  useEffect(() => {
    ensureLoaded(serverId, load)
  }, [serverId, entry, load])
  return useMemo(() => quickNames(entry.dto, idx), [entry.dto, idx])
}
