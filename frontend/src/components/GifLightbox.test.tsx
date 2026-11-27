import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import { setLocale } from '../i18n'
import { GifLightbox } from './GifLightbox'

vi.mock('../chat', () => ({
  openExternalLink: vi.fn(),
}))

const { openExternalLink } = await import('../chat')

beforeEach(() => {
  setLocale('en')
  vi.mocked(openExternalLink).mockReset()
})

test('GifLightbox renders /media/.../postgif, opens external browser on button click, and closes on Escape or backdrop', async () => {
  const onClose = vi.fn()
  const gif = { id: '0123456789abcdef', url: 'https://cdn.example/cat.gif', alt: 'happy cat', width: 320, height: 240 }
  render(<GifLightbox serverId={3} postId="p9" gif={gif} onClose={onClose} />)

  const dialog = screen.getByRole('dialog', { name: 'GIF viewer' })
  expect(dialog).toHaveAttribute('aria-modal', 'true')
  expect(dialog).toHaveAttribute('data-overlay', 'true')
  const img = dialog.querySelector('img')!
  expect(img).toHaveAttribute('src', '/media/3/postgif/p9?k=0123456789abcdef')

  await userEvent.click(screen.getByRole('button', { name: 'Open in browser' }))
  expect(openExternalLink).toHaveBeenCalledWith('https://cdn.example/cat.gif')

  await userEvent.keyboard('{Escape}')
  expect(onClose).toHaveBeenCalledTimes(1)

  await userEvent.click(dialog)
  expect(onClose).toHaveBeenCalledTimes(2)
})
