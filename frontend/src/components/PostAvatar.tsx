import { useCallback, useState } from 'react'
import type { PostView } from '../api/types'
import { mediaURL, useLoadFailure } from '../media'
import { Avatar } from './Avatar'
import { EmojiGlyph } from './EmojiGlyph'
import { IconInfo, IconWebhook } from './icons'

interface Props {
  serverId: number
  post: Pick<PostView, 'id' | 'user_id' | 'author' | 'avatar' | 'status' | 'icon' | 'icon_version' | 'system_author'>
  size: number
}

// PostAvatar is the picture next to a post. Go decides (PostView.icon):
// a webhook's own icon — its picture from /media/<srv>/posticon/<post id>
// (by post, never by URL: Go fetches it) or an emoji; "webhook" — a webhook
// post without an icon of its own; otherwise the account's avatar. A webhook
// post never shows its owner's avatar (it would read as if the owner had
// written it — the webapp shows its generic webhook logo too): the generic
// webhook icon is what it shows without an icon, while its icon loads and
// when that fails. Eager like Avatar (see there).
//
// Until the icon has loaded the generic one stands in over it: a new post is
// a new /media URL, and one whose picture Go has not fetched yet (a webhook's
// first icon from an external host takes a round trip there; a broken one
// fails only after it) would otherwise be an empty circle for that long. An
// icon already in the webview's cache is complete at mount: no stand-in, no
// flash.
export function PostAvatar({ serverId, post, size }: Props) {
  // A failure is remembered per icon version: an edit that changes the
  // icon tries the new one.
  const version = post.icon_version ?? ''
  const [failed, fail] = useLoadFailure(serverId, `${post.id}|${version}`)
  const [loadedSrc, setLoadedSrc] = useState('')
  const src = mediaURL(serverId, 'posticon', post.id, version ? { v: version } : {})
  const loaded = loadedSrc === src
  // Complete at mount (the webview's cache): shown before the first paint.
  // Only success is checked here: a failed /media answer is never cached
  // (no Cache-Control), so its error event always comes, after mount.
  const cached = useCallback(
    (el: HTMLImageElement | null) => {
      if (el && !loaded && el.complete && el.naturalWidth > 0) setLoadedSrc(src)
    },
    [src, loaded],
  )
  const icon = post.icon ?? ''
  // The server's own ephemeral answer: "System", never the user's picture.
  if (post.system_author) {
    return (
      <span
        data-testid="system-avatar"
        aria-hidden="true"
        className="flex shrink-0 items-center justify-center rounded-full bg-hover text-fg-muted"
        style={{ width: size, height: size }}
      >
        <IconInfo size={Math.round(size * 0.7)} />
      </span>
    )
  }
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
  if (icon === 'post' && !failed) {
    return (
      <span className="relative block shrink-0" style={{ width: size, height: size }}>
        <img
          ref={cached}
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
        {loaded ? null : <WebhookIcon size={size} className="absolute inset-0" />}
      </span>
    )
  }
  if (icon === 'post' || icon === 'webhook') return <WebhookIcon size={size} className="relative" />
  return <Avatar serverId={serverId} userId={post.user_id} version={post.avatar} name={post.author} status={post.status} size={size} surface="app" />
}

// WebhookIcon: the generic picture of a webhook post, on a round tinted
// background of the theme (accent-tinted, like a mention chip).
function WebhookIcon({ size, className }: { size: number; className: string }) {
  return (
    <span
      data-webhook-icon=""
      aria-hidden="true"
      className={`${className} flex shrink-0 items-center justify-center rounded-full bg-accent/20 text-accent`}
      style={{ width: size, height: size }}
    >
      <IconWebhook size={Math.round(size * 0.6)} />
    </span>
  )
}
