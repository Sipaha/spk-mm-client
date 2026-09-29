#!/usr/bin/env node
// Bundle check for the PDF preview (plan.md ruling: "nothing may block
// startup" — pdf.js is not in the initial chunk). Run after `vite build`
// (wired into the `build` script in package.json) so it inspects the real
// dist output, not source: PdfView.tsx is dynamically imported by
// Viewer.tsx, so pdf.js and its worker must land only in their own lazy
// chunks, never in the entry script or any other chunk the page loads up
// front (final review M7: the original version scanned only the entry
// script — a shared chunk statically imported by something else would have
// passed).
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import path from 'node:path'

const frontendDir = path.dirname(path.dirname(fileURLToPath(import.meta.url)))
const distDir = path.join(frontendDir, 'dist')
const assetsDir = path.join(distDir, 'assets')

const indexHtmlPath = path.join(distDir, 'index.html')
const indexHtml = readFileSync(indexHtmlPath, 'utf8')
const match = indexHtml.match(/<script[^>]*\stype="module"[^>]*\ssrc="([^"]+)"/)
if (!match) {
  console.error(`check-pdf-bundle: no <script type="module" src="..."> entry found in ${indexHtmlPath}`)
  process.exit(1)
}
const entryRelPath = match[1].replace(/^\//, '')

// PdfView's own chunk and the pdf.js worker are the only places pdf.js may
// live — everything else in dist/assets (the entry, and every other lazy
// chunk: EmojiPicker, data, ...) must have none of it.
const isPdfViewOwn = (name) => /^PdfView-.*\.(js|css)$/.test(name) || /^pdf\.worker.*\.mjs$/.test(name)
const jsChunks = readdirSync(assetsDir).filter((f) => f.endsWith('.js') && !isPdfViewOwn(f))
if (jsChunks.length === 0) {
  console.error(`check-pdf-bundle: found no non-PdfView .js chunks under ${assetsDir} — is dist stale/empty?`)
  process.exit(1)
}

const forbidden = [/pdfjs/i, /GlobalWorkerOptions/]
const offenders = []
for (const chunk of jsChunks) {
  const source = readFileSync(path.join(assetsDir, chunk), 'utf8')
  const hits = forbidden.filter((re) => re.test(source))
  if (hits.length > 0) offenders.push({ chunk, hits })
}
if (offenders.length > 0) {
  for (const { chunk, hits } of offenders) {
    console.error(`check-pdf-bundle: ${chunk} contains pdf.js (matched ${hits.map(String).join(', ')}).`)
  }
  console.error("pdf.js must load only from PdfView.tsx's own lazy chunk (React.lazy in Viewer.tsx), never from any other chunk.")
  process.exit(1)
}

// The optional wasm/cmaps/standard_fonts assets (final review I3) are
// copied by vite.config.ts's copyPdfjsAssets plugin, at build time, from
// the pinned pdfjs-dist — never bundled as JS, never in the initial chunk
// (PdfView.tsx references them only as runtime URL strings, '/pdfjs/...').
// Sanity-check the copy actually ran, and that the scripting sandbox
// (quickjs-eval, the one pdf.js asset that must never ship) is nowhere in
// dist.
const pdfjsAssetsDir = path.join(distDir, 'pdfjs')
const requiredAssets = [
  ['wasm/openjpeg.wasm', 'jpx'],
  ['wasm/jbig2.wasm', 'jbig2/ccitt'],
  ['wasm/qcms_bg.wasm', 'icc'],
]
const missing = requiredAssets.filter(([rel]) => !statSync(path.join(pdfjsAssetsDir, rel), { throwIfNoEntry: false }))
if (missing.length > 0) {
  console.error(`check-pdf-bundle: missing pdf.js asset(s) under ${pdfjsAssetsDir}: ${missing.map(([rel]) => rel).join(', ')}`)
  console.error('vite.config.ts\'s copyPdfjsAssets plugin should have copied these from pdfjs-dist at build time.')
  process.exit(1)
}
const dirSize = (dir) =>
  readdirSync(dir, { recursive: true }).reduce((sum, f) => {
    const st = statSync(path.join(dir, f))
    return st.isFile() ? sum + st.size : sum
  }, 0)
const cmapsSize = dirSize(path.join(pdfjsAssetsDir, 'cmaps'))
const fontsSize = dirSize(path.join(pdfjsAssetsDir, 'standard_fonts'))
const wasmSize = dirSize(path.join(pdfjsAssetsDir, 'wasm'))
const wasmFiles = readdirSync(path.join(pdfjsAssetsDir, 'wasm'))
if (wasmFiles.some((f) => f.includes('quickjs'))) {
  console.error(`check-pdf-bundle: ${pdfjsAssetsDir}/wasm contains quickjs-eval — that is pdf.js's scripting sandbox and must never ship (plan ruling: no PDF scripting).`)
  process.exit(1)
}
const quickjsInDist = readdirSync(distDir, { recursive: true }).filter((f) => f.toLowerCase().includes('quickjs'))
if (quickjsInDist.length > 0) {
  console.error(`check-pdf-bundle: quickjs found in dist: ${quickjsInDist.join(', ')} — pdf.js's scripting sandbox must never ship.`)
  process.exit(1)
}

const entrySize = statSync(path.join(distDir, entryRelPath)).size
const pdfViewJs = readdirSync(assetsDir).find((f) => /^PdfView-.*\.js$/.test(f))
const workerFile = readdirSync(assetsDir).find((f) => /^pdf\.worker.*\.mjs$/.test(f))
const pdfViewSize = pdfViewJs ? statSync(path.join(assetsDir, pdfViewJs)).size : 0
const workerSize = workerFile ? statSync(path.join(assetsDir, workerFile)).size : 0
const kb = (n) => `${(n / 1024).toFixed(1)} kB`

console.log(`check-pdf-bundle: OK — ${jsChunks.length} non-PdfView chunk(s) checked, none reference pdf.js`)
console.log(`  entry ${entryRelPath}: ${kb(entrySize)}`)
console.log(`  PdfView chunk ${pdfViewJs ?? '(not found)'}: ${kb(pdfViewSize)}`)
console.log(`  pdf worker ${workerFile ?? '(not found)'}: ${kb(workerSize)}`)
console.log(`  pdfjs optional assets — cmaps: ${kb(cmapsSize)}, standard_fonts: ${kb(fontsSize)}, wasm: ${kb(wasmSize)} (${wasmFiles.filter((f) => f.endsWith('.wasm')).join(', ')})`)
