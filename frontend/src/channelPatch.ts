import type { ChannelDTO, ChannelPatch, PostView } from './api/types'

function dedupePosts(posts: PostView[]): PostView[] {
  const seen = new Set<string>()
  let hasDup = false
  for (const p of posts) {
    if (seen.has(p.id)) {
      hasDup = true
      break
    }
    seen.add(p.id)
  }
  if (!hasDup) return posts
  const byId = new Map<string, PostView>()
  const order: string[] = []
  for (const p of posts) {
    if (!byId.has(p.id)) order.push(p.id)
    byId.set(p.id, p)
  }
  return order.map(id => byId.get(id)!)
}

// Keep unchanged row objects (and the whole posts array on metadata-only
// refreshes). A discarded baseline is recovered by the next reset response.
export function applyChannelPatch(previous: ChannelDTO | null, patch: ChannelPatch): ChannelDTO | null {
  if (patch.reset) return { ...patch.view, posts: dedupePosts(patch.posts), projection_rev: patch.revision }
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
