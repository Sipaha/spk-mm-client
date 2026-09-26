import type { FileView } from '../api/types'
import { fileKind, fitBox, imageSrc } from './files'

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
  expect(fileKind(F({ name: 'spec.pdf', ext: 'pdf', mime: 'application/pdf' }))).toBe('other')
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
