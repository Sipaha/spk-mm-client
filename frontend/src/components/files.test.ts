import type { FileView } from '../api/types'
import { fileKind, fitBox, imageSrc, PDF_MAX, videoBox } from './files'

const F = (o: Partial<FileView>): FileView => ({ id: 'f', name: 'x', size: 100, mime: '', ...o })

test('what kind of preview a file gets', () => {
  expect(fileKind(F({ name: 'a.png', ext: 'png', mime: 'image/png', has_preview: true }))).toBe('image')
  expect(fileKind(F({ name: 'a.svg', ext: 'svg', mime: 'image/svg+xml' }))).toBe('other')
  expect(fileKind(F({ name: 'a.gif', ext: 'gif', mime: 'image/gif', size: 1_000_000 }))).toBe('image')
  expect(fileKind(F({ name: 'a.gif', ext: 'gif', mime: 'image/gif', size: 20_000_000 }))).toBe('other')
  expect(fileKind(F({ name: 'a.tiff', ext: 'tiff', mime: 'image/tiff', has_preview: true }))).toBe('image')
  expect(fileKind(F({ name: 'a.tiff', ext: 'tiff', mime: 'image/tiff' }))).toBe('other')
  expect(fileKind(F({ name: 'server.log', ext: 'log', mime: 'text/plain' }))).toBe('text')
  expect(fileKind(F({ name: 'main.go', ext: 'go', mime: 'application/octet-stream' }))).toBe('text')
  expect(fileKind(F({ name: 'Makefile' }))).toBe('text')
  expect(fileKind(F({ name: 'notes.md', ext: 'md', mime: 'text/markdown' }))).toBe('markdown')
  expect(fileKind(F({ name: 'notes.markdown', ext: 'markdown', mime: 'text/plain' }))).toBe('markdown')
  expect(fileKind(F({ name: 'spec.pdf', ext: 'pdf', mime: 'application/pdf' }))).toBe('pdf')
})

test('pdf kind: by extension or MIME, up to PDF_MAX; over the cap it is just a card', () => {
  expect(fileKind(F({ name: 'spec.pdf', ext: 'pdf', mime: 'application/pdf', size: 1000 }))).toBe('pdf')
  // No extension left (e.g. stripped on upload): MIME alone is enough.
  expect(fileKind(F({ name: 'spec', ext: '', mime: 'application/pdf', size: 1000 }))).toBe('pdf')
  expect(fileKind(F({ name: 'spec.pdf', ext: 'pdf', mime: 'application/pdf', size: PDF_MAX }))).toBe('pdf')
  expect(fileKind(F({ name: 'spec.pdf', ext: 'pdf', mime: 'application/pdf', size: PDF_MAX + 1 }))).toBe('other')
})

test('video/audio kinds, by extension and by MIME (matches internal/media\'s allowlist)', () => {
  expect(fileKind(F({ name: 'clip.webm', ext: 'webm', mime: 'video/webm' }))).toBe('video')
  expect(fileKind(F({ name: 'clip.mp4', ext: 'mp4', mime: 'video/mp4' }))).toBe('video')
  expect(fileKind(F({ name: 'clip.mov', ext: 'mov', mime: 'video/quicktime' }))).toBe('video')
  expect(fileKind(F({ name: 'clip.mkv', ext: 'mkv', mime: 'video/x-matroska' }))).toBe('video')
  expect(fileKind(F({ name: 'tone.ogg', ext: 'ogg', mime: 'audio/ogg' }))).toBe('audio')
  expect(fileKind(F({ name: 'tone.mp3', ext: 'mp3', mime: 'audio/mpeg' }))).toBe('audio')
  expect(fileKind(F({ name: 'tone.flac', ext: 'flac', mime: 'audio/flac' }))).toBe('audio')
  // No extension (e.g. stripped on upload): falls back to MIME.
  expect(fileKind(F({ name: 'clip', ext: '', mime: 'video/webm' }))).toBe('video')
  expect(fileKind(F({ name: 'tone', ext: '', mime: 'audio/wav' }))).toBe('audio')
  // A type outside the streamed allowlist is not video/audio, even with an audio/video-ish MIME.
  expect(fileKind(F({ name: 'clip.avi', ext: 'avi', mime: 'video/x-msvideo' }))).toBe('other')
})

test('videoBox: fits the file\'s own dimensions within 480x270, or falls back to 480x270 (16:9) when unknown', () => {
  expect(videoBox(F({ width: 1920, height: 1080 }))).toEqual({ width: 480, height: 270 })
  expect(videoBox(F({ width: 300, height: 300 }))).toEqual({ width: 270, height: 270 })
  expect(videoBox(F({}))).toEqual({ width: 480, height: 270 })
})

test('image source: preview, the original when there is none, nothing when too big', () => {
  expect(imageSrc(F({ mime: 'image/jpeg', has_preview: true }))).toBe('preview')
  expect(imageSrc(F({ mime: 'image/png', size: 10_000 }))).toBe('file')
  expect(imageSrc(F({ mime: 'image/png', size: 30_000_000 }))).toBeNull()
  expect(imageSrc(F({ mime: 'image/gif', size: 10_000, has_preview: false }))).toBe('file')
  expect(imageSrc(F({ mime: 'image/svg+xml' }))).toBeNull()
})

test('fitBox keeps the aspect inside the box; unknown size gets a fixed box', () => {
  expect(fitBox(1280, 720, 480, 360)).toEqual({ width: 480, height: 270 })
  expect(fitBox(400, 600, 480, 360)).toEqual({ width: 240, height: 360 })
  expect(fitBox(100, 50, 480, 360)).toEqual({ width: 100, height: 50 })
  expect(fitBox(undefined, undefined, 480, 360)).toEqual({ width: 240, height: 180 })
})
