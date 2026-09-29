import type { ACChannel, ACCommand, ACUser, AutocompleteDTO, AutocompleteKind } from './api/types'
import type { EmojiIndex } from './emoji/index'

// Composer autocomplete (brief 2026-09-29): which word at the caret opens
// the popup, what it lists and what picking a row inserts. The rules
// follow the Mattermost webapp's suggestion providers
// (components/suggestion/*_provider.tsx); only the text before the caret
// counts.

export interface Trigger {
  kind: AutocompleteKind
  prefix: string // what follows the trigger character
  start: number // the trigger character's index
  end: number // the caret
}

// @: at the start or after a character that is not part of a word or an
// address (an email a@b never triggers) — like remarkMentions' MENTION.
const USERS = /(^|[^\p{L}\p{N}_.@-])@([\p{L}\p{N}._-]*)$/u
// ~: not after a word character or another ~.
const CHANNELS = /(^|[^\p{L}\p{N}_~])~([^~\s]*)$/u
// :name — at the start or after whitespace (never mid-word: 12:30, a:b,
// http://), from 2 characters of an emoji-name shape (":-)" is not one).
const EMOJI = /(^|\s):([^:\s]*)$/u
const EMOJI_NAME = /^[A-Za-z0-9_+-]{2,}$/
// /: only the very start of the message, while the command word is typed.
const COMMANDS = /^\/([^\s/]*)$/

export function findTrigger(text: string, caret: number): Trigger | null {
  const before = text.slice(0, caret)
  let m = COMMANDS.exec(before)
  if (m) return { kind: 'commands', prefix: m[1], start: 0, end: caret }
  m = USERS.exec(before)
  if (m) return { kind: 'users', prefix: m[2], start: m.index + m[1].length, end: caret }
  m = CHANNELS.exec(before)
  if (m) return { kind: 'channels', prefix: m[2], start: m.index + m[1].length, end: caret }
  m = EMOJI.exec(before)
  if (m && EMOJI_NAME.test(m[2]) && !m[2].startsWith('-')) {
    return { kind: 'emoji', prefix: m[2], start: m.index + m[1].length, end: caret }
  }
  return null
}

// applyCompletion replaces the typed trigger word with text and one space
// (none added when a space already follows); the caret lands after it.
export function applyCompletion(value: string, t: Pick<Trigger, 'start' | 'end'>, text: string): { value: string; caret: number } {
  const after = value.slice(t.end)
  const insert = /^\s/.test(after) ? text : text + ' '
  // Either way the caret lands past the space that now follows.
  return { value: value.slice(0, t.start) + insert + after, caret: t.start + text.length + 1 }
}

export type ACGroup = 'members' | 'special' | 'others' | 'myChannels' | 'otherChannels' | 'emoji' | 'commands'

export type ACItem =
  | { type: 'user'; key: string; group: 'members' | 'others'; user: ACUser }
  | { type: 'special'; key: string; group: 'special'; name: 'here' | 'channel' | 'all' }
  | { type: 'channel'; key: string; group: 'myChannels' | 'otherChannels'; channel: ACChannel }
  | { type: 'emoji'; key: string; group: 'emoji'; name: string; char: string | null; custom: boolean }
  | { type: 'command'; key: string; group: 'commands'; command: ACCommand }

// completionText: what picking the row inserts (a space follows).
export function completionText(i: ACItem): string {
  switch (i.type) {
    case 'user':
      return '@' + i.user.username
    case 'special':
      return '@' + i.name
    case 'channel':
      return '~' + i.channel.name
    case 'emoji':
      return ':' + i.name + ':'
    case 'command':
      return '/' + i.command.trigger
  }
}

const SPECIAL = ['here', 'channel', 'all'] as const

// buildItems: the rows for users/channels/commands (emoji: emojiItems).
// Users as the webapp orders them: channel members, special mentions
// matching the prefix, members outside the channel.
export function buildItems(t: Trigger, dto: AutocompleteDTO | null): ACItem[] {
  const out: ACItem[] = []
  switch (t.kind) {
    case 'users': {
      for (const u of dto?.users ?? []) out.push({ type: 'user', key: 'u:' + u.id, group: 'members', user: u })
      const p = t.prefix.toLowerCase()
      for (const name of SPECIAL) if (name.startsWith(p)) out.push({ type: 'special', key: 's:' + name, group: 'special', name })
      for (const u of dto?.others ?? []) out.push({ type: 'user', key: 'o:' + u.id, group: 'others', user: u })
      break
    }
    case 'channels':
      for (const c of dto?.channels ?? []) {
        out.push({ type: 'channel', key: 'c:' + c.id, group: c.joined ? 'myChannels' : 'otherChannels', channel: c })
      }
      break
    case 'commands':
      for (const c of dto?.commands ?? []) out.push({ type: 'command', key: 'k:' + c.trigger, group: 'commands', command: c })
      break
  }
  return out
}

const MAX_EMOJI = 50

// thumbs up before thumbs down (the webapp's customRules).
const THUMBS: Record<string, string[]> = { thumbsup: ['thumbsdown', '-1'], '+1': ['thumbsdown', '-1'] }

// emojiItems: standard emoji (by the first alias containing partial — the
// webapp matches anywhere in the name) and custom ones (a standard name
// wins), recent ones first, then names starting with partial, then
// standard before custom, then by name (webapp compareEmojis).
export function emojiItems(partial: string, idx: EmojiIndex | null, custom: string[], recent: string[]): (ACItem & { type: 'emoji' })[] {
  const q = partial.toLowerCase()
  const cands: { item: ACItem & { type: 'emoji' }; recent: boolean }[] = []
  for (const c of idx?.categories ?? []) {
    for (const e of c.emojis) {
      const name = e.names.find((n) => n.includes(q))
      if (!name) continue
      cands.push({
        item: { type: 'emoji', key: 'e:' + name, group: 'emoji', name, char: e.char, custom: false },
        recent: e.names.some((n) => recent.includes(n)),
      })
    }
  }
  const seen = new Set<string>()
  for (const name of custom) {
    if (!name.includes(q) || seen.has(name) || idx?.byName.has(name)) continue
    seen.add(name)
    cands.push({ item: { type: 'emoji', key: 'e:' + name, group: 'emoji', name, char: null, custom: true }, recent: recent.includes(name) })
  }
  cands.sort((a, b) => {
    if (a.recent !== b.recent) return a.recent ? -1 : 1
    const an = a.item.name
    const bn = b.item.name
    const ap = an.startsWith(q)
    const bp = bn.startsWith(q)
    if (ap !== bp) return ap ? -1 : 1
    if (THUMBS[an]?.includes(bn)) return -1
    if (THUMBS[bn]?.includes(an)) return 1
    if (a.item.custom !== b.item.custom) return a.item.custom ? 1 : -1
    return an.localeCompare(bn)
  })
  return cands.slice(0, MAX_EMOJI).map((c) => c.item)
}
