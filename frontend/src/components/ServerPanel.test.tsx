import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import type { Client } from '../api/client'
import { ApiError } from '../api/client'
import { setLocale } from '../i18n'
import { ServerPanel } from './ServerPanel'

const base = {
  id: 1, name: 'Acme', url: 'https://mm.acme', signed_in: false, username: '', gitlab: true,
  state: 'off' as const, unread: false, mentions: 0,
}

function fakeClient(over: Partial<Client> = {}): Client {
  return {
    listServers: vi.fn(),
    addServer: vi.fn(),
    removeServer: vi.fn().mockResolvedValue(undefined),
    startGitLabLogin: vi.fn().mockResolvedValue(undefined),
    loginWithPassword: vi.fn().mockResolvedValue({ ...base, signed_in: true, username: 'alice' }),
    logout: vi.fn().mockResolvedValue(undefined),
    subscribeEvents: vi.fn(),
    jumpToPost: vi.fn(),
    searchPosts: vi.fn().mockResolvedValue({ hits: [], has_next: false, limit_reached: false }),
    searchSuggest: vi.fn().mockResolvedValue({ users: [], others: [], channels: [], emoji: [], commands: [] }),
    loadNewer: vi.fn().mockResolvedValue(undefined),
    retryRevalidation: vi.fn().mockResolvedValue(undefined),
    openThreadAt: vi.fn(),
    loadThreadFocus: vi.fn().mockResolvedValue(undefined),
    retryThreadRevalidation: vi.fn().mockResolvedValue(undefined),
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

test('GitLab waiting hint resets when the login fails', async () => {
  const c = fakeClient()
  const { rerender } = render(<ServerPanel server={base} client={c} loginFailures={0} />)
  await userEvent.click(screen.getByRole('button', { name: 'Sign in with GitLab' }))
  expect(screen.getByText('Finish signing in in the browser window')).toBeInTheDocument()
  rerender(<ServerPanel server={base} client={c} loginFailures={1} />)
  expect(screen.queryByText('Finish signing in in the browser window')).toBeNull()
})

test('GitLab waiting hint resets when the server signs in', async () => {
  const c = fakeClient()
  const { rerender } = render(<ServerPanel server={base} client={c} loginFailures={0} />)
  await userEvent.click(screen.getByRole('button', { name: 'Sign in with GitLab' }))
  rerender(<ServerPanel server={{ ...base, signed_in: true, username: 'alice' }} client={c} loginFailures={0} />)
  rerender(<ServerPanel server={base} client={c} loginFailures={0} />) // signed out again
  expect(screen.queryByText('Finish signing in in the browser window')).toBeNull()
})

test('password sign-in is disabled while in flight (no double submit)', async () => {
  let resolve!: (v: unknown) => void
  const c = fakeClient({ loginWithPassword: vi.fn(() => new Promise((r) => { resolve = r })) as Client['loginWithPassword'] })
  render(<ServerPanel server={base} client={c} />)
  await userEvent.type(screen.getByLabelText('Login or email'), 'alice')
  await userEvent.type(screen.getByLabelText('Password'), 'secret')
  const btn = screen.getByRole('button', { name: 'Sign in' })
  await userEvent.click(btn)
  expect(btn).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Remove server' })).toBeDisabled()
  await userEvent.click(btn)
  expect(c.loginWithPassword).toHaveBeenCalledTimes(1)
  await act(async () => resolve({ ...base, signed_in: true, username: 'alice' }))
  expect(btn).toBeEnabled()
})

test('sign-out and remove are disabled while in flight', async () => {
  let resolve!: () => void
  const c = fakeClient({ logout: vi.fn(() => new Promise<void>((r) => { resolve = r })) })
  render(<ServerPanel server={{ ...base, signed_in: true, username: 'alice' }} client={c} />)
  const out = screen.getByRole('button', { name: 'Sign out' })
  await userEvent.click(out)
  expect(out).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Remove server' })).toBeDisabled()
  await userEvent.click(out)
  expect(c.logout).toHaveBeenCalledTimes(1)
  await act(async () => resolve())
  expect(out).toBeEnabled()
})

test('re-auth mode shows the form over a signed-in server and can go back', async () => {
  const onCancel = vi.fn()
  render(<ServerPanel server={{ ...base, signed_in: true, username: 'alice', state: 'needs_reauth' }} client={fakeClient()} reauth onCancel={onCancel} />)
  expect(screen.getByText('Your session on this server expired — sign in again')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Sign in' })).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Back' }))
  expect(onCancel).toHaveBeenCalled()
})
