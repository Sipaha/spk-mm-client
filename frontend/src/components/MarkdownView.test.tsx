import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import type { FileView } from '../api/types'
import { setLocale } from '../i18n'
import { MarkdownView } from './MarkdownView'

const readme: FileView = { id: 'f-readme', name: 'README.md', ext: 'md', size: 400, mime: 'text/markdown' }

beforeEach(() => setLocale('en'))
afterEach(() => vi.unstubAllGlobals())

test('requests the text with ?full=1, not the feed-fragment URL', async () => {
  const fetchMock = vi.fn<(url: string) => Promise<Response>>(async () => new Response('# Title\n'))
  vi.stubGlobal('fetch', fetchMock)
  render(<MarkdownView serverId={1} file={readme} me="alice" onLink={vi.fn()} />)
  await screen.findByRole('heading', { name: 'Title' })
  expect(fetchMock.mock.calls[0][0]).toBe('/media/1/text/f-readme?full=1')
})

test('renders heading, list, code and table; a remote image is a link, not an <img>', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => new Response('# Title\n\n- one\n- two\n\n```go\nfmt.Println("hi")\n```\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n![pic](https://example.com/x.png)')),
  )
  const onLink = vi.fn()
  const { container } = render(<MarkdownView serverId={1} file={readme} me="alice" onLink={onLink} />)
  expect(await screen.findByRole('heading', { name: 'Title' })).toBeInTheDocument()
  expect(screen.getAllByRole('listitem')).toHaveLength(2)
  expect(container.querySelector('code')).toBeInTheDocument()
  expect(container.querySelector('table')).toBeInTheDocument()
  expect(container.querySelector('img')).toBeNull()
  await userEvent.click(screen.getByRole('link', { name: /pic/ }))
  expect(onLink).toHaveBeenCalledWith('https://example.com/x.png')
})

test('the panel is opaque, like TextView: rounded, fills and scrolls the pane', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response('# Title\n')))
  const { container } = render(<MarkdownView serverId={1} file={readme} me="alice" onLink={vi.fn()} />)
  await screen.findByRole('heading', { name: 'Title' })
  const panel = container.firstElementChild!
  expect(panel).toHaveClass('bg-panel')
  expect(panel).toHaveClass('rounded')
  expect(panel).toHaveClass('h-full')
  expect(panel).toHaveClass('w-full')
  expect(panel).toHaveClass('overflow-auto')
})

// .md pre and .md code (index.css) paint fenced/inline code with
// --color-code-bg; if the panel used the same token (bg-code-bg), a code
// block would be invisible against it (round 2 regression). The panel must
// carry a different background token/class than the collision one.
test('the panel background is a different token from .md pre/code, so a code block still stands out', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response('```go\nfmt.Println("hi")\n```\n')))
  const { container } = render(<MarkdownView serverId={1} file={readme} me="alice" onLink={vi.fn()} />)
  await screen.findByText(/fmt\.Println/)
  const panel = container.firstElementChild!
  expect(container.querySelector('pre')).toBeInTheDocument()
  expect(panel).not.toHaveClass('bg-code-bg')
  expect(panel).toHaveClass('bg-panel')
})

test('the truncation notice uses the 1 MB i18n key', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response('hello', { headers: { 'X-Truncated': '1' } })))
  render(<MarkdownView serverId={1} file={readme} me="alice" onLink={vi.fn()} />)
  expect(await screen.findByText('Showing the first 1 MB')).toBeInTheDocument()
})

test('a load failure shows the not-found message, not a crash', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response('', { status: 415 })))
  render(<MarkdownView serverId={1} file={readme} me="alice" onLink={vi.fn()} />)
  expect(await screen.findByText('File not found')).toBeInTheDocument()
})
