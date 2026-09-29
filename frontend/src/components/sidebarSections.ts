import type { CategoryView, ChannelItem } from '../api/types'

// HeldChannel: mirrors the webapp's `state.views.channel.lastUnreadChannel`
// (reducers/views/channel.ts, SET_LAST_UNREAD_CHANNEL) — the channel that
// was unread/mentioned the moment it became active, kept in the Unreads
// section until the user switches to a *different* channel, so a row
// doesn't jump out from under the cursor the instant it's marked read.
// Captured centrally in `chat.ts`'s `openChannel` — the single gateway
// every channel switch funnels through (a sidebar click, a notification, a
// `~channel` link in the feed or thread panel) — read synchronously from
// the store before the async mark-as-read round trip, the same way the
// webapp's switchToChannel/setLastUnreadChannel captures it synchronously
// at dispatch time. Lives on the store (`heldChannel`) rather than in
// Sidebar.tsx's own state so every origin sees the same value.
export interface HeldChannel {
  id: string
  hadMentions: boolean
}

export interface SidebarSections {
  // unread: the Unreads category's rows, already ordered (mentions first,
  // then most-recently-active; muted last) — sidebar.types.unreadChannels
  // in the webapp (components/sidebar/unread_channels.tsx).
  unread: ChannelItem[]
  // categories: the input categories with every channel that moved into
  // `unread` removed (getChannelsInCategoryOrder's "Filter out channels
  // that have been moved to the Unreads category").
  categories: CategoryView[]
}

// computeSidebarSections groups unread/mentioned channels (plus the held
// active channel, if any) into a synthetic "Unreads" category and strips
// them out of their own categories, mirroring the webapp's
// getUnreadChannels/getChannelsInCategoryOrder pair (selectors/views/
// channel_sidebar.ts) built on top of mattermost-redux's
// getUnreadChannelIds/sortUnreadChannels (selectors/entities/channels.ts).
//
// Membership: a channel qualifies if its own `unread` flag is true — Go's
// unreadLocked (internal/state/server.go) computes it as
// `mentions > 0 || (!muted && messages > 0)`, exactly the webapp's
// `showUnread` (channel_utils.ts calculateUnreadCount) — or if it's the
// held channel (kept even after being read, by id, regardless of its
// current live unread/mentions state).
//
// Ordering: muted last (regardless of mentions — a muted channel only gets
// here at all via a mention), then non-muted mentions first (a held
// channel's remembered hadMentions counts here even after its live
// `mentions` drops to 0), then by last_activity_at descending.
export function computeSidebarSections(categories: CategoryView[] | null, held: HeldChannel | null): SidebarSections {
  const cats = categories ?? []
  const includedIds = new Set<string>()
  const entries: Array<{ item: ChannelItem; heldMentions: boolean }> = []

  for (const cat of cats) {
    for (const item of cat.channels ?? []) {
      const isHeld = held !== null && held.id === item.id
      if (!item.unread && !isHeld) continue
      includedIds.add(item.id)
      entries.push({ item, heldMentions: isHeld && held!.hadMentions })
    }
  }

  const unread = entries
    .slice()
    .sort((a, b) => {
      if (a.item.muted !== b.item.muted) return a.item.muted ? 1 : -1

      const aMentions = a.item.mentions > 0 || a.heldMentions
      const bMentions = b.item.mentions > 0 || b.heldMentions
      if (aMentions !== bMentions) return aMentions ? -1 : 1

      return (b.item.last_activity_at ?? 0) - (a.item.last_activity_at ?? 0)
    })
    .map((e) => e.item)

  const filtered = cats.map((cat) => ({
    ...cat,
    channels: (cat.channels ?? []).filter((c) => !includedIds.has(c.id)),
  }))

  return { unread, categories: filtered }
}
