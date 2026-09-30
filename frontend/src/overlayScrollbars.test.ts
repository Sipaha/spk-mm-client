import { readFileSync } from 'node:fs'
import { join } from 'node:path'

// WebKitGTK paints overlay scrollbars above every page layer, so a scroller
// under an open overlay showed its scrollbar on top of it (pdf-lag report,
// 2026-09-30). index.css hides those scrollbars while any [data-overlay] is
// open; every fixed overlay must carry the marker, or the rule never fires.
const css = readFileSync(join(__dirname, 'index.css'), 'utf8')
const src = (f: string) => readFileSync(join(__dirname, 'components', f), 'utf8')

test('index.css hides the scrollbars of scrollers outside an open overlay', () => {
  expect(css).toMatch(
    /body:has\(\[data-overlay\]\) :is\(\.overflow-auto, \.overflow-y-auto, \.overflow-x-auto\):not\(\[data-overlay\] \*\) \{\s*scrollbar-color: transparent transparent;\s*\}/,
  )
})

test.each(['Viewer.tsx', 'ReactorsModal.tsx', 'EmojiPicker.tsx', 'Downloads.tsx', 'PostMenu.tsx', 'FormattingMenu.tsx'])('%s marks its fixed overlay with data-overlay', (f) => {
  expect(src(f)).toMatch(/data-overlay="true"/)
})
