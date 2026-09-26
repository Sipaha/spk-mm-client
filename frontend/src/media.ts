import { useState } from 'react'
import { useLiveEpoch } from './store'

export type MediaKind = 'avatar' | 'thumb' | 'feed' | 'full' | 'text' | 'emoji'

// mediaURL addresses a picture or file snippet that Go serves on the UI's
// own origin (internal/media): the UI never talks to a Mattermost server
// and never sees its tokens.
export function mediaURL(serverId: number, kind: MediaKind, key: string, params: Record<string, string> = {}): string {
  const q = new URLSearchParams(params).toString()
  return `/media/${serverId}/${kind}/${encodeURIComponent(key)}${q ? `?${q}` : ''}`
}

// useLoadFailure remembers that the media of `key` failed to load until the
// server next goes live (its live epoch changes): offline, /media/ answers
// 404 for everything, and a row that stays mounted must not keep initials or
// a card for the rest of the session. A failure while live (a real 404, an
// SVG refused) costs one cached /media/ answer per reconnect.
export function useLoadFailure(serverId: number, key: string): [failed: boolean, fail: () => void] {
  const epoch = useLiveEpoch(serverId)
  const [failedAt, setFailedAt] = useState<string | null>(null)
  const tag = `${epoch}|${key}`
  return [failedAt === tag, () => setFailedAt(tag)]
}
