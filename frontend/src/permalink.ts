import type { ServerDTO } from './api/types'

export interface ParsedPermalink {
  serverId: number
  teamName: string
  postId: string
  fallbackUrl: string
}

const POST_ID_RE = /^[A-Za-z0-9_-]{1,64}$/

interface NormalizedServer {
  server: ServerDTO
  origin: string
  protocol: string
  host: string
  port: string
  basePath: string
}

function normalizeHost(hostname: string): string {
  return hostname.replace(/\.$/, '').toLowerCase()
}

function effectivePort(u: URL): string {
  if (u.port) return u.port
  return u.protocol === 'https:' ? '443' : '80'
}

function normalizeBasePath(pathname: string): string {
  const trimmed = pathname.replace(/\/+$/, '')
  return trimmed === '/' ? '' : trimmed
}

function normalizeServer(server: ServerDTO): NormalizedServer | null {
  if (!server.signed_in) return null
  try {
    const u = new URL(server.url)
    if ((u.protocol !== 'http:' && u.protocol !== 'https:') || !u.hostname) return null
    return {
      server,
      origin: u.origin,
      protocol: u.protocol,
      host: normalizeHost(u.hostname),
      port: effectivePort(u),
      basePath: normalizeBasePath(u.pathname),
    }
  } catch {
    return null
  }
}

function parsePlSegments(relPath: string): { teamName: string; postId: string } | null {
  if (!relPath.startsWith('/')) return null
  const withoutLeading = relPath.slice(1)
  const withoutTrailing = withoutLeading.endsWith('/') ? withoutLeading.slice(0, -1) : withoutLeading
  const parts = withoutTrailing.split('/')
  if (parts.length !== 3 || parts[1] !== 'pl') return null
  const [teamName, , postId] = parts
  if (!teamName || teamName === '.' || teamName === '..' || /\s/.test(teamName)) return null
  if (!POST_ID_RE.test(postId)) return null
  return { teamName, postId }
}

function buildFallbackUrl(ns: NormalizedServer, teamName: string, postId: string, search: string, hash: string): string {
  return `${ns.origin}${ns.basePath}/${teamName}/pl/${postId}${search}${hash}`
}

// parsePermalink recognizes Mattermost message links (`/<team>/pl/<postID>`)
// belonging to a signed-in server (including servers mounted under a base
// subpath and relative `/<team>/pl/<postID>` or `<basePath>/<team>/pl/<postID>`
// links for currentServerId). fallbackUrl is always an absolute http(s) URL
// rooted at the matched server's URL without duplicating its base subpath,
// preserving query and fragment.
export function parsePermalink(
  href: string,
  servers: readonly ServerDTO[],
  currentServerId?: number | null,
): ParsedPermalink | null {
  const trimmed = href.trim()
  if (!trimmed) return null

  // Relative path for the current signed-in server (reject protocol-relative `//host/...`).
  if (trimmed.startsWith('/')) {
    if (trimmed.startsWith('//') || currentServerId == null) return null
    const current = servers.find((s) => s.id === currentServerId)
    if (!current) return null
    const ns = normalizeServer(current)
    if (!ns) return null
    let u: URL
    try {
      u = new URL(trimmed, 'http://placeholder.invalid')
    } catch {
      return null
    }
    let parsed: { teamName: string; postId: string } | null = null
    if (ns.basePath && u.pathname.startsWith(`${ns.basePath}/`)) {
      parsed = parsePlSegments(u.pathname.slice(ns.basePath.length))
    }
    if (!parsed) {
      parsed = parsePlSegments(u.pathname)
    }
    if (!parsed) return null
    return {
      serverId: ns.server.id,
      teamName: parsed.teamName,
      postId: parsed.postId,
      fallbackUrl: buildFallbackUrl(ns, parsed.teamName, parsed.postId, u.search, u.hash),
    }
  }

  let u: URL
  try {
    u = new URL(trimmed)
  } catch {
    return null
  }
  if ((u.protocol !== 'http:' && u.protocol !== 'https:') || !u.hostname || u.username || u.password) {
    return null
  }
  const uHost = normalizeHost(u.hostname)
  const uPort = effectivePort(u)

  const candidates: NormalizedServer[] = []
  for (const s of servers) {
    const ns = normalizeServer(s)
    if (!ns) continue
    if (ns.protocol !== u.protocol || ns.host !== uHost || ns.port !== uPort) continue
    if (ns.basePath && u.pathname !== ns.basePath && !u.pathname.startsWith(`${ns.basePath}/`)) continue
    candidates.push(ns)
  }
  // Prefer the longest matching basePath; break ties by currentServerId.
  candidates.sort((a, b) => {
    if (b.basePath.length !== a.basePath.length) return b.basePath.length - a.basePath.length
    const aCur = a.server.id === currentServerId ? 1 : 0
    const bCur = b.server.id === currentServerId ? 1 : 0
    return bCur - aCur
  })

  for (const ns of candidates) {
    const relPath = ns.basePath ? u.pathname.slice(ns.basePath.length) : u.pathname
    const parsed = parsePlSegments(relPath)
    if (!parsed) continue
    return {
      serverId: ns.server.id,
      teamName: parsed.teamName,
      postId: parsed.postId,
      fallbackUrl: buildFallbackUrl(ns, parsed.teamName, parsed.postId, u.search, u.hash),
    }
  }
  return null
}
