import { _resetReactorCacheForTests, getCachedReactors, setCachedReactors } from './reactorCache'

beforeEach(() => _resetReactorCacheForTests())

const dto = (n: string) => ({ users: [{ id: 'u1', name: n, avatar: '' }], unknown: 0 })

test('a miss returns undefined', () => {
  expect(getCachedReactors(1, 'p1', '+1', 3)).toBeUndefined()
})

test('hit for the same (server, post, emoji, count)', () => {
  setCachedReactors(1, 'p1', '+1', 3, dto('bob'))
  expect(getCachedReactors(1, 'p1', '+1', 3)).toEqual(dto('bob'))
})

test('a different count misses — a stale entry', () => {
  setCachedReactors(1, 'p1', '+1', 3, dto('bob'))
  expect(getCachedReactors(1, 'p1', '+1', 4)).toBeUndefined()
})

test('a different post or emoji or server misses too', () => {
  setCachedReactors(1, 'p1', '+1', 3, dto('bob'))
  expect(getCachedReactors(1, 'p2', '+1', 3)).toBeUndefined()
  expect(getCachedReactors(1, 'p1', 'tada', 3)).toBeUndefined()
  expect(getCachedReactors(2, 'p1', '+1', 3)).toBeUndefined()
})

test('bounded at 100 entries: the oldest is evicted', () => {
  for (let i = 0; i < 100; i++) setCachedReactors(1, `p${i}`, '+1', 1, dto(`u${i}`))
  setCachedReactors(1, 'p100', '+1', 1, dto('u100')) // 101st entry, nothing read in between
  expect(getCachedReactors(1, 'p0', '+1', 1)).toBeUndefined()
  expect(getCachedReactors(1, 'p1', '+1', 1)).toEqual(dto('u1'))
  expect(getCachedReactors(1, 'p100', '+1', 1)).toEqual(dto('u100'))
})

test('reading an entry refreshes its recency (least-recently-used eviction)', () => {
  for (let i = 0; i < 100; i++) setCachedReactors(1, `p${i}`, '+1', 1, dto(`u${i}`))
  getCachedReactors(1, 'p0', '+1', 1) // touch the oldest — it should survive the next eviction
  setCachedReactors(1, 'p100', '+1', 1, dto('u100'))
  expect(getCachedReactors(1, 'p0', '+1', 1)).toEqual(dto('u0'))
  expect(getCachedReactors(1, 'p1', '+1', 1)).toBeUndefined()
})
