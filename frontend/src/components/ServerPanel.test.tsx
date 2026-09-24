import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import type { Client } from '../api/client'
import { ApiError } from '../api/client'
import { setLocale } from '../i18n'
import { ServerPanel } from './ServerPanel'

const base = { id: 1, name: 'Acme', url: 'https://mm.acme', signed_in: false, username: '', gitlab: true }

function fakeClient(over: Partial<Client> = {}): Client {
  return {
    listServers: vi.fn(),
    addServer: vi.fn(),
    removeServer: vi.fn().mockResolvedValue(undefined),
    startGitLabLogin: vi.fn().mockResolvedValue(undefined),
    loginWithPassword: vi.fn().mockResolvedValue({ ...base, signed_in: true, username: 'alice' }),
    logout: vi.fn().mockResolvedValue(undefined),
    subscribeEvents: vi.fn(),
    ...over,
  } as Client
}

beforeEach(() => setLocale('en'))

test('signed-out server offers GitLab and password login', async () => {
  const c = fakeClient()
  render(<ServerPanel server={base} client={c} />)
  await userEvent.click(screen.getByRole('button', { name: 'Sign in with GitLab' }))
  expect(c.startGitLabLogin).toHaveBeenCalledWith(1)
  expect(screen.getByText('Finish signing in in the browser window')).toBeInTheDocument()

  await userEvent.type(screen.getByLabelText('Login or email'), 'alice')
  await userEvent.type(screen.getByLabelText('Password'), 'secret')
  await userEvent.click(screen.getByRole('button', { name: 'Sign in' }))
  expect(c.loginWithPassword).toHaveBeenCalledWith(1, 'alice', 'secret')
})

test('hides GitLab button when disabled on server', () => {
  render(<ServerPanel server={{ ...base, gitlab: false }} client={fakeClient()} />)
  expect(screen.queryByRole('button', { name: 'Sign in with GitLab' })).toBeNull()
})

test('signed-in server shows the user and signs out', async () => {
  const c = fakeClient()
  render(<ServerPanel server={{ ...base, signed_in: true, username: 'alice' }} client={c} />)
  expect(screen.getByText('Signed in as alice')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Sign out' }))
  expect(c.logout).toHaveBeenCalledWith(1)
})

test('wrong password shows localized error', async () => {
  const c = fakeClient({ loginWithPassword: vi.fn().mockRejectedValue(new ApiError('bad_credentials', '')) })
  render(<ServerPanel server={base} client={c} />)
  await userEvent.type(screen.getByLabelText('Login or email'), 'alice')
  await userEvent.type(screen.getByLabelText('Password'), 'x')
  await userEvent.click(screen.getByRole('button', { name: 'Sign in' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('Wrong login or password')
})
