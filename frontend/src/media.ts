export type MediaKind = 'avatar' | 'thumb' | 'feed' | 'full' | 'text' | 'emoji'

// mediaURL addresses a picture or file snippet that Go serves on the UI's
// own origin (internal/media): the UI never talks to a Mattermost server
// and never sees its tokens.
export function mediaURL(serverId: number, kind: MediaKind, key: string, params: Record<string, string> = {}): string {
  const q = new URLSearchParams(params).toString()
  return `/media/${serverId}/${kind}/${encodeURIComponent(key)}${q ? `?${q}` : ''}`
}
