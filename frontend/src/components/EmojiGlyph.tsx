import { emojiChar, useEmojiIndex } from '../emoji'
import { mediaURL, useLoadFailure } from '../media'

// EmojiGlyph draws an emoji by name: a character for standard emoji, the
// server's picture for custom ones (via /media/, by name), ":name:" while
// unknown or if the picture fails. The picture loads eagerly (not
// loading="lazy" — see Avatar).
export function EmojiGlyph({ serverId, name, size = 16 }: { serverId: number; name: string; size?: number }) {
  const common = emojiChar(name, null)
  const idx = useEmojiIndex(!common)
  const [broken, setBroken] = useLoadFailure(serverId, name) // until the server goes live again
  const ch = common ?? emojiChar(name, idx)
  if (ch) return <span aria-hidden="true">{ch}</span>
  if (!idx || broken) return <span aria-hidden="true">:{name}:</span>
  return (
    <img
      src={mediaURL(serverId, 'emoji', name)}
      alt=""
      aria-hidden="true"
      width={size}
      height={size}
      onError={setBroken}
      className="inline-block align-text-bottom"
    />
  )
}
