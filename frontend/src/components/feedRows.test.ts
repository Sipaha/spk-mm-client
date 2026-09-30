import type { HistGap, PostView } from '../api/types'
import { buildRows, type Row } from './feedRows'

const base = new Date(2026, 8, 24, 10, 0).getTime()
const P = (id: string, user: string, min: number, o: Partial<PostView> = {}): PostView => ({
  id, user_id: user, author: user, message: id, create_at: base + min * 60_000, ...o,
})
const ch = (posts: PostView[], o: Partial<{ new_since: number; gap_after: string; has_more: boolean; crt: boolean; gap: HistGap }> = {}) => ({
  posts, new_since: 0, me_id: 'me', gap_after: '', has_more: false, crt: false, ...o,
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

test('a webhook post is never grouped, even under the same name (webapp areConsecutivePostsBySameUser)', () => {
  const rows = buildRows(ch([
    P('w1', 'hook', 0, { author: 'GitLab', webhook: true }), P('w2', 'hook', 1, { author: 'GitLab', webhook: true }),
    P('h1', 'hook', 2), P('h2', 'hook', 3),
  ]))
  expect(shape(rows)).toEqual(['day', 'w1*', 'w2*', 'h1*', 'h2'])
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
  expect(shape(rows)).toEqual(['more', 'day', 'a*', 'b', 'gapAfter', 'c*'])
})

// Spec «Поиск», «Лента (Feed)»: the held history (a segment jumped to, or
// scrolled up to) may not join the window — the gap row sits right before
// the window's first post (before_id), ahead of its day separator.
const gap = (o: Partial<HistGap> = {}): HistGap => ({ open: true, gen: 3, before_id: 'w1', stale: false, ...o })

test('the history gap row goes before the window\'s first post, ahead of its day separator; the next post heads a group', () => {
  const rows = buildRows(ch([P('s1', 'bob', 0), P('s2', 'bob', 1), P('w1', 'bob', 24 * 60), P('w2', 'bob', 24 * 60 + 1)], { gap: gap() }))
  expect(shape(rows)).toEqual(['day', 's1*', 's2', 'gap', 'day', 'w1*', 'w2'])
  const same = buildRows(ch([P('s2', 'bob', 1), P('w1', 'bob', 2)], { gap: gap() }))
  expect(shape(same)).toEqual(['day', 's2*', 'gap', 'w1*']) // same day, same author: still a new group
})

test('an open gap with an empty window (before_id \'\') sits at the end; a closed gap draws no row', () => {
  expect(shape(buildRows(ch([P('s1', 'bob', 0)], { gap: gap({ before_id: '' }) })))).toEqual(['day', 's1*', 'gap'])
  expect(shape(buildRows(ch([P('s1', 'bob', 0), P('w1', 'bob', 1)], { gap: gap({ open: false, before_id: '' }) })))).toEqual(['day', 's1*', 'w1'])
})

test('gap row key is stable while the gap generation holds, and differs from the reconnect row', () => {
  const key = (rows: Row[]) => rows.find((r) => r.kind === 'gap')?.key
  const a = buildRows(ch([P('s1', 'bob', 0), P('w1', 'bob', 1)], { gap: gap() }))
  const b = buildRows(ch([P('s0', 'bob', -1), P('s1', 'bob', 0), P('s2', 'bob', 0.5), P('w1', 'bob', 1)], { gap: gap() }))
  expect(key(a)).toBe('gap:3')
  expect(key(b)).toBe(key(a))
  expect(key(buildRows(ch([P('s1', 'bob', 0), P('w1', 'bob', 1)], { gap: gap({ gen: 4 }) })))).toBe('gap:4')
  const both = buildRows(ch([P('s1', 'bob', 0), P('w1', 'bob', 1), P('w2', 'bob', 2)], { gap: gap(), gap_after: 'w1' }))
  expect(both.filter((r) => r.kind === 'gap' || r.kind === 'gapAfter').map((r) => r.key)).toEqual(['gap:3', 'gap'])
})

test('a stale history shows on the open gap row; a closed stale gap is a thin indicator row at the top of the history', () => {
  const open = buildRows(ch([P('s1', 'bob', 0), P('w1', 'bob', 1)], { gap: gap({ stale: true }) }))
  expect(open.find((r) => r.kind === 'gap')).toMatchObject({ open: true, stale: true })
  const closed = buildRows(ch([P('s1', 'bob', 0), P('w1', 'bob', 1)], { has_more: true, gap: gap({ open: false, before_id: '', stale: true }) }))
  expect(shape(closed)).toEqual(['more', 'gap', 'day', 's1*', 'w1'])
  expect(closed[1]).toMatchObject({ key: 'gap-stale:3', open: false, stale: true })
})

test('new line hidden when new_since falls into the gap', () => {
  // The first newer post from someone else is right after the gap: the true
  // boundary may be among the posts not loaded — no line until they are.
  const into = buildRows(ch([P('s1', 'bob', 0), P('w1', 'bob', 10), P('w2', 'bob', 11)], { gap: gap(), new_since: base + 5 * 60_000 }))
  expect(shape(into)).toEqual(['day', 's1*', 'gap', 'w1*', 'w2'])
  // Own posts after the gap don't make it known either.
  const mine = buildRows(ch([P('s1', 'bob', 0), P('w1', 'me', 10), P('w2', 'bob', 11)], { gap: gap(), new_since: base + 5 * 60_000 }))
  expect(shape(mine).includes('new')).toBe(false)
  // Inside the segment (before the gap) the line is known and drawn.
  const before = buildRows(ch([P('s1', 'bob', 0), P('s2', 'bob', 6), P('w1', 'bob', 10)], { gap: gap(), new_since: base + 5 * 60_000 }))
  expect(shape(before)).toEqual(['day', 's1*', 'new', 's2*', 'gap', 'w1*'])
  // Once the gap closes the line comes back.
  const closed = buildRows(ch([P('s1', 'bob', 0), P('w1', 'bob', 10)], { gap: gap({ open: false }), new_since: base + 5 * 60_000 }))
  expect(shape(closed)).toEqual(['day', 's1*', 'new', 'w1*'])
})

test('a pending post and its confirmed replacement share one row key', () => {
  const pendingRows = buildRows(ch([P('u-me:1', 'me', 0, { pending: true, pending_post_id: 'u-me:1' })]))
  const confirmedRows = buildRows(ch([P('real-id-from-server', 'me', 0, { pending_post_id: 'u-me:1' })]))
  expect(pendingRows.find((r) => r.kind === 'post')?.key).toBe('u-me:1')
  expect(confirmedRows.find((r) => r.kind === 'post')?.key).toBe('u-me:1')
})

// Task 6: without CRT, the first reply of a run to the same root gets the
// "reply to <author>: <snippet>" context line (and is always a head row);
// a run of replies to the same thread shares just one. A reply directly
// under its own root is NOT "first of series" (webapp isFirstReply: the
// previous row must be neither this root nor another reply of the same
// thread) — the root right above already gives the context.
test('replyContext: only the first reply of a run to the same root, without CRT', () => {
  const rows = buildRows(
    ch([
      P('root', 'bob', 0),
      P('r0', 'carol', 1, { root_id: 'root', root_author: 'bob', root_snippet: 'hi there' }), // directly under its root: no context
      P('mid', 'bob', 2), // an unrelated post breaks the run
      P('r1', 'carol', 3, { root_id: 'root', root_author: 'bob', root_snippet: 'hi there' }), // first of a new run: context
      P('r2', 'carol', 4, { root_id: 'root' }), // same run: no context
      P('other', 'bob', 5),
      P('r3', 'carol', 6, { root_id: 'root', root_author: 'bob', root_snippet: 'hi there' }), // 'other' broke it: context again
    ]),
  )
  const post = (id: string) => rows.find((r) => r.kind === 'post' && r.post.id === id) as Extract<Row, { kind: 'post' }>
  expect(post('root').replyContext).toBeUndefined()
  expect(post('r0').replyContext).toBeUndefined()
  expect(post('r1').replyContext).toEqual({ author: 'bob', snippet: 'hi there' })
  expect(post('r1').head).toBe(true)
  expect(post('r2').replyContext).toBeUndefined()
  expect(post('other').replyContext).toBeUndefined()
  expect(post('r3').replyContext).toEqual({ author: 'bob', snippet: 'hi there' })
})

test('replyContext: a reply whose root is not held shows an empty author (UI renders "reply in a thread")', () => {
  const rows = buildRows(ch([P('r1', 'carol', 0, { root_id: 'gone' })]))
  const post = rows.find((r) => r.kind === 'post') as Extract<Row, { kind: 'post' }>
  expect(post.replyContext).toEqual({ author: '', snippet: '' })
})

test('replyContext: never set with CRT on, and never in the thread panel', () => {
  const post = P('r1', 'carol', 0, { root_id: 'root', root_author: 'bob', root_snippet: 'hi' })
  const withCRT = buildRows(ch([P('root', 'bob', 0), post], { crt: true }))
  expect((withCRT.find((r) => r.kind === 'post' && r.post.id === 'r1') as Extract<Row, { kind: 'post' }>).replyContext).toBeUndefined()
  const inThread = buildRows(ch([P('root', 'bob', 0), post]), 'thread')
  expect((inThread.find((r) => r.kind === 'post' && r.post.id === 'r1') as Extract<Row, { kind: 'post' }>).replyContext).toBeUndefined()
})

// Task 6: variant 'thread' inserts a "N replies" divider right after the root.
// Reply-style brief (2026-09-28): every inline reply (not only the first of
// a series) gets isInlineReply so PostItem can draw the left border bar
// around its whole content column, in the channel feed, without CRT.
test('isInlineReply: every reply post in the channel feed without CRT, not only the first of a series', () => {
  const rows = buildRows(
    ch([
      P('root', 'bob', 0),
      P('r0', 'carol', 1, { root_id: 'root' }), // directly under its root: still an inline reply
      P('mid', 'bob', 2),
      P('r1', 'carol', 3, { root_id: 'root' }), // first of a new run
      P('r2', 'carol', 4, { root_id: 'root' }), // consecutive: still flagged
    ]),
  )
  const post = (id: string) => rows.find((r) => r.kind === 'post' && r.post.id === id) as Extract<Row, { kind: 'post' }>
  expect(post('root').isInlineReply).toBeFalsy()
  expect(post('r0').isInlineReply).toBe(true)
  expect(post('mid').isInlineReply).toBeFalsy()
  expect(post('r1').isInlineReply).toBe(true)
  expect(post('r2').isInlineReply).toBe(true)
})

test('isInlineReply: never set with CRT on, and never in the thread panel', () => {
  const post = P('r1', 'carol', 0, { root_id: 'root' })
  const withCRT = buildRows(ch([P('root', 'bob', 0), post], { crt: true }))
  expect((withCRT.find((r) => r.kind === 'post' && r.post.id === 'r1') as Extract<Row, { kind: 'post' }>).isInlineReply).toBeFalsy()
  const inThread = buildRows(ch([P('root', 'bob', 0), post]), 'thread')
  expect((inThread.find((r) => r.kind === 'post' && r.post.id === 'r1') as Extract<Row, { kind: 'post' }>).isInlineReply).toBeFalsy()
})

test('thread variant: a "N replies" divider follows the root, counting the rest', () => {
  const rows = buildRows(ch([P('root', 'bob', 0), P('r1', 'carol', 1, { root_id: 'root' }), P('r2', 'carol', 2, { root_id: 'root' })]), 'thread')
  expect(shape(rows)).toEqual(['day', 'root*', 'threadReplies', 'r1*', 'r2'])
  expect(rows.find((r) => r.kind === 'threadReplies')).toMatchObject({ count: 2 })
})

test('thread variant: no divider is inserted before the root loads', () => {
  const rows = buildRows(ch([]), 'thread')
  expect(rows.some((r) => r.kind === 'threadReplies')).toBe(false)
})

// Fix round 1 (review): the divider must show the root's actual reply_count,
// not how many replies happen to be loaded into the panel right now — the
// panel only ever holds a page (ThreadPage=60) up to the cap
// (ThreadMaxReplies=200), so "loaded posts - 1" undercounts a thread with
// more replies than are currently paged in, and drifts upward as older
// pages load.
const divider = (rows: Row[]) => rows.find((r) => r.kind === 'threadReplies') as Extract<Row, { kind: 'threadReplies' }>

test('thread divider count comes from the root reply_count, not the number of loaded replies', () => {
  const root = P('root', 'bob', 0, { reply_count: 87 })
  const loaded = Array.from({ length: 60 }, (_, i) => P(`r${i}`, 'carol', i + 1, { root_id: 'root' }))
  const rows = buildRows(ch([root, ...loaded], { has_more: true }), 'thread')
  expect(divider(rows).count).toBe(87)
})

test('thread divider count is unchanged after an older page loads more replies into the panel', () => {
  const root = P('root', 'bob', 0, { reply_count: 87 })
  const firstPage = Array.from({ length: 60 }, (_, i) => P(`r${i}`, 'carol', i + 1, { root_id: 'root' }))
  const beforeRows = buildRows(ch([root, ...firstPage], { has_more: true }), 'thread')
  expect(divider(beforeRows).count).toBe(87)

  // Older replies paged in (loaded goes from 60 to 87): the count must stay 87.
  const fullPage = Array.from({ length: 87 }, (_, i) => P(`r${i}`, 'carol', i + 1, { root_id: 'root' }))
  const afterRows = buildRows(ch([root, ...fullPage], { has_more: false }), 'thread')
  expect(divider(afterRows).count).toBe(87)
})

test('thread divider count reflects a live new reply (reply_count grows)', () => {
  const root = P('root', 'bob', 0, { reply_count: 3 })
  const replies = Array.from({ length: 3 }, (_, i) => P(`r${i}`, 'carol', i + 1, { root_id: 'root' }))
  const before = buildRows(ch([root, ...replies]), 'thread')
  expect(divider(before).count).toBe(3)

  const rootAfterLive = P('root', 'bob', 0, { reply_count: 4 })
  const after = buildRows(ch([rootAfterLive, ...replies, P('r3', 'bob', 4, { root_id: 'root' })]), 'thread')
  expect(divider(after).count).toBe(4)
})

test('thread divider count reflects a reply deletion (reply_count shrinks)', () => {
  const root = P('root', 'bob', 0, { reply_count: 3 })
  const replies = Array.from({ length: 3 }, (_, i) => P(`r${i}`, 'carol', i + 1, { root_id: 'root' }))
  const before = buildRows(ch([root, ...replies]), 'thread')
  expect(divider(before).count).toBe(3)

  const rootAfterDelete = P('root', 'bob', 0, { reply_count: 2 })
  const after = buildRows(ch([rootAfterDelete, replies[0], replies[2]]), 'thread')
  expect(divider(after).count).toBe(2)
})

// --- Codex review (Task 6 fix round 1) repros ---

test('Codex: unread boundary inside the loaded window is shown despite an older gap',()=>{
 const rows=buildRows(ch([P('old','bob',0),P('w1','bob',10),P('w2','bob',11)],{new_since:base+10*60000,gap:{open:true,gen:1,before_id:'w1',stale:false}}));
 expect(rows.filter(r=>r.kind==='new')).toHaveLength(1);
});

test('review: an unread boundary between window posts is drawn even with a gap above the window', () => {
  const g = { open: true, gen: 1, before_id: 'w1', stale: false }
  const rows = buildRows(ch([P('s1', 'bob', 0), P('w1', 'bob', 10), P('w2', 'bob', 11), P('w3', 'bob', 12)], { gap: g, new_since: base + 11.5 * 60_000 }))
  expect(shape(rows)).toEqual(['day', 's1*', 'gap', 'w1*', 'w2', 'new', 'w3*'])
})
