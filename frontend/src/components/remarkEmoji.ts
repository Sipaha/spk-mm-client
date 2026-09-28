// A remark plugin: ":name:" shortcodes in text become <span data-emoji="name">
// (rendered by Markdown.tsx via EmojiGlyph). Like remarkMentions, it only
// walks 'text' mdast nodes and never recurses into link/linkReference
// nodes or code (inlineCode/code have no 'children', so a plain child-type
// check already excludes them) — so a shortcode inside `code`, a fenced
// block or a link's URL/label is left untouched. Whether a name actually
// resolves to a known emoji is decided at render time (EmojiGlyph/its
// caller), not here — matches the webapp's own split (Emoticons.tsx always
// tokenizes ":name:", PostEmoji decides known vs not).

interface MdNode {
  type: string
  value?: string
  children?: MdNode[]
  data?: Record<string, unknown>
}

export type Part = { text: string } | { emoji: string; raw: string }

// Mattermost emoji names: lowercase letters, digits, '_', '+', '-'
// (mm-10.11 webapp/channels/src/utils/emoticons.tsx EMOJI_PATTERN). No
// boundary requirement before/after — "(:smile:)" and adjacent ":a::b:"
// both convert.
const EMOJI = /:([a-z0-9_+-]+):/g

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

function walk(node: MdNode) {
  if (!node.children || node.type === 'link' || node.type === 'linkReference') return
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
// multi-paragraph text, since any of those would break the match). It does
// not check that every name actually resolves to a known emoji — the
// webapp's version effectively does, since an unresolved shortcode's
// PostEmoji falls back to bare text and breaks its own span/img regex; here
// that would only be caught by resolving each name at render time, which
// jumbo sizing intentionally does not depend on (documented simplification,
// see report).
const EMOJI_ONLY = /^(?:\s*:[a-z0-9_+-]+:\s*)+$/

export function isEmojiOnlyText(text: string): boolean {
  return text.trim().length > 0 && EMOJI_ONLY.test(text)
}
