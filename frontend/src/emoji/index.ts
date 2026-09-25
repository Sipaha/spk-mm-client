import { useEffect, useSyncExternalStore } from 'react'

export interface Emoji {
  char: string
  names: string[] // names[0] is what the webapp posts (getEmojiName)
}

export const CATEGORY_IDS = ['smileys', 'people', 'nature', 'food', 'travel', 'activities', 'objects', 'symbols', 'flags'] as const
export type CategoryId = (typeof CATEGORY_IDS)[number]

export interface EmojiIndex {
  categories: { id: CategoryId; emojis: Emoji[] }[]
  byName: Map<string, Emoji>
}

// BUILTIN: the most common reaction names, shown before the set loads.
export const BUILTIN: Record<string, string> = {
  '+1': '👍', thumbsup: '👍', '-1': '👎', thumbsdown: '👎', heart: '❤️', smile: '😄', slightly_smiling_face: '🙂',
  grinning: '😀', laughing: '😆', joy: '😂', wink: '😉', tada: '🎉', eyes: '👀', fire: '🔥', pray: '🙏',
  clap: '👏', ok_hand: '👌', rocket: '🚀', thinking_face: '🤔', white_check_mark: '✅', heavy_check_mark: '✔️',
  x: '❌', '100': '💯', warning: '⚠️', muscle: '💪', wave: '👋', sob: '😭', cry: '😢', raised_hands: '🙌',
}

// buildIndex parses data.ts: one string per category, one line per emoji,
// "<char> <name> <alias>…".
export function buildIndex(raw: readonly string[]): EmojiIndex {
  const byName = new Map<string, Emoji>()
  const categories = raw.map((block, i) => ({
    id: CATEGORY_IDS[i],
    emojis: block
      .split('\n')
      .filter(Boolean)
      .map((line) => {
        const [char, ...names] = line.split(' ')
        const e = { char, names }
        for (const n of names) if (!byName.has(n)) byName.set(n, e)
        return e
      }),
  }))
  return { categories, byName }
}

let index: EmojiIndex | null = null
let loading: Promise<EmojiIndex> | null = null
const listeners = new Set<() => void>()

// loadEmojiIndex loads the set — a lazy chunk (~16 KB gzip) — once.
export function loadEmojiIndex(): Promise<EmojiIndex> {
  loading ??= import('./data').then((m) => {
    index = buildIndex(m.default)
    listeners.forEach((l) => l())
    return index
  })
  return loading
}

function subscribe(l: () => void) {
  listeners.add(l)
  return () => {
    listeners.delete(l)
  }
}

// useEmojiIndex returns the set once loaded and re-renders then;
// want=false only reads it (no load is triggered).
export function useEmojiIndex(want = true): EmojiIndex | null {
  const idx = useSyncExternalStore(subscribe, () => index)
  useEffect(() => {
    if (want && !index) void loadEmojiIndex()
  }, [want])
  return idx
}

const TONES: Record<string, string> = {
  light: '\u{1F3FB}', medium_light: '\u{1F3FC}', medium: '\u{1F3FD}', medium_dark: '\u{1F3FE}', dark: '\u{1F3FF}',
}
const TONE_RE = /^(.+?)_(light|medium_light|medium|medium_dark|dark)_skin_tone$/

function withTone(base: string, tone: string): string {
  const cps = [...base]
  if (cps.includes('‍')) return base // ZWJ sequence: the default tone is close enough
  return cps[0] + tone + cps.slice(1).filter((c) => c !== '️').join('')
}

// emojiChar is the character for a reaction name: aliases (+1, thumbsup)
// and skin tones (+1_light_skin_tone) included. null: not a standard emoji
// — a custom one, or the set is not loaded and the name is not common.
export function emojiChar(name: string, idx: EmojiIndex | null): string | null {
  const e = idx?.byName.get(name)
  if (e) return e.char
  if (BUILTIN[name]) return BUILTIN[name]
  const m = TONE_RE.exec(name)
  if (m) {
    const base = emojiChar(m[1], idx)
    if (base) return withTone(base, TONES[m[2]])
  }
  return null
}

// searchEmoji ranks an exact name first, then names starting with the
// query, then names containing it; the set's order within each rank.
export function searchEmoji(idx: EmojiIndex, q: string, limit = 200): Emoji[] {
  const query = q.trim().toLowerCase()
  if (!query) return []
  const ranked: { rank: number; order: number; e: Emoji }[] = []
  let order = 0
  for (const c of idx.categories) {
    for (const e of c.emojis) {
      let rank = 3
      for (const n of e.names) rank = Math.min(rank, n === query ? 0 : n.startsWith(query) ? 1 : n.includes(query) ? 2 : 3)
      if (rank < 3) ranked.push({ rank, order, e })
      order++
    }
  }
  ranked.sort((a, b) => a.rank - b.rank || a.order - b.order)
  return ranked.slice(0, limit).map((r) => r.e)
}
