import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import type { FileView } from '../api/types'
import { setLocale } from '../i18n'
import { MarkdownSnippet } from './MarkdownSnippet'

const readme: FileView = { id: 'f-readme', name: 'README.md', ext: 'md', size: 400, mime: 'text/markdown' }
const handlers = () => ({ onView: vi.fn(), onDownload: vi.fn(), onOpen: vi.fn() })

beforeEach(() => setLocale('en'))
afterEach(() => vi.unstubAllGlobals())

function stubFetch(text: string) {
  const fetchMock = vi.fn<(url: string) => Promise<Response>>(async () => new Response(text))
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

test('renders a heading, a list and a code block; fixed height until expanded', async () => {
  stubFetch('# Title\n\n- one\n- two\n\n```go\nfmt.Println("hi")\n```\n')
  const h = handlers()
  const { container } = render(<MarkdownSnippet serverId={1} file={readme} me="alice" onLink={vi.fn()} {...h} />)
  expect(await screen.findByRole('heading', { name: 'Title' })).toBeInTheDocument()
  expect(screen.getAllByRole('listitem')).toHaveLength(2)
  expect(container.querySelector('code')).toBeInTheDocument()
  expect(container.querySelector('.md')).toHaveClass('md-preview')
  const box = container.querySelector('figure > div')!
  expect(box).toHaveClass('px-3', 'py-2')
  expect(box).toHaveClass('h-40')
  await userEvent.click(screen.getByRole('button', { name: 'Expand' }))
  expect(box).toHaveClass('max-h-96')
})

// .md pre and .md code (index.css) paint fenced/inline code with
// --color-code-bg; the snippet's own figure must not share that token, or a
// code block would be invisible against it (round 2 regression).
test('the figure background is a different token from .md pre/code, so a code block still stands out', async () => {
  stubFetch('```go\nfmt.Println("hi")\n```\n')
  const { container } = render(<MarkdownSnippet serverId={1} file={readme} me="alice" onLink={vi.fn()} {...handlers()} />)
  await screen.findByText(/fmt\.Println/)
  const figure = container.querySelector('figure')!
  expect(container.querySelector('pre')).toBeInTheDocument()
  expect(figure).not.toHaveClass('bg-code-bg')
  expect(figure).toHaveClass('bg-panel')
})

test('a remote image becomes a link, never an <img>; the link opens the system browser', async () => {
  stubFetch('![diagram](https://example.com/diagram.png) and a [link](https://example.com)')
  const onLink = vi.fn()
  const { container } = render(<MarkdownSnippet serverId={1} file={readme} me="alice" onLink={onLink} {...handlers()} />)
  await screen.findByRole('link', { name: /diagram/ })
  expect(container.querySelector('img')).toBeNull()
  await userEvent.click(screen.getByRole('link', { name: 'link' }))
  expect(onLink).toHaveBeenCalledWith('https://example.com')
})

test('a load failure (404/413/415 from /media/) falls back to a card', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response('', { status: 415 })))
  const h = handlers()
  render(<MarkdownSnippet serverId={1} file={readme} me="alice" onLink={vi.fn()} {...h} />)
  await waitFor(() => expect(screen.getByText('README.md')).toBeInTheDocument())
  await userEvent.click(screen.getByRole('button', { name: 'Download README.md' }))
  expect(h.onDownload).toHaveBeenCalledWith(readme)
  expect(screen.getByRole('button', { name: 'Open README.md' })).toBeInTheDocument()
})

test('view/download/open call their handlers', async () => {
  stubFetch('body text')
  const h = handlers()
  render(<MarkdownSnippet serverId={1} file={readme} me="alice" onLink={vi.fn()} {...h} />)
  await screen.findByText('body text')
  await userEvent.click(screen.getByRole('button', { name: 'View README.md' }))
  expect(h.onView).toHaveBeenCalledWith(readme)
  await userEvent.click(screen.getByRole('button', { name: 'Open README.md' }))
  expect(h.onOpen).toHaveBeenCalledWith(readme)
})
