import type { PostView } from '../api/types'
import { buildRows, firstVisiblePostIndex, type Row } from './feedRows'

const base = new Date(2026, 8, 24, 10, 0).getTime()
const P = (id: string, user: string, min: number, o: Partial<PostView> = {}): PostView => ({
  id, user_id: user, author: user, message: id, create_at: base + min * 60_000, ...o,
})
const ch = (posts: PostView[], o: Partial<{ new_since: number; gap_after: string; has_more: boolean }> = {}) => ({
  posts, new_since: 0, me_id: 'me', gap_after: '', has_more: false, ...o,
})
const shape = (rows: Row[]) => rows.map((r) => (r.kind === 'post' ? `${r.key}${r.head ? '*' : ''}` : r.kind))

test('groups one author within 5 minutes; author, pause, system and day break groups', () => {
  const rows = buildRows(ch([
    P('a1', 'bob', 0), P('a2', 'bob', 3), P('a3', 'bob', 9), P('b1', 'carol', 10),
    P('s1', 'carol', 11, { system: true }), P('b2', 'carol', 12), P('d1', 'carol', 24 * 60),
  ]))
  expect(shape(rows)).toEqual(['day', 'a1*', 'a2', 'a3*', 'b1*', 's1*', 'b2*', 'day', 'd1*'])
})

test('webhook posts of one user with different names are not grouped', () => {
  const rows = buildRows(ch([P('w1', 'hook', 0, { author: 'CI' }), P('w2', 'hook', 1, { author: 'Deploy' })]))
  expect(shape(rows)).toEqual(['day', 'w1*', 'w2*'])
})

test('new-messages line: before the first newer post from someone else, once', () => {
  const rows = buildRows(ch([P('old', 'bob', 0), P('mine', 'me', 2), P('n1', 'bob', 3), P('n2', 'bob', 4)], { new_since: base + 60_000 }))
  expect(shape(rows)).toEqual(['day', 'old*', 'mine*', 'new', 'n1*', 'n2'])
  expect(shape(buildRows(ch([P('x', 'bob', 5)])))).toEqual(['day', 'x*'])
})

test('pending own posts never get the line', () => {
  const rows = buildRows(ch([P('p', 'me', 5, { pending: true })], { new_since: base }))
  expect(shape(rows)).toEqual(['day', 'p*'])
})

test('history row on top, gap row after the last post before the loss', () => {
  const rows = buildRows(ch([P('a', 'bob', 0), P('b', 'bob', 1), P('c', 'bob', 2)], { has_more: true, gap_after: 'b' }))
  expect(shape(rows)).toEqual(['more', 'day', 'a*', 'b', 'gap', 'c*'])
})

test('a pending post and its confirmed replacement share one row key', () => {
  const pendingRows = buildRows(ch([P('u-me:1', 'me', 0, { pending: true, pending_post_id: 'u-me:1' })]))
  const confirmedRows = buildRows(ch([P('real-id-from-server', 'me', 0, { pending_post_id: 'u-me:1' })]))
  expect(pendingRows.find((r) => r.kind === 'post')?.key).toBe('u-me:1')
  expect(confirmedRows.find((r) => r.kind === 'post')?.key).toBe('u-me:1')
})

test('firstVisiblePostIndex skips rows before `from` and non-post rows, unlike scanning from the start', () => {
  const rows = buildRows(ch([P('a', 'bob', 0), P('b', 'bob', 1), P('c', 'bob', 2), P('d', 'bob', 3)]))
  // rows: [day, a*, b, c, d] — indices 0..4
  expect(firstVisiblePostIndex(rows, 0)).toBe(1) // 'a', same as scanning the whole (unoverscanned) list
  expect(firstVisiblePostIndex(rows, 3)).toBe(3) // 'c': must not fall back to 'a' just because it's earlier
  expect(firstVisiblePostIndex(rows, 99)).toBe(-1) // past the end: nothing to anchor on
})
