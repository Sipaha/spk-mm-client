// A remark plugin: the words a search found, in a result's markdown, become
// <mark> (spec «Поиск», Секция 3 «Подсветка»). It works on the text nodes of
// the tree — never on HTML or URLs: a link's visible text is highlighted,
// its href untouched; code (inline and blocks) is left alone. Word
// boundaries are Unicode letters/digits/_ (the webapp's \b without the u
// flag never matches Cyrillic), case-insensitive; a trailing '*' is a
// prefix; a phrase (a term with spaces, from "…") is marked as a whole
// where the post has it, else its words are (Bleve does not search
// phrases — ruling R7). Runs last, after mentions/channels/emoji: marking
// first would split "@bob" before the mention plugin sees it.

interface MdNode {
  type: string
  value?: string
  children?: MdNode[]
  data?: Record<string, unknown>
}

const B = '(?<![\\p{L}\\p{N}_])'
const E = '(?![\\p{L}\\p{N}_])'
const esc = (s: string) => s.replace(/[.*+?^${}()|[\]\\/]/g, '\\$&') // u-mode: no identity escapes beyond these

function termSource(term: string): string {
  if (/\s/.test(term)) return B + term.split(/\s+/).map(esc).join('\\s+') + E
  if (term.endsWith('*')) return B + esc(term.slice(0, -1)) + '[\\p{L}\\p{N}_]*' // the whole word
  return B + esc(term) + E
}

// SKIP: nodes whose text is not prose — or already a mark.
const SKIP = new Set(['code', 'inlineCode', 'html', 'emoji', 'highlight'])

// collect: the post's text nodes (a phrase counts only within one node —
// that is where walk can mark it).
function collect(node: MdNode, out: string[]) {
  if (SKIP.has(node.type)) return
  if (node.type === 'text' && node.value) out.push(node.value)
  for (const c of node.children ?? []) collect(c, out)
}

// patternFor: one regex over every term of this post, longer terms first
// (a phrase wins over its own words).
function patternFor(terms: string[], texts: string[]): RegExp | null {
  const sources: { len: number; src: string }[] = []
  for (const term of terms) {
    if (/\s/.test(term) && !texts.some((t) => new RegExp(termSource(term), 'iu').test(t))) {
      for (const w of term.split(/\s+/)) if (w) sources.push({ len: w.length, src: termSource(w) })
      continue
    }
    sources.push({ len: term.length, src: termSource(term) })
  }
  if (sources.length === 0) return null
  sources.sort((a, b) => b.len - a.len)
  return new RegExp(sources.map((s) => s.src).join('|'), 'giu')
}

function split(value: string, re: RegExp): MdNode[] | null {
  const out: MdNode[] = []
  let last = 0
  for (const m of value.matchAll(re)) {
    if (m[0] === '') continue
    if (m.index > last) out.push({ type: 'text', value: value.slice(last, m.index) })
    out.push({ type: 'highlight', data: { hName: 'mark' }, children: [{ type: 'text', value: m[0] }] })
    last = m.index + m[0].length
  }
  if (out.length === 0) return null
  if (last < value.length) out.push({ type: 'text', value: value.slice(last) })
  return out
}

function walk(node: MdNode, re: RegExp) {
  if (SKIP.has(node.type) || !node.children) return
  const out: MdNode[] = []
  for (const child of node.children) {
    if (child.type === 'text' && child.value) {
      const parts = split(child.value, re)
      if (parts) {
        out.push(...parts)
        continue
      }
    } else {
      walk(child, re)
    }
    out.push(child)
  }
  node.children = out
}

export function remarkHighlight(terms: string[]) {
  return (tree: MdNode) => {
    if (terms.length === 0) return
    const text: string[] = []
    collect(tree, text)
    const re = patternFor(terms, text)
    if (re) walk(tree, re)
  }
}

// hitTerms: what to highlight in one hit — the words the server matched in
// that very post (Elasticsearch fills matches; Bleve/SQL leave it empty),
// else the session's terms.
export function hitTerms(hit: { matches?: string[] | null }, terms: string[]): string[] {
  return hit.matches && hit.matches.length > 0 ? hit.matches : terms
}
