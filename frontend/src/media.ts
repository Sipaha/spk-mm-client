import { useState } from 'react'
import { client } from './api/client'
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

// streamBase caches client.mediaStreamBase() for the life of the page
// (Task 7's contract): the desktop URL carries a secret session token that
// must never sit in localStorage or a log line, and the browser's fixed
// "/media" has nothing to gain from being asked again. A rejected attempt
// is not cached, so the next player retries instead of failing forever.
let streamBase: Promise<string> | undefined

function getStreamBase(): Promise<string> {
  if (!streamBase) {
    streamBase = client.mediaStreamBase().catch((e) => {
      streamBase = undefined
      throw e
    })
  }
  return streamBase
}

// resetStreamBaseForTests: test-only. A successfully resolved base is
// cached for the page's lifetime by design (see above), which means it
// also survives across `test()`s sharing one module instance within a
// file — a test that wants to see a *first* MediaStreamBase() call (e.g.
// a rejection) needs this to undo an earlier test's successful resolution.
// Not called by production code.
export function resetStreamBaseForTests(): void {
  streamBase = undefined
}

// streamURL: the one shape <video>/<audio> read from in both modes,
// `${base}/${serverId}/stream/${fileId}` — only the base differs (a
// same-origin "/media" path in the browser, a loopback URL with a token on
// desktop), and it is asked for at most once per session.
export async function streamURL(serverId: number, fileId: string): Promise<string> {
  const base = await getStreamBase()
  return `${base}/${serverId}/stream/${fileId}`
}
