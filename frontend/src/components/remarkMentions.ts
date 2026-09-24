// A remark plugin: @username, @channel, @here, @all in text become
// <span data-mention="name">. Code and link texts are left alone.

interface MdNode {
  type: string
  value?: string
  children?: MdNode[]
  data?: Record<string, unknown>
}

export type Part = { text: string } | { mention: string; raw: string }

// Mattermost usernames: letters, digits, '.', '-', '_'. The character before
// '@' must not be part of a word or an address (bob@example.com).
const MENTION = /(^|[^\p{L}\p{N}_.@-])@([a-z0-9][a-z0-9._-]*)/giu

export function splitMentions(text: string): Part[] {
  const out: Part[] = []
  let last = 0
  for (const m of text.matchAll(MENTION)) {
    const raw = m[2].replace(/\.+$/, '') // "@bob." at the end of a sentence
    const at = m.index! + m[1].length
    if (at > last) out.push({ text: text.slice(last, at) })
    out.push({ mention: raw.toLowerCase(), raw })
    last = at + 1 + raw.length
  }
  if (last < text.length) out.push({ text: text.slice(last) })
  return out
}

function walk(node: MdNode) {
  if (!node.children || node.type === 'link' || node.type === 'linkReference') return
  const out: MdNode[] = []
  for (const child of node.children) {
    if (child.type === 'text' && child.value?.includes('@')) {
      for (const part of splitMentions(child.value)) {
        out.push(
          'mention' in part
            ? {
                type: 'mention',
                data: { hName: 'span', hProperties: { dataMention: part.mention } },
                children: [{ type: 'text', value: '@' + part.raw }],
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

export function remarkMentions() {
  return (tree: MdNode) => {
    walk(tree)
  }
}
