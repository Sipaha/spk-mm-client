import type { ChannelDTO, ChannelPatch } from './api/types'

// Keep unchanged row objects (and the whole posts array on metadata-only
// refreshes). A discarded baseline is recovered by the next reset response.
export function applyChannelPatch(previous: ChannelDTO | null, patch: ChannelPatch): ChannelDTO | null {
  if (patch.reset) return { ...patch.view, posts: patch.posts, projection_rev: patch.revision }
  if (!previous || previous.id !== patch.view.id || previous.projection_rev !== patch.base) return null
  let posts = previous.posts
  if (patch.posts.length || patch.removed?.length || patch.order) {
    const byId = new Map(posts.map(p => [p.id, p]))
    for (const id of patch.removed ?? []) byId.delete(id)
    for (const p of patch.posts) byId.set(p.id, p)
    if (patch.order) {
      if (patch.order.length !== byId.size || patch.order.some(id => !byId.has(id)) || new Set(patch.order).size !== patch.order.length) return null
      posts = patch.order.map(id => byId.get(id)!)
    } else {
      posts = posts.filter(p => byId.has(p.id)).map(p => byId.get(p.id)!)
      if (posts.length !== byId.size) return null
    }
  }
  return { ...patch.view, posts, projection_rev: patch.revision }
}
