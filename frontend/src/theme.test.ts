// The app ships dark by default (task 14b) with no theme setting. If one of
// these hard-coded light-neutral classes reappears anywhere in src, some
// future change re-introduced a light patch instead of using the semantic
// tokens defined in index.css (bg-app, bg-panel, text-fg, ...).
const LIGHT_BG = /\bbg-white\b|\bbg-neutral-50\b|\bbg-neutral-100\b|\bbg-neutral-200\b|\bbg-amber-50\b|\bbg-amber-100\b|\bbg-blue-50\b|\bbg-red-50\b/

test('no hard-coded light background classes remain in src', () => {
  const files = import.meta.glob('./**/*.{ts,tsx}', { eager: true, query: '?raw', import: 'default' }) as Record<string, string>
  const offenders = Object.entries(files)
    .filter(([path]) => !path.endsWith('.test.ts') && !path.endsWith('.test.tsx'))
    .filter(([, text]) => LIGHT_BG.test(text))
    .map(([path]) => path)
  expect(offenders).toEqual([])
})
