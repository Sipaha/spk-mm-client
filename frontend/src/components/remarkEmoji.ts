// A remark plugin: ":name:" shortcodes in text become <span data-emoji="name">
// (rendered by Markdown.tsx via EmojiGlyph). Like remarkMentions, it only
// walks 'text' mdast nodes and never recurses into code (inlineCode/code
// have no 'children', so a plain child-type check already excludes them) or
// a bare-URL autolink (see isAutolink) — so a shortcode inside `code`, a
// fenced block or a link's URL is left untouched. A markdown link's *label*
// text ("[...]" part) is walked like any other inline content — fix round 1
// #3: the webapp's renderer runs doFormatText (which includes emoticon
// handling) on link label text too, only the href itself is passed through
// untouched (mm-10.11 utils/markdown/renderer.tsx's `link`/`text` methods).
// Whether a name actually resolves to a known emoji is decided at render
// time (EmojiGlyph/its caller), not here — matches the webapp's own split
// (Emoticons.tsx always tokenizes ":name:", PostEmoji decides known or not).

interface MdNode {
  type: string
  value?: string
  children?: MdNode[]
  data?: Record<string, unknown>
  url?: string
}

export type Part = { text: string } | { emoji: string; raw: string }

// Mattermost emoji names: letters (either case), digits, '_', '+', '-' — the
// confirmed mm-10.11 pattern (utils/emoticons.tsx EMOJI_PATTERN) and the
// server's own custom-emoji name validation
// (server/public/model/utils.go's validSimpleAlphaNumHyphenUnderscorePlus)
// both allow uppercase; fix round 1 #1 — a lowercase-only class here meant a
// custom emoji named e.g. ":MyEmoji:" could never even be tokenized, let
// alone looked up. Resolution (case handling for the *lookup*) happens at
// render time — see Markdown.tsx. No boundary requirement before/after:
// "(:smile:)" and adjacent ":a::b:" both convert.
const EMOJI = /:([a-zA-Z0-9_+-]+):/g

export function splitEmoji(text: string): Part[] {
  const out: Part[] = []
  let last = 0
  for (const m of text.matchAll(EMOJI)) {
    if (m.index! > last) out.push({ text: text.slice(last, m.index!) })
    out.push({ emoji: m[1], raw: m[0] })
    last = m.index! + m[0].length
  }
  if (last < text.length) out.push({ text: text.slice(last) })
  return out
}

// emojiNames: every ":name:" shortcode text found in raw text, in order,
// case preserved (as typed) — used by Markdown.tsx to decide jumbo sizing
// only once every one of them is confirmed to resolve (fix round 1 #2).
export function emojiNames(text: string): string[] {
  const names: string[] = []
  for (const part of splitEmoji(text)) if ('emoji' in part) names.push(part.emoji)
  return names
}

// isAutolink: GFM's literal-autolink extension (remark-gfm) produces a
// `link` node whose only child is a text node equal to the URL itself — no
// separate label was ever written. An explicit "[label](url)" link's label
// text is independent of its href. We only need to tell these apart to keep
// protecting the "http://x:8080:" case (a bare URL, not a real shortcode)
// while still walking a genuine link label's text for shortcodes.
function isAutolink(node: MdNode): boolean {
  return node.children?.length === 1 && node.children[0].type === 'text' && node.children[0].value === node.url
}

function walk(node: MdNode) {
  if (!node.children) return
  if (node.type === 'link' && isAutolink(node)) return
  const out: MdNode[] = []
  for (const child of node.children) {
    if (child.type === 'text' && child.value?.includes(':')) {
      for (const part of splitEmoji(child.value)) {
        out.push(
          'emoji' in part
            ? {
                type: 'emoji',
                data: { hName: 'span', hProperties: { dataEmoji: part.emoji } },
                children: [{ type: 'text', value: part.raw }],
              }
            : { type: 'text', value: part.text },
        )
      }
    } else {
      walk(child)
      out.push(child)
    }
  }
  node.children = out
}

export function remarkEmoji() {
  return (tree: MdNode) => {
    walk(tree)
  }
}

// isEmojiOnlyText: the webapp's jumbo rule (mm-10.11
// utils/text_formatting.tsx htmlEmojiPattern) makes a message larger when,
// once formatted, it is nothing but emoji spans/images and whitespace in a
// single paragraph — no count cap. We approximate that structurally on the
// raw source before parsing (cheap, single regex pass, matches the "no
// per-render index allocation" performance rule): the whole trimmed text
// must be one or more ":name:" shortcodes separated only by whitespace, with
// nothing else (which also rules out fenced/inline code, links and
// multi-paragraph text, since any of those would break the match). This is
// only the *shape* check — Markdown.tsx additionally requires every matched
// name to actually resolve (standard or known-custom) before applying jumbo
// sizing (fix round 1 #2: a message mixing a resolved and an unresolved
// shortcode must render at normal size, not a partially-enlarged mix).
const EMOJI_ONLY = /^(?:\s*:[a-zA-Z0-9_+-]+:\s*)+$/

export function isEmojiOnlyText(text: string): boolean {
  return text.trim().length > 0 && EMOJI_ONLY.test(text)
}
