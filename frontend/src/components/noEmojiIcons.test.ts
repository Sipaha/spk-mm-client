// Guards the UI pass's Ruling ("Иконки"): any glyph serving as an icon in a
// button/link/chip/list row/badge goes through components/icons.tsx, not a
// raw emoji/dingbat/arrow character. Real emoji (message text, reactions,
// channel-type markers) are unaffected — they live in EmojiGlyph.tsx and
// glyph.ts, neither of which is a .tsx file this glob reaches... actually
// glyph.ts is a .ts file (excluded by the .tsx-only glob below) and
// EmojiGlyph.tsx computes its glyph at runtime (no matching literal in its
// source), so both pass without an explicit allowlist entry.
//
// Ranges (deliberately the same ones the plan's Ruling names, not a wider
// net): U+1F300–U+1FAFF (emoji), U+2190–U+21FF (arrows), U+2300–U+23FF
// (misc technical), U+25A0–U+25FF (geometric shapes), U+2700–U+27BF
// (dingbats).
const ICON_RANGES = /[\u{1F300}-\u{1FAFF}\u{2190}-\u{21FF}\u{2300}-\u{23FF}\u{25A0}-\u{25FF}\u{2700}-\u{27BF}]/u

// Path-based exceptions, each with its reason. Test files intentionally
// keep real emoji in expected strings (reaction/channel-marker assertions)
// and may use a glyph in a descriptive test title; neither is UI source.
const ALLOW: { match(path: string): boolean; reason: string }[] = [
  { match: (p) => p.endsWith('.test.tsx'), reason: 'tests assert on real emoji (reactions, channel markers) and may name a glyph in a title; not UI source' },
]

// Strips /* */ and // comments so only JSX text and string literals remain
// (component sources here have no "//" inside string literals to trip on).
function stripComments(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/\/\/.*$/gm, '')
}

test('no emoji/dingbat/arrow glyph stands in for an icon in components/*.tsx source', () => {
  const files = import.meta.glob('./*.tsx', { eager: true, query: '?raw', import: 'default' }) as Record<string, string>
  const offenders = Object.entries(files)
    .filter(([path]) => !ALLOW.some((a) => a.match(path)))
    .filter(([, text]) => ICON_RANGES.test(stripComments(text)))
    .map(([path]) => path)
  expect(offenders).toEqual([])
})
