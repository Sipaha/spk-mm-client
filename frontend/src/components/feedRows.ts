import type { ChannelDTO, PostView } from '../api/types'

export type Row =
  | { kind: 'more'; key: 'more' }
  | { kind: 'day'; key: string; ms: number }
  | { kind: 'new'; key: 'new' }
  | { kind: 'gap'; key: 'gap' }
  | { kind: 'post'; key: string; post: PostView; head: boolean }

const GROUP_MS = 5 * 60 * 1000
const dayKey = (ms: number) => new Date(ms).toDateString()

// buildRows turns the channel's posts (oldest first) into feed rows: day
// separators, the "new messages" line, the gap marker and author groups.
export function buildRows(ch: Pick<ChannelDTO, 'posts' | 'new_since' | 'me_id' | 'gap_after' | 'has_more'>): Row[] {
  const rows: Row[] = []
  if (ch.has_more) rows.push({ kind: 'more', key: 'more' })
  let prev: PostView | null = null
  let lineDone = false
  let broken = false // a gap row ended the previous group
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
    // A pending post's real post arrives under a different id; keying by
    // pending_post_id (set on both sides by the Go layer) keeps the row —
    // and the virtualizer's measured size for it — across the swap.
    rows.push({ kind: 'post', key: p.pending_post_id || p.id, post: p, head })
    broken = false
    if (ch.gap_after && p.id === ch.gap_after) {
      rows.push({ kind: 'gap', key: 'gap' })
      broken = true
    }
    prev = p
  }
  return rows
}
