import { memo, useMemo } from 'react'
import ReactMarkdown, { type Components } from 'react-markdown'
import remarkBreaks from 'remark-breaks'
import remarkGfm from 'remark-gfm'
import type { EmojiDTO } from '../api/types'
import { emojiChar, useEmojiIndex } from '../emoji'
import { useEmojiInfo } from '../emoji/recent'
import { EmojiGlyph } from './EmojiGlyph'
import { IconImage } from './icons'
import { isEmojiOnlyText, remarkEmoji } from './remarkEmoji'
import { remarkMentions } from './remarkMentions'

const plugins = [remarkGfm, remarkBreaks, remarkMentions, remarkEmoji]
const SPECIAL = new Set(['channel', 'here', 'all'])

// MarkdownEmojiNode renders a ":name:" node the remark plugin produced:
// a known standard emoji (frontend/src/emoji, aliases included) or, once
// the server's custom emoji list is known, a known custom one — both via
// the shared EmojiGlyph, at jumbo size when the whole message is emoji
// (see isEmojiOnlyText). An unknown or not-yet-known name stays the literal
// ":name:" text the plugin kept as this node's children — no image is
// requested for it (unlike EmojiGlyph's own reaction/quick-pick callers,
// whose names are already known-valid, arbitrary message text is not).
function MarkdownEmojiNode({
  serverId,
  name,
  jumbo,
  emojiInfo,
  children,
}: {
  serverId: number
  name: string
  jumbo: boolean
  emojiInfo?: () => Promise<EmojiDTO>
  children?: React.ReactNode
}) {
  const common = emojiChar(name, null)
  const idx = useEmojiIndex(!common)
  const standard = !!(common ?? emojiChar(name, idx))
  // Only ask for the (per-server, lazily loaded) custom emoji list once a
  // name isn't already a known standard one — no fetch at all for plain
  // words that happen to sit between two colons.
  const info = useEmojiInfo(serverId, standard ? undefined : emojiInfo)
  const custom = !standard && !!info?.custom_enabled && info.custom.includes(name)
  if (!standard && !custom) return <>{children}</>
  return (
    <span className={jumbo ? 'text-3xl' : undefined}>
      <EmojiGlyph serverId={serverId} name={name} size={jumbo ? 32 : 16} />
    </span>
  )
}

function linkTo(href: string | undefined, onLink: (href: string) => void) {
  return (e: React.MouseEvent) => {
    e.preventDefault()
    if (href) onLink(href)
  }
}

// Markdown renders a message the way Mattermost does, without ever loading
// remote content: images become links, every link opens in the system
// browser, ":name:" shortcodes become emoji (see MarkdownEmojiNode).
// serverId/emojiInfo are only needed to resolve a *custom* emoji shortcode
// (mediaURL, PostActions.emojiInfo) — both optional so a caller that only
// ever shows text with standard shortcodes (or none at all) doesn't need
// a server context; a custom name without them just stays literal text.
export const Markdown = memo(function Markdown({
  text,
  me,
  onLink,
  serverId = 0,
  emojiInfo,
}: {
  text: string
  me: string
  onLink(href: string): void
  serverId?: number
  emojiInfo?: () => Promise<EmojiDTO>
}) {
  // The whole-message jumbo rule (mm-10.11): only ":name:" shortcodes and
  // whitespace, nothing else — see isEmojiOnlyText.
  const jumbo = useMemo(() => isEmojiOnlyText(text), [text])
  const components: Components = {
    a: ({ href, children }) => (
      <a href={href} title={href} className="text-accent hover:underline" onClick={linkTo(href, onLink)}>
        {children}
      </a>
    ),
    img: ({ src, alt }) => {
      const href = typeof src === 'string' ? src : ''
      return (
        <a href={href} title={href} className="inline-flex items-center gap-1 text-accent hover:underline" onClick={linkTo(href, onLink)}>
          <IconImage size={14} />
          {alt || href}
        </a>
      )
    },
    span: (props) => {
      const p = props as Record<string, unknown>
      const emojiName = p['data-emoji']
      if (typeof emojiName === 'string') {
        return (
          <MarkdownEmojiNode serverId={serverId} name={emojiName} jumbo={jumbo} emojiInfo={emojiInfo}>
            {props.children}
          </MarkdownEmojiNode>
        )
      }
      const name = p['data-mention']
      if (typeof name !== 'string') return <span className={props.className}>{props.children}</span>
      const loud = name === me.toLowerCase() || SPECIAL.has(name)
      return (
        <span data-mention={name} className={loud ? 'rounded bg-mention-bg px-0.5 font-medium text-mention-fg' : 'font-medium text-accent'}>
          {props.children}
        </span>
      )
    },
    table: ({ children }) => (
      <div className="overflow-x-auto">
        <table>{children}</table>
      </div>
    ),
  }
  return (
    <div className="md break-words">
      <ReactMarkdown remarkPlugins={plugins} skipHtml components={components}>
        {text}
      </ReactMarkdown>
    </div>
  )
})
