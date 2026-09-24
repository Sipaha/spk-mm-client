import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import { ApiError } from '../api/client'
import { setLocale } from '../i18n'
import { AddServerForm } from './AddServerForm'

beforeEach(() => setLocale('en'))

test('submits the URL and reports success', async () => {
  const add = vi.fn().mockResolvedValue({ id: 3 })
  const onAdded = vi.fn()
  render(<AddServerForm add={add} onAdded={onAdded} />)
  await userEvent.type(screen.getByLabelText('Server address'), 'mm.example.com')
  await userEvent.click(screen.getByRole('button', { name: 'Add' }))
  expect(add).toHaveBeenCalledWith('mm.example.com')
  expect(onAdded).toHaveBeenCalledWith(3)
})

test('shows a localized error', async () => {
  const add = vi.fn().mockRejectedValue(new ApiError('not_mattermost', ''))
  render(<AddServerForm add={add} onAdded={vi.fn()} />)
  await userEvent.type(screen.getByLabelText('Server address'), 'example.com')
  await userEvent.click(screen.getByRole('button', { name: 'Add' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('This address is not a Mattermost server')
})
