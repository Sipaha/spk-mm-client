import fs from 'node:fs'
import path from 'node:path'
import { defineConfig, type Plugin } from 'vitest/config'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// PdfView's optional pdf.js assets (final review I3): scanned/colour-managed
// PDFs need wasm/cmaps/standard_fonts, fetched by pdf.js itself only when a
// document actually needs them — never in the initial bundle, never bundled
// as JS (they're binary/many small files, not modules). Copied straight
// from the pinned pdfjs-dist at build time, under /pdfjs/ at the site root
// (same origin the rest of the app's assets are served from, in both
// browser mode and the Wails desktop asset server — see PdfView.tsx). Never
// quickjs-eval.*: that is pdf.js's scripting sandbox, deliberately not
// shipped (plan ruling: no PDF scripting).
function copyPdfjsAssets(): Plugin {
  return {
    name: 'copy-pdfjs-assets',
    apply: 'build',
    // writeBundle (not generateBundle): runs after Rollup has physically
    // written every chunk to outDir, which is after Vite's own emptyOutDir
    // — copying earlier would risk these files being wiped again.
    writeBundle() {
      const pdfjsDir = path.join(import.meta.dirname, 'node_modules/pdfjs-dist')
      const outDir = path.join(import.meta.dirname, 'dist/pdfjs')
      for (const dir of ['cmaps', 'standard_fonts']) {
        fs.cpSync(path.join(pdfjsDir, dir), path.join(outDir, dir), { recursive: true })
      }
      const wasmOut = path.join(outDir, 'wasm')
      fs.mkdirSync(wasmOut, { recursive: true })
      for (const f of ['openjpeg.wasm', 'jbig2.wasm', 'qcms_bg.wasm']) {
        fs.copyFileSync(path.join(pdfjsDir, 'wasm', f), path.join(wasmOut, f))
      }
    },
  }
}

export default defineConfig({
  plugins: [react(), tailwindcss(), copyPdfjsAssets()],
  build: { outDir: 'dist', emptyOutDir: true },
  test: { environment: 'jsdom', globals: true, setupFiles: ['./vitest.setup.ts'] },
})
