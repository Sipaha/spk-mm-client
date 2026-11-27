import { describe, expect, test } from 'vitest'
import type { ServerDTO } from './api/types'
import { parsePermalink } from './permalink'

const srv = (id: number, url: string, signed_in = true): ServerDTO => ({
  id,
  name: `S${id}`,
  url,
  signed_in,
  username: 'alice',
  gitlab: false,
  state: signed_in ? 'live' : 'off',
  unread: false,
  mentions: 0,
})

describe('parsePermalink', () => {
  test('matches absolute permalink for signed-in server, case-insensitive host, default ports, optional slash, query and fragment', () => {
    const servers = [srv(1, 'https://mm.citeck.ru'), srv(2, 'http://mm.local:80')]

    expect(parsePermalink('https://mm.citeck.ru/citeck/pl/j1ccaz5egjdiij8tjyh6n59hqc', servers, 1)).toEqual({
      serverId: 1,
      teamName: 'citeck',
      postId: 'j1ccaz5egjdiij8tjyh6n59hqc',
      fallbackUrl: 'https://mm.citeck.ru/citeck/pl/j1ccaz5egjdiij8tjyh6n59hqc',
    })

    expect(parsePermalink('https://MM.CITECK.RU:443/citeck/pl/post_1-a/?foo=1&bar=2#sec', servers, 1)).toEqual({
      serverId: 1,
      teamName: 'citeck',
      postId: 'post_1-a',
      fallbackUrl: 'https://mm.citeck.ru/citeck/pl/post_1-a?foo=1&bar=2#sec',
    })

    expect(parsePermalink('http://mm.local/dev/pl/p99', servers, 1)).toEqual({
      serverId: 2,
      teamName: 'dev',
      postId: 'p99',
      fallbackUrl: 'http://mm.local/dev/pl/p99',
    })
  })

  test('picks longest matching basePath among signed-in servers and avoids duplicating subpath in fallbackUrl', () => {
    const servers = [
      srv(1, 'https://corp.example'),
      srv(2, 'https://corp.example/mm'),
      srv(3, 'https://corp.example/mm/sub', false), // unsigned: ignored
    ]

    expect(parsePermalink('https://corp.example/mm/teamA/pl/abc123?x=1#h', servers, 1)).toEqual({
      serverId: 2,
      teamName: 'teamA',
      postId: 'abc123',
      fallbackUrl: 'https://corp.example/mm/teamA/pl/abc123?x=1#h',
    })

    // Unsigned server #3 is skipped, falls back to checking #2 (extra segment -> null).
    expect(parsePermalink('https://corp.example/mm/sub/teamA/pl/abc123', servers, 1)).toBeNull()

    // Root server #1 matches when /mm is not present.
    expect(parsePermalink('https://corp.example/teamB/pl/xyz789', servers, 2)).toEqual({
      serverId: 1,
      teamName: 'teamB',
      postId: 'xyz789',
      fallbackUrl: 'https://corp.example/teamB/pl/xyz789',
    })
  })

  test('PR1: relative permalink (with and without base subpath) always produces absolute http(s) fallbackUrl without duplicating subpath', () => {
    const servers = [
      srv(1, 'https://mm.citeck.ru'),
      srv(2, 'https://corp.example/chat/'),
    ]

    // Root server, relative link with query + fragment.
    expect(parsePermalink('/citeck/pl/p1?from=email#top', servers, 1)).toEqual({
      serverId: 1,
      teamName: 'citeck',
      postId: 'p1',
      fallbackUrl: 'https://mm.citeck.ru/citeck/pl/p1?from=email#top',
    })

    // Subpath server, relative link WITHOUT basePath.
    expect(parsePermalink('/eng/pl/p2/?q=hello#frag', servers, 2)).toEqual({
      serverId: 2,
      teamName: 'eng',
      postId: 'p2',
      fallbackUrl: 'https://corp.example/chat/eng/pl/p2?q=hello#frag',
    })

    // Subpath server, relative link WITH basePath (must NOT duplicate /chat/chat).
    expect(parsePermalink('/chat/eng/pl/p3?a=b#c', servers, 2)).toEqual({
      serverId: 2,
      teamName: 'eng',
      postId: 'p3',
      fallbackUrl: 'https://corp.example/chat/eng/pl/p3?a=b#c',
    })

    // No current server or unsigned current server -> null.
    expect(parsePermalink('/citeck/pl/p1', servers, null)).toBeNull()
    expect(parsePermalink('/citeck/pl/p1', [srv(1, 'https://mm.citeck.ru', false)], 1)).toBeNull()
  })

  test('rejects foreign hosts, mismatched scheme/port, unsigned servers, non-/pl/ paths, extra segments and invalid postIDs', () => {
    const servers = [srv(1, 'https://mm.citeck.ru'), srv(2, 'https://unsigned.example', false)]

    for (const bad of [
      '',
      'not a url',
      'https://other.example/citeck/pl/p1',
      'http://mm.citeck.ru/citeck/pl/p1', // wrong scheme
      'https://mm.citeck.ru:8443/citeck/pl/p1', // wrong port
      'https://unsigned.example/team/pl/p1', // not signed in
      'https://mm.citeck.ru/citeck/channels/town-square', // not /pl/
      'https://mm.citeck.ru/citeck/pl/p1/extra', // extra segment
      'https://mm.citeck.ru/pl/p1', // missing team
      'https://mm.citeck.ru//pl/p1', // empty team
      'https://mm.citeck.ru/../pl/p1', // dot team
      'https://mm.citeck.ru/citeck/pl/bad.id', // invalid postId char
      'https://mm.citeck.ru/citeck/pl/' + 'a'.repeat(65), // postId > 64 chars
      '//mm.citeck.ru/citeck/pl/p1', // protocol-relative rejected
      'javascript:alert(1)',
      'mailto:user@example.com',
      'https://user:pass@mm.citeck.ru/citeck/pl/p1', // userinfo rejected
    ]) {
      expect(parsePermalink(bad, servers, 1), bad).toBeNull()
    }
  })
})
