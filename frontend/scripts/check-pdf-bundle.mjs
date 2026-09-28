#!/usr/bin/env node
// Bundle check for the PDF preview (plan.md ruling: "nothing may block
// startup" — pdf.js is not in the initial chunk). Run after `vite build`
// (wired into the `build` script in package.json) so it inspects the real
// dist output, not source: PdfView.tsx is dynamically imported by
// Viewer.tsx, so pdf.js and its worker must land only in their own lazy
// chunks, never in the entry script the page loads up front.
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import path from 'node:path'

const frontendDir = path.dirname(path.dirname(fileURLToPath(import.meta.url)))
const distDir = path.join(frontendDir, 'dist')

const indexHtmlPath = path.join(distDir, 'index.html')
const indexHtml = readFileSync(indexHtmlPath, 'utf8')
const match = indexHtml.match(/<script[^>]*\stype="module"[^>]*\ssrc="([^"]+)"/)
if (!match) {
  console.error(`check-pdf-bundle: no <script type="module" src="..."> entry found in ${indexHtmlPath}`)
  process.exit(1)
}

const entryRelPath = match[1].replace(/^\//, '')
const entryPath = path.join(distDir, entryRelPath)
const entrySource = readFileSync(entryPath, 'utf8')

const forbidden = [/pdfjs/i, /GlobalWorkerOptions/]
const hits = forbidden.filter((re) => re.test(entrySource))
if (hits.length > 0) {
  console.error(`check-pdf-bundle: the initial chunk (${entryRelPath}) contains pdf.js (matched ${hits.map(String).join(', ')}).`)
  console.error('pdf.js must load only from PdfView.tsx\'s lazy chunk (React.lazy in Viewer.tsx), never up front.')
  process.exit(1)
}

console.log(`check-pdf-bundle: OK — ${entryRelPath} (${(entrySource.length / 1024).toFixed(1)} kB) has no pdf.js`)
