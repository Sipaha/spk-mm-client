interface MdNode {
  type: string
  value?: string
  children?: MdNode[]
  data?: Record<string, unknown>
}

const SKIP = new Set(['code', 'inlineCode', 'html', 'find'])

function walk(node: MdNode, needle: string) {
  if (SKIP.has(node.type) || !node.children) return
  const out: MdNode[] = []
  for (const child of node.children) {
    if (child.type === 'text' && child.value) {
      const value = child.value
      const lower = value.toLocaleLowerCase()
      let from = 0
      let found = false
      for (;;) {
        const at = lower.indexOf(needle, from)
        if (at < 0) break
        found = true
        if (at > from) out.push({ type: 'text', value: value.slice(from, at) })
        out.push({ type: 'find', data: { hName: 'mark', hProperties: { 'data-file-find': 'true' } }, children: [{ type: 'text', value: value.slice(at, at + needle.length) }] })
        from = at + needle.length
      }
      if (found) {
        if (from < value.length) out.push({ type: 'text', value: value.slice(from) })
        continue
      }
    } else {
      walk(child, needle)
    }
    out.push(child)
  }
  node.children = out
}

export function remarkFind(query: string) {
  return (tree: MdNode) => {
    const needle = query.trim().toLocaleLowerCase()
    if (needle) walk(tree, needle)
  }
}
