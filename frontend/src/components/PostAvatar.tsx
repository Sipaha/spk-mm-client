import { useState } from 'react'
import type { PostView } from '../api/types'
import { mediaURL, useLoadFailure } from '../media'
import { Avatar } from './Avatar'
import { EmojiGlyph } from './EmojiGlyph'

interface Props {
  serverId: number
  post: Pick<PostView, 'id' | 'user_id' | 'author' | 'avatar' | 'status' | 'icon' | 'icon_version'>
  size: number
}

// PostAvatar is the picture next to a post: a webhook's own icon when Go
// says so (PostView.icon) — its picture from /media/<srv>/posticon/<post
// id> (by post, never by URL: Go fetches it) or an emoji — otherwise the
// account's avatar, which is also what a failed icon falls back to. Eager
// like Avatar (see there).
//
// Until the icon has loaded the account's avatar stands in over it: a new
// post is a new /media URL, and one whose picture Go has not fetched yet (a
// webhook's first icon from an external host takes a round trip there; a
// broken one fails only after it) would otherwise be an empty circle for
// that long. An icon already in the webview's cache is complete at mount:
// no stand-in, no flash.
export function PostAvatar({ serverId, post, size }: Props) {
  // A failure is remembered per icon version: an edit that changes the
  // icon tries the new one.
  const version = post.icon_version ?? ''
  const [failed, fail] = useLoadFailure(serverId, `${post.id}|${version}`)
  const [loadedSrc, setLoadedSrc] = useState('')
  const icon = post.icon ?? ''
  const emoji = icon.length > 2 && icon.startsWith(':') && icon.endsWith(':') ? icon.slice(1, -1) : ''
  if (emoji) {
    return (
      <span
        className="flex shrink-0 items-center justify-center rounded-full bg-hover"
        style={{ width: size, height: size, fontSize: Math.round(size * 0.6), lineHeight: 1 }}
      >
        <EmojiGlyph serverId={serverId} name={emoji} size={Math.round(size * 0.7)} />
      </span>
    )
  }
  const account = <Avatar serverId={serverId} userId={post.user_id} version={post.avatar} name={post.author} status={post.status} size={size} surface="app" />
  if (icon === 'post' && !failed) {
    const src = mediaURL(serverId, 'posticon', post.id, version ? { v: version } : {})
    const loaded = loadedSrc === src
    return (
      <span className="relative block shrink-0" style={{ width: size, height: size }}>
        <img
          // Complete at mount (the webview's cache): shown before the first paint.
          ref={(el) => {
            if (el && !loaded && el.complete && el.naturalWidth > 0) setLoadedSrc(src)
          }}
          src={src}
          alt=""
          width={size}
          height={size}
          decoding="async"
          draggable={false}
          onLoad={() => setLoadedSrc(src)}
          onError={fail}
          className="block h-full w-full rounded-full bg-hover object-cover"
        />
        {loaded ? null : <span className="absolute inset-0">{account}</span>}
      </span>
    )
  }
  return account
}
