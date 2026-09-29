import { expect, test } from 'vitest'
import type { CategoryView, ChannelItem } from '../api/types'
import { computeSidebarSections } from './sidebarSections'

function chan(over: Partial<ChannelItem> & { id: string }): ChannelItem {
  return { name: over.id, type: 'O', unread: false, mentions: 0, muted: false, last_activity_at: 0, ...over }
}

function cat(id: string, channels: ChannelItem[], type = 'channels'): CategoryView {
  return { id, type, name: id, collapsed: false, channels }
}

test('a read channel stays in its own category and is not in Unreads', () => {
  const read = chan({ id: 'c-read' })
  const { unread, categories } = computeSidebarSections([cat('ch', [read])], null)
  expect(unread).toEqual([])
  expect(categories[0].channels).toEqual([read])
})

test('an unread channel moves to Unreads and leaves its own category', () => {
  const off = chan({ id: 'c-off', unread: true, last_activity_at: 100 })
  const { unread, categories } = computeSidebarSections([cat('ch', [off])], null)
  expect(unread).toEqual([off])
  expect(categories[0].channels).toEqual([])
})

test('a channel with a mention but unread:false still counts (mentions imply unread server-side, but the model does not require it)', () => {
  // Go's `unread` already implies mentions > 0 (unreadLocked), but the pure
  // function only looks at `unread` for membership — a channel with
  // mentions > 0 and unread: false (shouldn't happen from the real server,
  // but the model must not silently include or exclude based on mentions
  // alone) is NOT pulled into Unreads by mentions alone.
  const odd = chan({ id: 'c-odd', unread: false, mentions: 3 })
  const { unread, categories } = computeSidebarSections([cat('ch', [odd])], null)
  expect(unread).toEqual([])
  expect(categories[0].channels).toEqual([odd])
})

test('muted unread without a mention is not unread server-side, so it never reaches Unreads', () => {
  // Mirrors the real invariant (Go's unreadLocked never sets unread:true for
  // a muted channel unless it has a mention) — a muted, non-mentioned
  // channel simply never has unread:true, so it is never pulled in.
  const muted = chan({ id: 'c-muted', unread: false, muted: true })
  const { unread, categories } = computeSidebarSections([cat('ch', [muted])], null)
  expect(unread).toEqual([])
  expect(categories[0].channels).toEqual([muted])
})

test('a muted channel with a mention (unread:true) is included but sorts after all non-muted rows', () => {
  const mutedMention = chan({ id: 'c-muted-mention', unread: true, mentions: 1, muted: true, last_activity_at: 999 })
  const plainUnread = chan({ id: 'c-plain', unread: true, last_activity_at: 1 })
  const { unread } = computeSidebarSections([cat('ch', [mutedMention, plainUnread])], null)
  expect(unread.map((c) => c.id)).toEqual(['c-plain', 'c-muted-mention'])
})

test('DMs and favorites are grouped into Unreads the same as ordinary channels', () => {
  const dm = chan({ id: 'c-dm', type: 'D', unread: true, last_activity_at: 5 })
  const fav = chan({ id: 'c-fav', unread: true, last_activity_at: 10 })
  const { unread, categories } = computeSidebarSections(
    [cat('fav', [fav], 'favorites'), cat('dm', [dm], 'direct_messages')],
    null,
  )
  expect(unread.map((c) => c.id)).toEqual(['c-fav', 'c-dm'])
  expect(categories[0].channels).toEqual([])
  expect(categories[1].channels).toEqual([])
})

test('ordering: mentions first, then by last_activity_at descending, muted always last', () => {
  const old = chan({ id: 'old', unread: true, mentions: 0, last_activity_at: 1 })
  const mention = chan({ id: 'mention', unread: true, mentions: 2, last_activity_at: 2 })
  const recent = chan({ id: 'recent', unread: true, mentions: 0, last_activity_at: 50 })
  const mutedMention = chan({ id: 'muted-mention', unread: true, mentions: 1, muted: true, last_activity_at: 1000 })
  const { unread } = computeSidebarSections([cat('ch', [old, mention, recent, mutedMention])], null)
  expect(unread.map((c) => c.id)).toEqual(['mention', 'recent', 'old', 'muted-mention'])
})

test('held: the active channel that was unread when opened stays in Unreads after it is marked read', () => {
  const opened = chan({ id: 'c-active', unread: false, mentions: 0, last_activity_at: 5 }) // now read
  const { unread, categories } = computeSidebarSections([cat('ch', [opened])], { id: 'c-active', hadMentions: false })
  expect(unread).toEqual([opened])
  expect(categories[0].channels).toEqual([])
})

test('held: a held channel keeps its remembered hadMentions for sort position even after mentions drop to 0', () => {
  const heldRead = chan({ id: 'c-held', unread: false, mentions: 0, last_activity_at: 1 })
  const plainUnread = chan({ id: 'c-plain', unread: true, mentions: 0, last_activity_at: 100 })
  const { unread } = computeSidebarSections([cat('ch', [heldRead, plainUnread])], { id: 'c-held', hadMentions: true })
  // c-held sorts as if it still had a mention (first), even though its live
  // mentions is 0 and it's older by last_activity_at.
  expect(unread.map((c) => c.id)).toEqual(['c-held', 'c-plain'])
})

test('held channel not found anywhere (e.g. left/archived) does nothing', () => {
  const other = chan({ id: 'c-other' })
  const { unread, categories } = computeSidebarSections([cat('ch', [other])], { id: 'gone', hadMentions: true })
  expect(unread).toEqual([])
  expect(categories[0].channels).toEqual([other])
})

test('empty categories/null input produce an empty Unreads section', () => {
  expect(computeSidebarSections(null, null)).toEqual({ unread: [], categories: [] })
  expect(computeSidebarSections([], null)).toEqual({ unread: [], categories: [] })
})

test('a category with a null channels array is treated as empty, not a crash', () => {
  const cv: CategoryView = { id: 'fav', type: 'favorites', name: 'fav', collapsed: false, channels: null }
  const { unread, categories } = computeSidebarSections([cv], null)
  expect(unread).toEqual([])
  expect(categories[0].channels).toEqual([])
})
