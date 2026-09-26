import type { FileView } from '../api/types'

export type FileKind = 'image' | 'text' | 'other'

const RASTER = new Set(['image/png', 'image/jpeg', 'image/gif', 'image/webp', 'image/bmp'])

// Text files previewed inline. Markdown is not here on purpose: rendered
// .md previews are in the backlog; until then .md is a card.
export const TEXT_EXT = new Set([
  'txt', 'log', 'csv', 'tsv', 'json', 'yaml', 'yml', 'xml', 'toml', 'ini', 'conf', 'cfg', 'env', 'properties', 'sql',
  'sh', 'bash', 'zsh', 'ps1', 'bat', 'go', 'py', 'js', 'mjs', 'cjs', 'ts', 'tsx', 'jsx', 'java', 'kt', 'kts', 'gradle',
  'groovy', 'scala', 'c', 'h', 'cc', 'cpp', 'hpp', 'cs', 'rs', 'rb', 'php', 'swift', 'lua', 'pl', 'r', 'css', 'scss',
  'less', 'html', 'htm', 'vue', 'svelte', 'diff', 'patch', 'proto', 'graphql', 'tf', 'hcl', 'dockerfile', 'makefile',
  'gitignore', 'editorconfig',
])

export const GIF_INLINE_MAX = 8 * 1024 * 1024
export const IMAGE_FILE_MAX = 25 * 1024 * 1024

const extOf = (f: FileView) => (f.ext || (f.name.includes('.') ? f.name.slice(f.name.lastIndexOf('.') + 1) : f.name)).toLowerCase()

// imageSrc: what the feed and the viewer load for an image — the server
// preview, or the original when there is none (GIF keeps its animation;
// small images the server made no preview for). null: shown as a card.
export function imageSrc(f: FileView): 'preview' | 'file' | null {
  const mime = (f.mime || '').toLowerCase()
  if (!mime.startsWith('image/') || mime === 'image/svg+xml') return null
  if (mime === 'image/gif') return f.size <= GIF_INLINE_MAX ? 'file' : null
  if (f.has_preview) return 'preview'
  return RASTER.has(mime) && f.size <= IMAGE_FILE_MAX ? 'file' : null
}

// imageOriginalOk: whether the viewer may request the original
// (mediaURL(..., 'full', id, { src: 'file' })) regardless of whether a
// preview exists — same size rule as imageSrc's 'file' branch (GIF against
// GIF_INLINE_MAX, everything else against IMAGE_FILE_MAX), but not gated on
// has_preview: the viewer wants the original even when a preview exists,
// the preview there is only ever a placeholder until it loads.
export function imageOriginalOk(f: FileView): boolean {
  const mime = (f.mime || '').toLowerCase()
  if (!RASTER.has(mime)) return false
  if (mime === 'image/gif') return f.size <= GIF_INLINE_MAX
  return f.size <= IMAGE_FILE_MAX
}

export function fileKind(f: FileView): FileKind {
  if (imageSrc(f)) return 'image'
  const ext = extOf(f)
  const mime = (f.mime || '').toLowerCase()
  if (ext === 'md' || ext === 'markdown' || mime === 'text/markdown') return 'other'
  if (TEXT_EXT.has(ext) || mime === 'text/plain') return 'text'
  return 'other'
}

// fitBox: the box an image is shown in — known before it loads, so the
// feed never shifts. Unknown dimensions get a fixed box (object-contain).
export function fitBox(w: number | undefined, h: number | undefined, maxW: number, maxH: number) {
  if (!w || !h) return { width: Math.min(240, maxW), height: Math.min(180, maxH) }
  const s = Math.min(1, maxW / w, maxH / h)
  return { width: Math.max(1, Math.round(w * s)), height: Math.max(1, Math.round(h * s)) }
}
