import { act, render, screen } from '@testing-library/react'
import { vi } from 'vitest'
import type { ApiEvent } from './api/types'
import { setLocale } from './i18n'
import { useStore } from './store'

const h = vi.hoisted(() => ({ emit: null as ((e: ApiEvent) => void) | null }))

vi.mock('./api/client', async (orig) => {
  const real = await orig<typeof import('./api/client')>()
  return {
    ...real,
    client: {
      ...real.httpClient,
      listServers: vi.fn().mockResolvedValue([]),
      subscribeEvents: (fn: (e: ApiEvent) => void) => {
        h.emit = fn
        return () => {}
      },
    },
  }
})

const { App } = await import('./App')

beforeEach(() => {
  setLocale('en')
  useStore.setState({ servers: [], selectedId: null, lastError: null })
})

test('servers_changed clears a stale login error', async () => {
  render(<App />)
  await act(async () => h.emit!({ type: 'login_failed', payload: { code: 'no_pending_login' } }))
  expect(screen.getByRole('alert')).toBeInTheDocument()
  await act(async () => h.emit!({ type: 'servers_changed' }))
  expect(screen.queryByRole('alert')).toBeNull()
})
