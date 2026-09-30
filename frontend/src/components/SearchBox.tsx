import { useCallback, useEffect, useId, useLayoutEffect, useRef, useState } from 'react'
import { client } from '../api/client'
import type { ACChannel, ACUser } from '../api/types'
import { applyCompletion } from '../autocomplete'
import { submitSearch } from '../chat'
import { t } from '../i18n'
import { isShortcut } from '../keyboard'
import { useStore } from '../store'
import { Avatar } from './Avatar'
import { ChannelTypeMarker, IconSearch } from './icons'
import { AUTOCOMPLETE_DEBOUNCE } from './useAutocomplete'

// SearchToken: the from:/in: filter the caret is in (spec «Поиск»,
// Секция 2) — lead is what stays ("-from:", "in:"…), prefix what the
// caret has typed of its value, start/end the whole token's bounds.
export interface SearchToken {
  kind: 'users' | 'channels'
  lead: string
  prefix: string
  start: number
  end: number
}

// findSearchToken: the whitespace-delimited token around the caret, if it
// is a from:/in:/channel: filter (negated too); the value typed so far is
// the text between its colon and the caret.
export function findSearchToken(value: string, caret: number): SearchToken | null {
  let start = caret
  while (start > 0 && !/\s/.test(value[start - 1])) start--
  let end = caret
  while (end < value.length && !/\s/.test(value[end])) end++
  const m = /^(-?(from|in|channel):)(.*)$/i.exec(value.slice(start, caret))
  if (!m) return null
  return { kind: m[2].toLowerCase() === 'from' ? 'users' : 'channels', lead: m[1], prefix: m[3], start, end }
}

type Row = { key: string; kind: 'user'; user: ACUser } | { key: string; kind: 'channel'; channel: ACChannel }

// insertText: what a row puts after the filter's colon — a username
// (from:bob), or what in: takes: a channel's slug, "@bob" for a DM,
// "@a,b,c" for a GM (Go's searchSuggest names them so).
const insertText = (r: Row) => (r.kind === 'user' ? r.user.username : r.channel.name)

function SuggestRow({ serverId, row }: { serverId: number; row: Row }) {
  if (row.kind === 'user') {
    const u = row.user
    const extra = [u.full_name, u.nickname ? `(${u.nickname})` : ''].filter(Boolean).join(' ')
    return (
      <>
        <Avatar serverId={serverId} userId={u.id} version={u.avatar} name={u.username} status={u.status} size={24} surface="app" />
        <span className="font-semibold">@{u.username}</span>
        {extra && <span className="truncate text-fg-muted">{extra}</span>}
        {u.me && <span className="text-fg-muted">{t('autocomplete.you')}</span>}
      </>
    )
  }
  const c = row.channel
  const direct = c.type === 'D' || c.type === 'G'
  return (
    <>
      <span className="flex h-6 w-6 shrink-0 items-center justify-center text-fg-muted">
        <ChannelTypeMarker type={c.type} size={16} />
      </span>
      <span className="truncate font-semibold">{c.display_name || c.name}</span>
      <span className="truncate text-fg-muted">{direct ? c.name : `~${c.name}`}</span>
    </>
  )
}

interface Props {
  // header: the field in the channel's header (owns Ctrl+F); pane: the
  // results panel's own, over the feed in a narrow window.
  variant: 'header' | 'pane'
}

// SearchBox is the message search field (role=combobox): Enter searches the
// team on screen; from:/in: under the caret suggest users/channels (Go's
// searchSuggest — debounced, each request numbered and aborted by the
// next, like the composer's useAutocomplete); ↑/↓ move, Enter/Tab insert.
// Esc: closes the suggestions, then clears the text, then leaves the field
// — each step taken (preventDefault), so the panel under it stays open.
export function SearchBox({ variant }: Props) {
  const serverId = useStore((s) => s.selectedId)
  const teamId = useStore((s) => s.sidebar?.team_id ?? '')
  const draft = useStore((s) => s.searchDraft)
  const setDraft = useStore((s) => s.setSearchDraft)
  const inputRef = useRef<HTMLInputElement>(null)
  const listId = useId()
  const [focused, setFocused] = useState(false)
  const [open, setOpen] = useState<{ token: SearchToken; rows: Row[]; active: number } | null>(null)
  const seq = useRef(0)
  const ctl = useRef<AbortController | null>(null)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const current = useRef<SearchToken | null>(null)
  // dismissed: Esc closed the list for the token starting here — it stays
  // closed while that token is typed on.
  const dismissed = useRef<number | null>(null)

  const stop = useCallback(() => {
    clearTimeout(timer.current)
    ctl.current?.abort()
    ctl.current = null
  }, [])
  const close = useCallback(() => {
    stop()
    seq.current++
    current.current = null
    setOpen(null)
  }, [stop])
  useEffect(() => stop, [stop])

  // Ctrl+F (the physical F — keyboard.ts) focuses the search: the results
  // panel's own field when it overlays the feed, else this one. Not while
  // a viewer (any, PDF too) or another modal is open — they have their own
  // Ctrl+F or none — nor when someone already took the key.
  useEffect(() => {
    if (variant !== 'header') return
    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented || !isShortcut(e, 'KeyF', { ctrl: true, shift: false }) || e.altKey) return
      if (document.querySelector('[aria-modal="true"]')) return
      e.preventDefault()
      const el = document.querySelector<HTMLInputElement>('input[data-search-input="pane"]') ?? inputRef.current
      el?.focus()
      el?.select()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [variant])

  const update = (value: string, caret: number | null) => {
    const tok = caret === null ? null : findSearchToken(value, caret)
    if (!tok || serverId === null || !teamId) {
      dismissed.current = null
      if (current.current) close()
      return
    }
    if (dismissed.current === tok.start) return
    dismissed.current = null
    const cur = current.current
    if (cur && cur.kind === tok.kind && cur.start === tok.start && cur.prefix === tok.prefix) {
      current.current = tok // the same word: its end may have moved
      return
    }
    stop()
    current.current = tok
    const id = ++seq.current
    timer.current = setTimeout(() => {
      const c = new AbortController()
      ctl.current = c
      client.searchSuggest(serverId, teamId, tok.kind, tok.prefix, c.signal).then(
        (dto) => {
          if (id !== seq.current) return
          const rows: Row[] =
            tok.kind === 'users'
              ? [...(dto.users ?? []), ...(dto.others ?? [])].map((u) => ({ key: 'u:' + u.id, kind: 'user' as const, user: u }))
              : (dto.channels ?? []).map((ch) => ({ key: 'c:' + ch.id, kind: 'channel' as const, channel: ch }))
          setOpen(rows.length > 0 ? { token: current.current ?? tok, rows, active: 0 } : null)
        },
        () => {}, // offline/refused/aborted: no list
      )
    }, AUTOCOMPLETE_DEBOUNCE)
  }

  // The caret after an insertion lands once React has put the new value in.
  const pendingCaret = useRef<number | null>(null)
  useLayoutEffect(() => {
    const c = pendingCaret.current
    if (c === null) return
    pendingCaret.current = null
    inputRef.current?.setSelectionRange(c, c)
  }, [draft])

  const pick = (i: number) => {
    const el = inputRef.current
    const row = open?.rows[i]
    if (!el || !open || !row) return
    const tok = current.current ?? open.token
    const { value, caret } = applyCompletion(el.value, tok, tok.lead + insertText(row))
    close()
    pendingCaret.current = caret
    setDraft(value)
  }

  const onKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.nativeEvent.isComposing) return
    if (open) {
      const n = open.rows.length
      switch (e.key) {
        case 'ArrowDown':
          e.preventDefault()
          setOpen({ ...open, active: (open.active + 1) % n })
          return
        case 'ArrowUp':
          e.preventDefault()
          setOpen({ ...open, active: (open.active - 1 + n) % n })
          return
        case 'Enter':
        case 'Tab':
          if (e.shiftKey || e.ctrlKey || e.altKey || e.metaKey) break
          e.preventDefault()
          pick(open.active)
          return
        case 'Escape':
          e.preventDefault()
          dismissed.current = open.token.start
          close()
          return
      }
    }
    if (e.key === 'Enter') {
      e.preventDefault()
      close()
      void submitSearch(e.currentTarget.value)
    } else if (e.key === 'Escape') {
      e.preventDefault()
      if (e.currentTarget.value !== '') {
        close()
        setDraft('')
      } else {
        e.currentTarget.blur()
      }
    }
  }

  const optionId = (i: number) => `${listId}-opt-${i}`
  const wide = variant === 'pane' || focused || draft !== ''
  return (
    <div className={`relative ${variant === 'pane' ? 'w-full' : `min-w-24 shrink transition-[width] duration-150 ${wide ? 'w-80' : 'w-56'}`}`}>
      <span className="pointer-events-none absolute inset-y-0 left-1.5 flex items-center text-fg-subtle">
        <IconSearch size={14} />
      </span>
      <input
        ref={inputRef}
        type="text"
        role="combobox"
        data-search-input={variant}
        aria-label={t('search.label')}
        placeholder={t('search.placeholder')}
        aria-autocomplete="list"
        aria-expanded={!!open}
        aria-controls={open ? listId : undefined}
        aria-activedescendant={open ? optionId(open.active) : undefined}
        spellCheck={false}
        autoComplete="off"
        value={draft}
        onChange={(e) => {
          setDraft(e.target.value)
          update(e.target.value, e.target.selectionStart)
        }}
        onSelect={(e) => update(e.currentTarget.value, e.currentTarget.selectionStart)}
        onKeyDown={onKeyDown}
        onFocus={() => {
          setFocused(true)
          // A kept session hidden (narrow: its overlay left for the
          // channel) shows again when the search is taken up again.
          const s = useStore.getState()
          if (variant === 'header' && s.search && s.rhs === null) s.setRhs('search')
        }}
        onBlur={() => {
          setFocused(false)
          close()
        }}
        className="h-6 w-full rounded border border-line bg-app pl-6 pr-2 text-sm text-fg placeholder:text-fg-subtle focus:border-accent focus:outline-none"
      />
      {open && serverId !== null && (
        <div
          id={listId}
          role="listbox"
          aria-label={t('search.suggestions')}
          onMouseDown={(e) => e.preventDefault()}
          className="absolute left-0 top-full z-30 mt-1 max-h-72 w-full min-w-64 overflow-y-auto rounded-lg border border-line bg-panel py-1 text-sm shadow-lg"
        >
          {open.rows.map((row, i) => (
            <div
              key={row.key}
              id={optionId(i)}
              role="option"
              aria-selected={i === open.active}
              onClick={() => pick(i)}
              onMouseMove={() => i !== open.active && setOpen({ ...open, active: i })}
              className={`flex cursor-pointer items-center gap-2 px-3 py-1 text-fg ${i === open.active ? 'bg-hover' : ''}`}
            >
              <SuggestRow serverId={serverId} row={row} />
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
