import { Call, Events } from '@wailsio/runtime'
import { afterEach, expect, test, vi } from 'vitest'
import { ApiError, wailsClient } from './client'

const FQN = 'github.com/spk/spk-mm-client/internal/api/transport.API.'

// The module-level @wailsio/runtime mock (frontend/vitest.setup.ts) provides
// bare vi.fn()s for Call.ByName / Events.On; these tests drive them directly
// to exercise the desktop transport (wailsClient), which the http/browser
// tests in client.test.ts don't touch at all.
afterEach(() => {
  vi.mocked(Call.ByName).mockReset()
  vi.mocked(Events.On).mockReset()
})

test('calls are addressed by FQN with positional args', async () => {
  vi.mocked(Call.ByName).mockResolvedValue({ id: 1 })
  await wailsClient.addServer('u')
  expect(Call.ByName).toHaveBeenCalledWith(FQN + 'AddServer', 'u')

  vi.mocked(Call.ByName).mockResolvedValue({ id: 1, signed_in: true })
  await wailsClient.loginWithPassword(1, 'alice', 'secret')
  expect(Call.ByName).toHaveBeenCalledWith(FQN + 'LoginWithPassword', 1, 'alice', 'secret')
})

test('a rejection is parsed into ApiError', async () => {
  vi.mocked(Call.ByName).mockRejectedValue(new Error('bad_credentials: HTTP 401'))
  await expect(wailsClient.listServers()).rejects.toEqual(new ApiError('bad_credentials', 'HTTP 401'))
})

test('subscribeEvents registers all event types and unwraps both payload shapes', () => {
  const offs = [vi.fn(), vi.fn(), vi.fn(), vi.fn(), vi.fn(), vi.fn()]
  let call = 0
  vi.mocked(Events.On).mockImplementation(() => offs[call++])

  const onEvent = vi.fn()
  const unsubscribe = wailsClient.subscribeEvents(onEvent)

  expect(Events.On).toHaveBeenCalledTimes(6)
  const registeredNames = vi.mocked(Events.On).mock.calls.map((c) => c[0])
  expect(registeredNames).toEqual([
    'servers_changed',
    'login_failed',
    'open_external',
    'sidebar_changed',
    'channel_changed',
    'open_channel',
  ])

  const [serversChangedCb, loginFailedCb, openExternalCb] = vi
    .mocked(Events.On)
    .mock.calls.map((c) => c[1] as (ev: { data: unknown }) => void)

  // data = payload directly
  serversChangedCb({ data: { code: 'bad_credentials' } })
  expect(onEvent).toHaveBeenCalledWith({ type: 'servers_changed', payload: { code: 'bad_credentials' } })

  // data = [payload] (1-element array)
  loginFailedCb({ data: [{ code: 'bad_credentials' }] })
  expect(onEvent).toHaveBeenCalledWith({ type: 'login_failed', payload: { code: 'bad_credentials' } })

  // undefined payload
  openExternalCb({ data: undefined })
  expect(onEvent).toHaveBeenCalledWith({ type: 'open_external', payload: undefined })

  unsubscribe()
  for (const off of offs) expect(off).toHaveBeenCalledTimes(1)
})
