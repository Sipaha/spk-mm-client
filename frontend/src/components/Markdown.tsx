import { memo, useMemo } from 'react'
import ReactMarkdown, { type Components } from 'react-markdown'
import remarkBreaks from 'remark-breaks'
import remarkGfm from 'remark-gfm'
import type { EmojiDTO } from '../api/types'
import { emojiChar, useEmojiIndex, type EmojiIndex } from '../emoji'
import { useEmojiInfo } from '../emoji/recent'
import { EmojiGlyph } from './EmojiGlyph'
import { IconImage } from './icons'
import { emojiNames, isEmojiOnlyText, remarkEmoji } from './remarkEmoji'
import { openChannel } from '../chat'
import { useStore } from '../store'
import { remarkChannelMentions } from './remarkChannelMentions'
import { remarkHighlight } from './remarkHighlight'
import { remarkMentions } from './remarkMentions'

const plugins = [remarkGfm, remarkBreaks, remarkMentions, remarkChannelMentions, remarkEmoji]
const NO_HIGHLIGHT: string[] = []

// ChannelMention: a ~name the selected server's sidebar knows (the team on
// screen, like the webapp's current-team lookup) links to that channel by
// its display name; any other stays the text it was.
function ChannelMention({ serverId, name, children }: { serverId: number; name: string; children?: React.ReactNode }) {
  const ch = useStore((s) => {
    if (s.selectedId !== serverId) return null
    for (const c of s.sidebar?.categories ?? []) for (const it of c.channels ?? []) if (it.slug === name) return it
    return null
  })
  if (!ch) return <>{children}</>
  return (
    <a
      href={`#~${name}`}
      data-channel={name}
      className="text-accent hover:underline"
      onClick={(e) => {
        e.preventDefault()
        void openChannel(serverId, ch.id)
      }}
    >
      ~{ch.name}
    </a>
  )
}
const SPECIAL = new Set(['channel', 'here', 'all'])

// standardChar: a ":name:" shortcode resolves to a standard emoji
// case-insensitively — confirmed against mm-10.11: Emoticons.renderEmoji
// lowercases the name before the emoji-map lookup that decides known vs
// not (utils/emoticons.tsx), so ":White_Check_Mark:" and ":white_check_mark:"
// are the same lookup there. Our own index/BUILTIN are keyed lowercase (by
// convention — system emoji names always are), so lowercasing here is a
// pure widening with no effect on already-lowercase input. A *custom*
// name's lookup (against EmojiDTO.custom) stays case-sensitive — that list
// is exactly the set of real names the server has, verbatim.
function standardChar(name: string, idx: EmojiIndex | null): string | null {
  return emojiChar(name.toLowerCase(), idx)
}

// MarkdownEmojiNode renders a ":name:" node the remark plugin produced:
// a known standard emoji (frontend/src/emoji, aliases included) or, once
// the server's custom emoji list is known, a known custom one — both via
// the shared EmojiGlyph, at jumbo size when the whole message is emoji AND
// every one of its shortcodes resolves (see isEmojiOnlyText and Markdown's
// own jumbo computation below — a mix of resolved/unresolved names must
// never render partially enlarged). An unknown or not-yet-known name stays
// the literal ":name:" text the plugin kept as this node's children — no
// image is requested for it (unlike EmojiGlyph's own reaction/quick-pick
// callers, whose names are already known-valid, arbitrary message text is
// not).
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
  const common = standardChar(name, null)
  const idx = useEmojiIndex(!common)
  const standard = !!(common ?? standardChar(name, idx))
  // Only ask for the (per-server, lazily loaded) custom emoji list once a
  // name isn't already a known standard one — no fetch at all for plain
  // words that happen to sit between two colons.
  const info = useEmojiInfo(serverId, standard ? undefined : emojiInfo)
  const custom = !standard && !!info?.custom_enabled && info.custom.includes(name)
  if (!standard && !custom) return <>{children}</>
  return (
    <span className={jumbo ? 'text-3xl' : undefined}>
      <EmojiGlyph serverId={serverId} name={standard ? name.toLowerCase() : name} size={jumbo ? 32 : 16} />
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
  inline = false,
  highlight = NO_HIGHLIGHT,
}: {
  text: string
  me: string
  onLink(href: string): void
  serverId?: number
  emojiInfo?: () => Promise<EmojiDTO>
  // inline (channel/thread header text, header-markdown-brief 2026-09-30):
  // the caller owns single-line clamping (a `truncate` container with a
  // `title` holding the raw text) — this flag only keeps *us* from ever
  // emitting a block box that would fight it. Every element the standard
  // renderer would otherwise put on its own line (paragraphs, headings,
  // blockquotes, lists, code fences, <hr>) renders as its children only, and
  // a line break — hard (blank line -> new paragraph, both collapse to the
  // same "next paragraph" children-only case) or soft (remarkBreaks' <br>) —
  // becomes a single space instead, so arbitrary multi-line/multi-block
  // input still comes out as one inline run for the container to clamp.
  // Root wraps in <span>, not <div>, so it never breaks its parent's own
  // inline/flex flow.
  inline?: boolean
  // highlight: a search result's terms to <mark> (remarkHighlight — last,
  // after the mention/channel/emoji plugins). Keep the array stable: it
  // keys the plugin list.
  highlight?: string[]
}) {
  const remarkPlugins = useMemo(() => (highlight.length > 0 ? [...plugins, [remarkHighlight, highlight] as const] : plugins), [highlight])
  // The whole-message jumbo rule (mm-10.11): only ":name:" shortcodes and
  // whitespace, AND every one of them resolves (standard or known-custom) —
  // fix round 1 #2. isEmojiOnlyText is just the cheap shape pre-check: most
  // messages aren't shortcode-only text at all, so names/the index/the
  // custom-emoji fetch are only ever touched for the rare message that is.
  // A name not yet confirmed (index still loading, custom list still
  // fetching) simply keeps jumbo off until it resolves, same latency model
  // MarkdownEmojiNode already uses for an individual unresolved name.
  const names = useMemo(() => (isEmojiOnlyText(text) ? emojiNames(text) : []), [text])
  const commonOnly = useMemo(() => names.length > 0 && names.every((n) => !!standardChar(n, null)), [names])
  const jumboIdx = useEmojiIndex(names.length > 0 && !commonOnly)
  const allStandard = useMemo(() => names.length > 0 && names.every((n) => !!standardChar(n, jumboIdx)), [names, jumboIdx])
  const jumboInfo = useEmojiInfo(serverId, names.length > 0 && !allStandard ? emojiInfo : undefined)
  const jumbo = useMemo(
    () => names.length > 0 && names.every((n) => !!standardChar(n, jumboIdx) || (!!jumboInfo?.custom_enabled && jumboInfo.custom.includes(n))),
    [names, jumboIdx, jumboInfo],
  )
  const components: Components = {
    a: ({ href, children }) => (
      <a href={href} title={href} className="text-accent hover:underline" onClick={linkTo(href, onLink)}>
        {children}
      </a>
    ),
    img: ({ src, alt }) => {
      const href = typeof src === 'string' ? src : ''
      return (
        // max-w-full + a wrapping, shrinkable text item: the text is often
        // the bare URL, an unbreakable run whose min-content width an
        // inline-flex box keeps — it pushed the feed scroller sideways once
        // the open thread panel narrowed the feed (2026-09-30).
        <a href={href} title={href} className="inline-flex max-w-full items-center gap-1 text-accent hover:underline" onClick={linkTo(href, onLink)}>
          <IconImage size={14} className="shrink-0" />
          <span className="min-w-0 [overflow-wrap:anywhere]">{alt || href}</span>
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
      const channel = p['data-channel']
      if (typeof channel === 'string') {
        return (
          <ChannelMention serverId={serverId} name={channel}>
            {props.children}
          </ChannelMention>
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
    mark: ({ children }) => <mark className="rounded-sm bg-mention-bg text-mention-fg">{children}</mark>,
    table: ({ children }) => (
      <div className="overflow-x-auto">
        <table>{children}</table>
      </div>
    ),
  }
  // inlineComponents: only the block-level tags need overriding — anything
  // already inline (a/img/span/strong/em/code/...) is left to `components`
  // above. children-only (no wrapping element at all) rather than a <span>
  // per node: a <span> per paragraph/list item is still a valid child of an
  // inline run, but nesting many of them buys nothing here and keeps a flat,
  // easy-to-read DOM for the single line this always ends up as.
  const inlineComponents: Components = {
    p: ({ children }) => <>{children} </>,
    h1: ({ children }) => <>{children} </>,
    h2: ({ children }) => <>{children} </>,
    h3: ({ children }) => <>{children} </>,
    h4: ({ children }) => <>{children} </>,
    h5: ({ children }) => <>{children} </>,
    h6: ({ children }) => <>{children} </>,
    blockquote: ({ children }) => <>{children} </>,
    ul: ({ children }) => <>{children}</>,
    ol: ({ children }) => <>{children}</>,
    li: ({ children }) => <>{children} </>,
    pre: ({ children }) => <>{children}</>,
    hr: () => <>{' '}</>,
    br: () => <>{' '}</>,
  }
  const Wrapper: 'span' | 'div' = inline ? 'span' : 'div'
  return (
    <Wrapper className={inline ? undefined : 'md break-words'}>
      <ReactMarkdown remarkPlugins={remarkPlugins as never} skipHtml components={inline ? { ...components, ...inlineComponents } : components}>
        {text}
      </ReactMarkdown>
    </Wrapper>
  )
})
