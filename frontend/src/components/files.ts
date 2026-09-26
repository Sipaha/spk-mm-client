import type { FileView } from '../api/types'

export type FileKind = 'image' | 'video' | 'audio' | 'text' | 'markdown' | 'other'

const RASTER = new Set(['image/png', 'image/jpeg', 'image/gif', 'image/webp', 'image/bmp'])

// Text files previewed inline as plain text. Markdown is not here on
// purpose: .md/.markdown get their own 'markdown' kind (rendered by
// MarkdownSnippet/MarkdownView), not the plain-text snippet.
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

// Video/audio extensions and MIME types the UI offers to stream: this must
// match internal/media's streamTypeByExt/streamTypeByMime allowlist (Task
// 7) — extension first, MIME as a fallback (e.g. no extension left after
// upload). Something let through here that the webview's GStreamer can't
// actually decode still fails cleanly at the element's `error` event
// (falls back to a card), same as an image the server can't preview.
const STREAM_KIND_BY_EXT: Record<string, 'video' | 'audio'> = {
  mp4: 'video', m4v: 'video', webm: 'video', mov: 'video', ogv: 'video', mkv: 'video',
  mp3: 'audio', ogg: 'audio', oga: 'audio', opus: 'audio', wav: 'audio', flac: 'audio', m4a: 'audio', aac: 'audio',
}
const STREAM_KIND_BY_MIME: Record<string, 'video' | 'audio'> = {
  'video/mp4': 'video', 'video/x-m4v': 'video', 'video/webm': 'video',
  'video/quicktime': 'video', 'video/ogg': 'video', 'video/x-matroska': 'video',
  'audio/mpeg': 'audio', 'audio/mp3': 'audio', 'audio/ogg': 'audio', 'audio/opus': 'audio',
  'audio/wav': 'audio', 'audio/x-wav': 'audio', 'audio/wave': 'audio', 'audio/flac': 'audio',
  'audio/x-flac': 'audio', 'audio/mp4': 'audio', 'audio/x-m4a': 'audio', 'audio/aac': 'audio', 'audio/webm': 'audio',
}

function streamKind(f: FileView): 'video' | 'audio' | undefined {
  const ext = extOf(f)
  if (STREAM_KIND_BY_EXT[ext]) return STREAM_KIND_BY_EXT[ext]
  const mime = (f.mime || '').toLowerCase().split(';')[0].trim()
  return STREAM_KIND_BY_MIME[mime]
}

export function fileKind(f: FileView): FileKind {
  if (imageSrc(f)) return 'image'
  const sk = streamKind(f)
  if (sk) return sk
  const ext = extOf(f)
  const mime = (f.mime || '').toLowerCase()
  if (ext === 'md' || ext === 'markdown' || mime === 'text/markdown') return 'markdown'
  if (TEXT_EXT.has(ext) || mime === 'text/plain') return 'text'
  return 'other'
}

// VIDEO_BOX: the feed's fixed video frame — the file's own width/height
// (Task 8), scaled to fit, or this 16:9 default when they're unknown (the
// fake's seeded clips have none) so the feed never jumps once the file
// actually loads.
export const VIDEO_BOX = { w: 480, h: 270 }

export function videoBox(f: FileView): { width: number; height: number } {
  if (f.width && f.height) return fitBox(f.width, f.height, VIDEO_BOX.w, VIDEO_BOX.h)
  return { width: VIDEO_BOX.w, height: VIDEO_BOX.h }
}

// fitBox: the box an image is shown in — known before it loads, so the
// feed never shifts. Unknown dimensions get a fixed box (object-contain).
export function fitBox(w: number | undefined, h: number | undefined, maxW: number, maxH: number) {
  if (!w || !h) return { width: Math.min(240, maxW), height: Math.min(180, maxH) }
  const s = Math.min(1, maxW / w, maxH / h)
  return { width: Math.max(1, Math.round(w * s)), height: Math.max(1, Math.round(h * s)) }
}
