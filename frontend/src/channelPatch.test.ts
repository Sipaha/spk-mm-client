import { applyChannelPatch } from './channelPatch'
import type { ChannelDTO, ChannelPatch, PostView } from './api/types'

const row = (id: string, message = id): PostView => ({ id, user_id: 'u1', author: 'alice', message, create_at: 1 })
const channel = { id: 'a', posts: [row('1'), row('2')], projection_rev: 1 } as ChannelDTO
const patch = (fields: Partial<ChannelPatch> = {}): ChannelPatch => ({ view: { ...channel, posts: [], draft: 'new draft' }, base: 1, revision: 2, reset: false, posts: [], ...fields })

test('metadata-only updates preserve the entire history array', () => {
 const next = applyChannelPatch(channel, patch())!
 expect(next.posts).toBe(channel.posts)
 expect(next.draft).toBe('new draft')
 expect(next.projection_rev).toBe(2)
})
test('changes, deletes, prepends, pending replacement and ordering preserve unchanged rows', () => {
 const next = applyChannelPatch(channel, patch({ posts: [row('0'), row('2', 'edited')], order: ['0', '1', '2'] }))!
 expect(next.posts.map(p => p.id)).toEqual(['0', '1', '2'])
 expect(next.posts[1]).toBe(channel.posts[0])
 expect(next.posts[2].message).toBe('edited')
 const deleted = applyChannelPatch(channel, patch({ removed: ['2'], order: ['1'] }))!
 expect(deleted.posts).toEqual([channel.posts[0]])
 const confirmed = applyChannelPatch(channel, patch({ removed: ['2'], posts: [row('real')], order: ['1', 'real'] }))!
 expect(confirmed.posts.map(p => p.id)).toEqual(['1', 'real'])
})
test('unknown baselines and broken membership are rejected; reset recovers', () => {
 expect(applyChannelPatch(channel, patch({ base: 0 }))).toBeNull()
 expect(applyChannelPatch(null, patch())).toBeNull()
 expect(applyChannelPatch(channel, patch({ order: ['missing'] }))).toBeNull()
 expect(applyChannelPatch(channel, patch({ order: ['1', '1'] }))).toBeNull()
 const reset = applyChannelPatch(null, patch({ reset: true, posts: [] }))!
 expect(reset.posts).toEqual([])
})

test('reset deduplicates repeated post IDs while keeping distinct posts with identical text and allowing follow-up deltas', () => {
 const uniquePosts = [row('p1', 'same'), row('p2', 'same')]
 const cleanReset = applyChannelPatch(null, patch({ reset: true, revision: 5, posts: uniquePosts }))!
 expect(cleanReset.posts).toBe(uniquePosts)
 expect(cleanReset.posts.map(p => p.id)).toEqual(['p1', 'p2'])

 const dupReset = applyChannelPatch(null, patch({
  reset: true,
  revision: 6,
  posts: [row('p-0139', 'old'), row('twin-1', 'ok'), row('p-0139', 'latest'), row('twin-2', 'ok')],
 }))!
 expect(dupReset.posts.map(p => p.id)).toEqual(['p-0139', 'twin-1', 'twin-2'])
 expect(dupReset.posts[0].message).toBe('latest')

 const delta = applyChannelPatch(dupReset, patch({
  base: 6,
  revision: 7,
  posts: [row('p-0139', 'edited again')],
  order: ['p-0139', 'twin-1', 'twin-2'],
 }))
 expect(delta).not.toBeNull()
 expect(delta!.posts.map(p => p.id)).toEqual(['p-0139', 'twin-1', 'twin-2'])
 expect(delta!.posts[0].message).toBe('edited again')
})
