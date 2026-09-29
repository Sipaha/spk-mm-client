import type { FileView } from '../api/types'

export type FileKind = 'image' | 'video' | 'audio' | 'text' | 'markdown' | 'pdf' | 'other'

// PDF_MAX must equal internal/media.PDFMax (Task 1, Go): over the cap the
// file is just a download card, never handed to pdf.js.
export const PDF_MAX = 50 * 1024 * 1024

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

export const extOf = (f: FileView) => (f.ext || (f.name.includes('.') ? f.name.slice(f.name.lastIndexOf('.') + 1) : f.name)).toLowerCase()

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
  if ((ext === 'pdf' || mime === 'application/pdf') && f.size <= PDF_MAX) return 'pdf'
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

// CardType: FileCard's icon/colour bucket (file-cards brief, 2026-09-30) —
// finer-grained than FileKind for anything that lands in a generic card
// (FileKind's 'other'), so a .docx and a .zip don't look the same. pdf/
// image/video/audio/text ride on FileKind's own routing (a card only shows
// them as a fallback — a failed image/media/text load); document/
// spreadsheet/presentation/archive are extension/mime buckets of their
// own, matching the webapp's getFileType/ICON_NAME_FROM_TYPE split
// (mm-10.11 webapp/channels/src/utils/{utils,constants}.tsx) plus an
// 'archive' bucket the webapp itself doesn't have (it lumps zip/tar into
// its generic "other" — this project's cards call it out explicitly, per
// the brief).
export type CardType = 'pdf' | 'image' | 'video' | 'audio' | 'text' | 'document' | 'spreadsheet' | 'presentation' | 'archive' | 'generic'

const DOCUMENT_EXT = new Set(['doc', 'docx', 'rtf', 'odt', 'pages'])
const SPREADSHEET_EXT = new Set(['xls', 'xlsx', 'ods', 'numbers'])
const PRESENTATION_EXT = new Set(['ppt', 'pptx', 'odp', 'key'])
const ARCHIVE_EXT = new Set(['zip', 'rar', '7z', 'tar', 'gz', 'tgz', 'bz2', 'tbz2', 'xz'])

export function cardType(f: FileView): CardType {
  const kind = fileKind(f)
  if (kind === 'pdf' || kind === 'image' || kind === 'video' || kind === 'audio') return kind
  if (kind === 'text' || kind === 'markdown') return 'text'
  const ext = extOf(f)
  const mime = (f.mime || '').toLowerCase()
  if (DOCUMENT_EXT.has(ext) || mime.includes('wordprocessingml') || mime === 'application/msword' || mime.includes('opendocument.text')) return 'document'
  if (SPREADSHEET_EXT.has(ext) || mime.includes('spreadsheetml') || mime === 'application/vnd.ms-excel' || mime.includes('opendocument.spreadsheet')) return 'spreadsheet'
  if (PRESENTATION_EXT.has(ext) || mime.includes('presentationml') || mime === 'application/vnd.ms-powerpoint' || mime.includes('opendocument.presentation')) return 'presentation'
  if (ARCHIVE_EXT.has(ext) || mime.includes('zip') || mime.includes('x-rar') || mime.includes('x-7z') || mime.includes('x-tar') || mime.includes('gzip')) return 'archive'
  return 'generic'
}

// cardTypeLabel: the card's meta line's type word ("PDF", "ZIP") — the
// extension uppercased, like the webapp's own fileInfo.extension.toUpperCase()
// (file_attachment.tsx), falling back to the MIME subtype when there is no
// extension left (e.g. stripped on upload).
export function cardTypeLabel(f: FileView): string {
  // Not extOf(): that falls back to the *whole filename* when there is no
  // dot (so 'Makefile' matches TEXT_EXT's 'makefile' entry) — exactly
  // wrong for a user-facing label ("SPEC 2KB" for a dot-less "spec" would
  // be nonsense). Here, no real extension means no extension label at all;
  // MIME is the only other source of truth.
  const realExt = f.ext || (f.name.includes('.') ? f.name.slice(f.name.lastIndexOf('.') + 1).toLowerCase() : '')
  if (realExt) return realExt.toUpperCase()
  const subtype = (f.mime || '').split('/')[1]
  return subtype ? subtype.toUpperCase() : ''
}

// cardSizeLabel: the card's meta line's size word ("34KB") — the webapp's
// own fileSizeToString (utils/utils.tsx): whole-number KB/MB/GB/TB (one
// decimal only under 10 units), no space before the unit. Deliberately not
// format.ts's formatSize (used everywhere else in this app — download
// tooltips, toasts): the brief calls for matching the official client's
// own card wording specifically ("PDF 34KB"), not our general size format.
export function cardSizeLabel(bytes: number): string {
  const unit = (n: number, u: string) => (bytes < n * 10 ? Math.round((bytes / n) * 10) / 10 : Math.round(bytes / n)) + u
  if (bytes > 1024 ** 4) return unit(1024 ** 4, 'TB')
  if (bytes > 1024 ** 3) return unit(1024 ** 3, 'GB')
  if (bytes > 1024 ** 2) return unit(1024 ** 2, 'MB')
  if (bytes > 1024) return Math.round(bytes / 1024) + 'KB'
  return bytes + 'B'
}

// fitBox: the box an image is shown in — known before it loads, so the
// feed never shifts. Unknown dimensions get a fixed box (object-contain).
export function fitBox(w: number | undefined, h: number | undefined, maxW: number, maxH: number) {
  if (!w || !h) return { width: Math.min(240, maxW), height: Math.min(180, maxH) }
  const s = Math.min(1, maxW / w, maxH / h)
  return { width: Math.max(1, Math.round(w * s)), height: Math.max(1, Math.round(h * s)) }
}
