import type { ChannelDTO, PostView } from '../api/types'

export type Row =
  | { kind: 'more'; key: 'more' }
  | { kind: 'day'; key: string; ms: number }
  | { kind: 'new'; key: 'new' }
  | { kind: 'gap'; key: 'gap' }
  // threadReplies: the panel's "N replies" divider, right after the root
  // (variant 'thread' only — see buildRows).
  | { kind: 'threadReplies'; key: 'thread-replies'; count: number }
  | {
      kind: 'post'
      key: string
      post: PostView
      head: boolean
      // replyContext: this reply's "reply to <author>: <snippet>" line —
      // only the first reply of a consecutive series to the same root,
      // without CRT, in the channel feed (variant 'channel'). author '' =
      // the root is not held ("reply in a thread").
      replyContext?: { author: string; snippet: string } | null
    }

export type FeedVariant = 'channel' | 'thread'

const GROUP_MS = 5 * 60 * 1000
const dayKey = (ms: number) => new Date(ms).toDateString()

// isFirstReplyOfSeries: the previous feed row is neither this reply's root
// nor another reply of the same thread (webapp's isFirstReply). The very
// first row overall (no previous post) counts as first-of-series too.
function isFirstReplyOfSeries(prev: PostView | null, p: PostView): boolean {
  if (!p.root_id) return false
  if (!prev) return true
  if (prev.id === p.root_id) return false
  if (prev.root_id === p.root_id) return false
  return true
}

// buildRows turns the channel's (or thread's) posts (oldest first) into feed
// rows: day separators, the "new messages" line, the gap marker, author
// groups and — variant-dependent — the reply-context line (channel, CRT
// off) or the "N replies" divider after the root (thread).
export function buildRows(
  ch: Pick<ChannelDTO, 'posts' | 'new_since' | 'me_id' | 'gap_after' | 'has_more' | 'crt'>,
  variant: FeedVariant = 'channel',
): Row[] {
  const rows: Row[] = []
  if (ch.has_more) rows.push({ kind: 'more', key: 'more' })
  let prev: PostView | null = null
  let lineDone = false
  let broken = false // a gap row (or the thread divider) ended the previous group
  let firstPost = true
  for (const p of ch.posts) {
    let head = true
    if (!prev || dayKey(prev.create_at) !== dayKey(p.create_at)) {
      rows.push({ kind: 'day', key: `day-${dayKey(p.create_at)}`, ms: p.create_at })
    } else {
      head =
        broken ||
        prev.user_id !== p.user_id ||
        prev.author !== p.author ||
        p.create_at - prev.create_at > GROUP_MS ||
        !!p.system ||
        !!prev.system ||
        !!p.webhook ||
        !!prev.webhook
    }
    if (!lineDone && ch.new_since > 0 && p.create_at > ch.new_since && p.user_id !== ch.me_id && !p.pending && !p.failed) {
      rows.push({ kind: 'new', key: 'new' })
      lineDone = true
      head = true
    }
    let replyContext: { author: string; snippet: string } | null | undefined
    if (variant === 'channel' && !ch.crt && p.root_id && isFirstReplyOfSeries(prev, p)) {
      replyContext = { author: p.root_author ?? '', snippet: p.root_snippet ?? '' }
      head = true
    }
    // A pending post's real post arrives under a different id; keying by
    // pending_post_id (set on both sides by the Go layer) keeps the row —
    // and the virtualizer's measured size for it — across the swap.
    rows.push({ kind: 'post', key: p.pending_post_id || p.id, post: p, head, replyContext })
    broken = false
    if (variant === 'thread' && firstPost && !p.root_id) {
      rows.push({ kind: 'threadReplies', key: 'thread-replies', count: ch.posts.length - 1 })
      broken = true
    }
    firstPost = false
    if (ch.gap_after && p.id === ch.gap_after) {
      rows.push({ kind: 'gap', key: 'gap' })
      broken = true
    }
    prev = p
  }
  return rows
}
