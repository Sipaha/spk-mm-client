import type { ChannelDTO, HistGap, PostView } from '../api/types'

export type Row =
  | { kind: 'more'; key: 'more' }
  | { kind: 'day'; key: string; ms: number }
  | { kind: 'new'; key: 'new' }
  // gapAfter: the window lost posts across a reconnect (gap_after).
  | { kind: 'gapAfter'; key: 'gap' }
  // gap: the held history does not (yet) join the window (spec «Поиск»,
  // «Лента (Feed)»). open: the row between them, loading newer pages (key
  // 'gap:<gen>' — the same row while that gap holds); a closed gap whose
  // history is still stale is a thin indicator at the top of the history
  // (key 'gap-stale:<gen>').
  | { kind: 'gap'; key: string; open: boolean; stale: boolean }
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
      // isInlineReply: this post is a reply rendered inline in the channel
      // feed (variant 'channel', no CRT) — set for EVERY such reply, not
      // only the first of a series (unlike replyContext). PostItem uses it
      // to wrap the post's content column in a left border bar, webapp-like
      // (reply-style brief, 2026-09-28). Never set in the thread panel or
      // with CRT on.
      isInlineReply?: boolean
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
// rows: day separators, the "new messages" line, the gap markers, author
// groups and — variant-dependent — the reply-context line (channel, CRT
// off) or the "N replies" divider after the root (thread).
// gap (the history gap; absent: none): its row goes right before
// before_id's post — ahead of that post's day separator — or after the
// last post while before_id is '' (or not shown). The "new messages" line
// is not drawn after an open gap when new_since is older than the window's
// first post (before_id): its true place may then be among the posts not
// loaded yet. A boundary inside the window itself is known and drawn.
export function buildRows(
  ch: Pick<ChannelDTO, 'posts' | 'new_since' | 'me_id' | 'gap_after' | 'has_more' | 'crt'> & { gap?: HistGap },
  variant: FeedVariant = 'channel',
): Row[] {
  const rows: Row[] = []
  if (ch.has_more) rows.push({ kind: 'more', key: 'more' })
  const gap = ch.gap
  if (gap && !gap.open && gap.stale) rows.push({ kind: 'gap', key: `gap-stale:${gap.gen}`, open: false, stale: true })
  let prev: PostView | null = null
  let lineDone = false
  let broken = false // a gap row (or the thread divider) ended the previous group
  let firstPost = true
  let gapDone = !gap?.open
  // new_since before the window's first post: the boundary may be in the gap.
  const gapFirst = gap?.open ? ch.posts.find((p) => p.id === gap.before_id) : undefined
  const lineInGap = !!gapFirst && ch.new_since < gapFirst.create_at
  const pushGap = () => {
    rows.push({ kind: 'gap', key: `gap:${gap!.gen}`, open: true, stale: gap!.stale })
    gapDone = true
    broken = true
  }
  for (const p of ch.posts) {
    if (!gapDone && gap!.before_id !== '' && p.id === gap!.before_id) pushGap()
    const afterGap = gap?.open === true && gapDone
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
      lineDone = true
      if (!(afterGap && lineInGap)) {
        rows.push({ kind: 'new', key: 'new' })
        head = true
      }
    }
    const isInlineReply = variant === 'channel' && !ch.crt && !!p.root_id
    let replyContext: { author: string; snippet: string } | null | undefined
    if (isInlineReply && isFirstReplyOfSeries(prev, p)) {
      replyContext = { author: p.root_author ?? '', snippet: p.root_snippet ?? '' }
      head = true
    }
    // A pending post's real post arrives under a different id; keying by
    // pending_post_id (set on both sides by the Go layer) keeps the row —
    // and the virtualizer's measured size for it — across the swap.
    rows.push({ kind: 'post', key: p.pending_post_id || p.id, post: p, head, replyContext, isInlineReply })
    broken = false
    if (variant === 'thread' && firstPost && !p.root_id) {
      // The root's own reply_count is the thread's true total — never the
      // number of replies currently paged into the panel (ThreadPage=60,
      // growing toward ThreadMaxReplies=200 as older pages load; fix round
      // 1 review). Fall back to the loaded count only if reply_count is
      // itself absent (e.g. genuinely zero replies, omitempty on the wire).
      rows.push({ kind: 'threadReplies', key: 'thread-replies', count: p.reply_count ?? ch.posts.length - 1 })
      broken = true
    }
    firstPost = false
    if (ch.gap_after && p.id === ch.gap_after) {
      rows.push({ kind: 'gapAfter', key: 'gap' })
      broken = true
    }
    prev = p
  }
  if (!gapDone) pushGap()
  return rows
}
