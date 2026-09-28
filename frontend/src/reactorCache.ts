import type { ReactionUsersDTO } from './api/types'

// A small bounded LRU, module-level so it survives PostItem/Reactions
// remounts across the virtualized feed (a per-component cache would be
// created fresh every time a row remounts and never hit). Keyed on
// (server, post, emoji, count): a changed count means the chip's reaction
// list moved since the last fetch — stale, so it misses and refetches
// (the brief's rule).
const MAX_ENTRIES = 100

const cache = new Map<string, ReactionUsersDTO>()

function key(serverId: number, postId: string, emoji: string, count: number): string {
  return `${serverId}:${postId}:${emoji}:${count}`
}

export function getCachedReactors(serverId: number, postId: string, emoji: string, count: number): ReactionUsersDTO | undefined {
  const k = key(serverId, postId, emoji, count)
  const v = cache.get(k)
  if (v === undefined) return undefined
  // Re-insert to mark it most-recently-used (Map iteration/eviction order
  // follows insertion order).
  cache.delete(k)
  cache.set(k, v)
  return v
}

export function setCachedReactors(serverId: number, postId: string, emoji: string, count: number, value: ReactionUsersDTO): void {
  const k = key(serverId, postId, emoji, count)
  cache.delete(k)
  cache.set(k, value)
  if (cache.size > MAX_ENTRIES) {
    const oldest = cache.keys().next().value
    if (oldest !== undefined) cache.delete(oldest)
  }
}

export function _resetReactorCacheForTests(): void {
  cache.clear()
}
