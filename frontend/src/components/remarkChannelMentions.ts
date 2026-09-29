// A remark plugin: ~channel-name in text becomes <span data-channel="name">
// — rendered by Markdown.tsx as a link to the channel when the sidebar
// knows that name (the webapp's autolinkChannelMentions: "~Display Name"),
// else as the text it was. Like remarkMentions: text nodes only, never
// code or a link.

interface MdNode {
  type: string
  value?: string
  children?: MdNode[]
  data?: Record<string, unknown>
}

export type Part = { text: string } | { channel: string; raw: string }

// Channel URL names: lowercase letters, digits, '.', '-', '_'. The '~'
// must not follow a word character or another '~' (the webapp's \B~).
const CHANNEL = /(^|[^\p{L}\p{N}_~])~([a-z0-9][a-z0-9._-]*)/giu

export function splitChannels(text: string): Part[] {
  const out: Part[] = []
  let last = 0
  for (const m of text.matchAll(CHANNEL)) {
    const raw = m[2].replace(/\.+$/, '') // "~town-square." at a sentence end
    const at = m.index! + m[1].length
    if (at > last) out.push({ text: text.slice(last, at) })
    out.push({ channel: raw.toLowerCase(), raw })
    last = at + 1 + raw.length
  }
  if (last < text.length) out.push({ text: text.slice(last) })
  return out
}

function walk(node: MdNode) {
  if (!node.children || node.type === 'link' || node.type === 'linkReference') return
  const out: MdNode[] = []
  for (const child of node.children) {
    if (child.type === 'text' && child.value?.includes('~')) {
      for (const part of splitChannels(child.value)) {
        out.push(
          'channel' in part
            ? { type: 'channelMention', data: { hName: 'span', hProperties: { dataChannel: part.channel } }, children: [{ type: 'text', value: '~' + part.raw }] }
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

export function remarkChannelMentions() {
  return (tree: MdNode) => {
    walk(tree)
  }
}
