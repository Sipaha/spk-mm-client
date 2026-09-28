import type { PostView } from '../api/types'
import { mediaURL, useLoadFailure } from '../media'
import { Avatar } from './Avatar'
import { EmojiGlyph } from './EmojiGlyph'

interface Props {
  serverId: number
  post: Pick<PostView, 'id' | 'user_id' | 'author' | 'avatar' | 'status' | 'icon'>
  size: number
}

// PostAvatar is the picture next to a post: a webhook's own icon when Go
// says so (PostView.icon) — its picture from /media/<srv>/posticon/<post
// id> (by post, never by URL: Go fetches it) or an emoji — otherwise the
// account's avatar, which is also what a failed icon falls back to. Eager
// like Avatar (see there).
export function PostAvatar({ serverId, post, size }: Props) {
  const [failed, fail] = useLoadFailure(serverId, post.id)
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
  if (icon === 'post' && !failed) {
    return (
      <span className="relative block shrink-0" style={{ width: size, height: size }}>
        <img
          src={mediaURL(serverId, 'posticon', post.id)}
          alt=""
          width={size}
          height={size}
          decoding="async"
          draggable={false}
          onError={fail}
          className="block h-full w-full rounded-full bg-hover object-cover"
        />
      </span>
    )
  }
  return <Avatar serverId={serverId} userId={post.user_id} version={post.avatar} name={post.author} status={post.status} size={size} surface="app" />
}
