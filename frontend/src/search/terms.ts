// parseSearchTerms: the words of a search query to highlight in its
// results (spec «Поиск», Секция 2/3) — the webapp's parseSearchTerms
// (utils/text_formatting.tsx), with its punctuation trim actually applied:
//   - a "quoted phrase" is one term, as a whole (the highlighter falls back
//     to its words in a post where the phrase itself does not occur —
//     Bleve does not search phrases, ruling R7);
//   - filters (from:, in:, channel:, on:, before:, after:, ext:, also with a
//     space after the colon) and anything negated with '-' are not terms;
//   - @mentions and #hashtags keep their sign; a trailing '*' stays (a
//     prefix); other punctuation around a word goes.
// Case-insensitive duplicates are kept once.

const PHRASE = /^(-?)"([^"]*)"?/
const FILTER = /^-?(?:in|from|channel|on|before|after|ext):[ \t]*[^\s"]*/i
const WORD = /^\S+?(?=\s|"|$)/
const PUNCT_START = /^[^\p{L}\p{N}\s#@]+/u
const PUNCT_END = /[^\p{L}\p{N}\s*]+$/u

export function parseSearchTerms(q: string): string[] {
  const out: string[] = []
  const seen = new Set<string>()
  const add = (term: string) => {
    const t = term.trim()
    if (!t || !/[\p{L}\p{N}]/u.test(t)) return
    const key = t.toLowerCase()
    if (seen.has(key)) return
    seen.add(key)
    out.push(t)
  }
  let rest = q
  while (rest) {
    const lead = /^\s+/.exec(rest)
    if (lead) {
      rest = rest.slice(lead[0].length)
      continue
    }
    let m = PHRASE.exec(rest)
    if (m) {
      rest = rest.slice(m[0].length)
      if (!m[1]) add(m[2].replace(/\s+/g, ' '))
      continue
    }
    m = FILTER.exec(rest)
    if (m) {
      rest = rest.slice(m[0].length)
      continue
    }
    m = WORD.exec(rest)!
    rest = rest.slice(m[0].length)
    if (m[0].startsWith('-')) continue
    // split like the server (SqlPostStore.SearchPosts): <>+()~ separate
    // words; a word's own @ (a mention) is kept at its start.
    for (const part of m[0].split(/[<>+()~]/)) {
      let w = part.replace(PUNCT_START, '')
      if (!w.endsWith('*')) w = w.replace(PUNCT_END, '')
      else w = w.replace(/[^\p{L}\p{N}]+\*$/u, '*')
      if (w === '*') continue
      add(w)
    }
  }
  return out
}
