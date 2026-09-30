import { act, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import type { FileView, ServerState } from '../api/types'
import { client } from '../api/client'
import { setLocale } from '../i18n'
import { resetStreamBaseForTests } from '../media'
import { useStore } from '../store'
import { MediaPlayer } from './MediaPlayer'

const video: FileView = { id: 'f-clip', name: 'clip.webm', ext: 'webm', size: 43538, mime: 'video/webm' }
const audio: FileView = { id: 'f-tone', name: 'tone.ogg', ext: 'ogg', size: 9736, mime: 'audio/ogg' }
const handlers = () => ({ onView: vi.fn(), onDownload: vi.fn(), onOpen: vi.fn() })

beforeEach(() => {
  setLocale('en')
  // media.ts caches a successfully resolved stream base for the page's
  // life (by design — Task 7); within one test file that would let an
  // earlier test's success silently survive into a later test that wants
  // to see a fresh (or rejected) MediaStreamBase() call.
  resetStreamBaseForTests()
  vi.spyOn(client, 'mediaStreamBase').mockResolvedValue('/media')
})
afterEach(() => vi.restoreAllMocks())

test('video: fixed box before load, preload="none", poster placeholder; clicking it calls play() synchronously inside the click', async () => {
  const h = handlers()
  const play = vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue(undefined)
  const { container } = render(<MediaPlayer serverId={1} file={video} kind="video" {...h} />)
  const el = container.querySelector('video')!
  expect(el).toHaveAttribute('preload', 'none')
  expect(el).not.toHaveAttribute('controls')
  // Fixed box up to the load — the feed must not jump once the stream resolves.
  // (up to the column's width: it scales down with its aspect ratio kept).
  expect(el.parentElement).toHaveStyle({ width: '480px', maxWidth: '100%', aspectRatio: '480 / 270' })
  expect(el.parentElement!.style.height).toBe('')
  const poster = screen.getByRole('button', { name: 'Play clip.webm' })
  expect(poster).toHaveTextContent('clip.webm')
  expect(poster).toHaveTextContent('42.5 KB')

  // The URL (module-level cached base + this file's stream path) is
  // fetched on mount, not on click — by the time of a real click it has
  // almost always already resolved.
  await vi.waitFor(() => expect(el).toHaveAttribute('src', '/media/1/stream/f-clip'))

  // fireEvent.click is synchronous (unlike userEvent.click, which awaits
  // internally): asserting play() right after, with no await in between,
  // proves it ran inside the click's own call stack — Task 8 fix round 1,
  // WebKitGTK (spike S6) silently refuses playback started from a later
  // effect/microtask instead of the gesture itself.
  fireEvent.click(poster)
  expect(play).toHaveBeenCalledTimes(1)
  expect(screen.queryByRole('button', { name: 'Play clip.webm' })).toBeNull()
  expect(el).toHaveAttribute('controls')
})

test("video: if the URL isn't resolved yet at click time, native controls appear but nothing auto-plays once it arrives (no gesture-less playback)", async () => {
  const h = handlers()
  let resolveBase!: (v: string) => void
  vi.mocked(client.mediaStreamBase).mockReturnValueOnce(
    new Promise<string>((resolve) => {
      resolveBase = resolve
    }),
  )
  const play = vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue(undefined)
  const { container } = render(<MediaPlayer serverId={1} file={video} kind="video" {...h} />)
  const poster = screen.getByRole('button', { name: 'Play clip.webm' })
  fireEvent.click(poster)
  expect(play).not.toHaveBeenCalled() // nothing to play yet
  expect(screen.queryByRole('button', { name: 'Play clip.webm' })).toBeNull() // controls shown instead, ready for the user's own next click
  await act(async () => {
    resolveBase('/media')
    await Promise.resolve()
  })
  await vi.waitFor(() => expect(container.querySelector('video')).toHaveAttribute('src', '/media/1/stream/f-clip'))
  expect(play).not.toHaveBeenCalled() // still not auto-played — no gesture accompanied the URL's arrival
})

test('video: a small ⤢ opens the viewer, independent of the play button', async () => {
  const h = handlers()
  render(<MediaPlayer serverId={1} file={video} kind="video" {...h} />)
  await userEvent.click(screen.getByRole('button', { name: 'View clip.webm' }))
  expect(h.onView).toHaveBeenCalledWith(video)
  expect(h.onDownload).not.toHaveBeenCalled()
})

test('video: the element\'s error event falls back to a card', async () => {
  const h = handlers()
  const { container } = render(<MediaPlayer serverId={1} file={video} kind="video" {...h} />)
  fireEvent.error(container.querySelector('video')!)
  expect(container.querySelector('video')).toBeNull()
  expect(screen.getByText('clip.webm')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Download clip.webm' }))
  expect(h.onDownload).toHaveBeenCalledWith(video)
})

test('audio: compact controls, preload="none", no poster', async () => {
  const h = handlers()
  const { container } = render(<MediaPlayer serverId={1} file={audio} kind="audio" {...h} />)
  const el = container.querySelector('audio')!
  expect(el).toHaveAttribute('preload', 'none')
  expect(el).toHaveAttribute('controls')
  expect(screen.queryByRole('button', { name: /Play/ })).toBeNull()
  expect(screen.getByText('tone.ogg')).toBeInTheDocument()
  await vi.waitFor(() => expect(el).toHaveAttribute('src', '/media/1/stream/f-tone'))
})

test('audio: the element\'s error event falls back to a card', () => {
  const h = handlers()
  const { container } = render(<MediaPlayer serverId={1} file={audio} kind="audio" {...h} />)
  fireEvent.error(container.querySelector('audio')!)
  expect(container.querySelector('audio')).toBeNull()
  expect(screen.getByText('tone.ogg')).toBeInTheDocument()
})

test('big (viewer) video: a bare element, no poster/box chrome — native controls only', async () => {
  const h = handlers()
  const { container } = render(<MediaPlayer serverId={1} file={video} kind="video" big {...h} />)
  const el = container.querySelector('video')!
  expect(el).toHaveAttribute('controls')
  expect(el).toHaveAttribute('preload', 'none')
  expect(screen.queryByRole('button', { name: /Play/ })).toBeNull()
  expect(screen.queryByRole('button', { name: /View/ })).toBeNull()
})

test('only one player plays at a time: starting one pauses the other', () => {
  const h = handlers()
  const { container } = render(
    <>
      <MediaPlayer serverId={1} file={video} kind="video" big {...h} />
      <MediaPlayer serverId={1} file={audio} kind="audio" big {...h} />
    </>,
  )
  const [v, a] = [container.querySelector('video')!, container.querySelector('audio')!]
  const pauseV = vi.spyOn(v, 'pause')
  const pauseA = vi.spyOn(a, 'pause')
  fireEvent.play(v)
  expect(pauseA).toHaveBeenCalledTimes(1)
  expect(pauseV).not.toHaveBeenCalled() // playing itself doesn't self-pause
  pauseA.mockClear()
  fireEvent.play(a)
  expect(pauseV).toHaveBeenCalledTimes(1)
})

test('unmounting releases the element: paused, src dropped, reloaded (Task 13b)', () => {
  const h = handlers()
  const pause = vi.spyOn(HTMLMediaElement.prototype, 'pause')
  const load = vi.spyOn(HTMLMediaElement.prototype, 'load')
  const { container, unmount } = render(<MediaPlayer serverId={1} file={video} kind="video" big {...h} />)
  const el = container.querySelector('video')!
  el.setAttribute('src', '/media/1/stream/f-clip')
  unmount()
  expect(pause).toHaveBeenCalled()
  expect(el.hasAttribute('src')).toBe(false)
  expect(load).toHaveBeenCalled()
})

test("a stream failure releases the element right away, not just on unmount (it's swapped for a card, not unmounted as a whole)", () => {
  const h = handlers()
  const pause = vi.spyOn(HTMLMediaElement.prototype, 'pause')
  const load = vi.spyOn(HTMLMediaElement.prototype, 'load')
  const { container } = render(<MediaPlayer serverId={1} file={video} kind="video" {...h} />)
  const el = container.querySelector('video')!
  el.setAttribute('src', '/media/1/stream/f-clip')
  fireEvent.error(el)
  expect(container.querySelector('video')).toBeNull() // swapped for the card
  expect(pause).toHaveBeenCalled()
  expect(el.hasAttribute('src')).toBe(false)
  expect(load).toHaveBeenCalled()
})

const goes = (state: ServerState) =>
  act(() => useStore.getState().setServers([{ id: 1, name: 'A', url: 'https://a', signed_in: true, username: 'a', gitlab: false, state, unread: false, mentions: 0 }]))

test('a failed video is tried again once the server goes live again', async () => {
  const h = handlers()
  goes('reconnecting')
  const { container } = render(<MediaPlayer serverId={1} file={video} kind="video" {...h} />)
  fireEvent.error(container.querySelector('video')!)
  expect(container.querySelector('video')).toBeNull()
  goes('live')
  expect(container.querySelector('video')).not.toBeNull()
})

test('a rejecting MediaStreamBase falls back to a card, the same as a media error — not an inert poster forever', async () => {
  const h = handlers()
  vi.mocked(client.mediaStreamBase).mockRejectedValueOnce(new Error('no loopback yet'))
  const { container } = render(<MediaPlayer serverId={1} file={video} kind="video" {...h} />)
  await vi.waitFor(() => expect(container.querySelector('video')).toBeNull())
  expect(screen.getByText('clip.webm')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Download clip.webm' }))
  expect(h.onDownload).toHaveBeenCalledWith(video)
})

test('a rejecting MediaStreamBase is retried once the server goes live again', async () => {
  const h = handlers()
  vi.mocked(client.mediaStreamBase).mockRejectedValueOnce(new Error('no loopback yet'))
  goes('reconnecting')
  const { container } = render(<MediaPlayer serverId={1} file={video} kind="video" {...h} />)
  await vi.waitFor(() => expect(container.querySelector('video')).toBeNull())
  vi.mocked(client.mediaStreamBase).mockResolvedValue('/media')
  goes('live')
  await vi.waitFor(() => expect(container.querySelector('video')).not.toBeNull())
})
