import { useCallback, useEffect, useId, useRef, useState, type RefObject } from 'react'
import { client } from '../api/client'
import type { AutocompleteDTO, EmojiDTO } from '../api/types'
import { applyCompletion, buildItems, completionText, emojiItems, findTrigger, type ACItem, type Trigger } from '../autocomplete'
import { replaceTextareaValue } from '../composerFormatting'
import { loadEmojiIndex, type EmojiIndex } from '../emoji/index'

// DEBOUNCE: the server is asked once typing pauses this long (brief: ~150 ms).
export const AUTOCOMPLETE_DEBOUNCE = 150

export interface AutocompleteState {
  trigger: Trigger
  items: ACItem[]
  active: number
}

interface Options {
  serverId: number
  channelId: string
  rootId: string
  textareaRef: RefObject<HTMLTextAreaElement | null>
  emojiInfo(): Promise<EmojiDTO>
}

const sameTrigger = (a: Trigger | null, b: Trigger) => !!a && a.kind === b.kind && a.start === b.start && a.prefix === b.prefix

// useAutocomplete drives the composer's popup (brief 2026-09-29): the word
// at the caret (findTrigger) opens it; local rows (special mentions, the
// standard emoji set) show at once, the server's (through Go) once typing
// pauses AUTOCOMPLETE_DEBOUNCE ms. Every request carries a sequence number
// — an answer that is not the latest is dropped — and an AbortSignal: a
// newer word aborts the older request, which cancels it in Go too. A failed
// request leaves the local rows (often none): typing never waits on it.
export function useAutocomplete({ serverId, channelId, rootId, textareaRef, emojiInfo }: Options) {
  const listId = useId()
  const [state, setState] = useState<AutocompleteState | null>(null)
  const current = useRef<Trigger | null>(null)
  const seq = useRef(0)
  const ctl = useRef<AbortController | null>(null)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  // dismissed: Esc closed the popup for the word starting here — it stays
  // closed while that word is typed on.
  const dismissed = useRef<string | null>(null)
  const emoji = useRef<{ dto: EmojiDTO | null; idx: EmojiIndex | null }>({ dto: null, idx: null })
  const emojiInfoRef = useRef(emojiInfo)
  emojiInfoRef.current = emojiInfo

  const stop = useCallback(() => {
    clearTimeout(timer.current)
    timer.current = undefined
    ctl.current?.abort()
    ctl.current = null
  }, [])

  const close = useCallback(() => {
    stop()
    seq.current++
    current.current = null
    setState(null)
  }, [stop])

  useEffect(() => stop, [stop])

  // rowsFor: the rows of trigger t with the server's answer dto (null: not
  // yet, or failed).
  const rowsFor = (t: Trigger, dto: AutocompleteDTO | null): ACItem[] => {
    if (t.kind !== 'emoji') return buildItems(t, dto)
    const e = emoji.current
    const custom = [...(e.dto?.custom ?? []), ...(dto?.emoji ?? [])]
    return emojiItems(t.prefix, e.idx, custom, e.dto?.recent ?? [])
  }

  const show = (id: number, t: Trigger, items: ACItem[]) => {
    if (id !== seq.current) return
    setState(items.length > 0 ? { trigger: t, items, active: 0 } : null)
  }

  const update = (value: string, caret: number | null) => {
    const t = caret === null ? null : findTrigger(value, caret)
    if (!t) {
      dismissed.current = null
      if (current.current) close()
      return
    }
    if (dismissed.current === `${t.kind}:${t.start}`) return
    dismissed.current = null
    if (sameTrigger(current.current, t)) return
    stop()
    current.current = t
    const id = ++seq.current
    let answered = false
    let answer: AutocompleteDTO | null = null
    show(id, t, rowsFor(t, null))
    if (t.kind === 'emoji') {
      // The standard set and the recent/custom names are local: load them
      // once, then redraw whatever this word has so far.
      const e = emoji.current
      void Promise.all([
        e.idx ?? loadEmojiIndex().then((idx) => (e.idx = idx)),
        e.dto ??
          emojiInfoRef.current().then(
            (dto) => (e.dto = dto),
            () => null,
          ),
      ])
        .then(() => {
          if (id === seq.current) show(id, t, rowsFor(t, answered ? answer : null))
        })
        .catch(() => {}) // the set's chunk failed to load: the rows so far stay
    }
    timer.current = setTimeout(() => {
      const c = new AbortController()
      ctl.current = c
      client.autocomplete(serverId, t.kind, channelId, rootId, t.prefix, c.signal).then(
        (dto) => {
          if (id !== seq.current) return
          answered = true
          answer = dto
          show(id, t, rowsFor(t, dto))
        },
        () => {}, // offline/refused/aborted: the local rows stay
      )
    }, AUTOCOMPLETE_DEBOUNCE)
  }

  const pick = (i: number) => {
    const el = textareaRef.current
    const s = state
    if (!el || !s || !s.items[i]) return
    const { value, caret } = applyCompletion(el.value, s.trigger, completionText(s.items[i]))
    close()
    replaceTextareaValue(el, value, caret, caret)
  }

  const setActive = (i: number) => setState((s) => (s && i >= 0 && i < s.items.length ? { ...s, active: i } : s))

  // onKeyDown: true when the popup took the key (the composer must not act
  // on it — in particular Enter must not send while the popup is open).
  const onKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>): boolean => {
    if (!state || e.nativeEvent.isComposing) return false
    const n = state.items.length
    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault()
        setActive((state.active + 1) % n)
        return true
      case 'ArrowUp':
        e.preventDefault()
        setActive((state.active - 1 + n) % n)
        return true
      case 'Enter':
      case 'Tab':
        if (e.shiftKey || e.ctrlKey || e.altKey || e.metaKey) return false
        e.preventDefault()
        pick(state.active)
        return true
      case 'Escape':
        // preventDefault: the thread panel closes on an Escape nobody took.
        e.preventDefault()
        dismissed.current = `${state.trigger.kind}:${state.trigger.start}`
        close()
        return true
    }
    return false
  }

  const optionId = (i: number) => `${listId}-opt-${i}`
  const open = !!state
  const textareaProps = {
    'aria-autocomplete': 'list' as const,
    'aria-expanded': open,
    'aria-controls': open ? listId : undefined,
    'aria-activedescendant': open ? optionId(state.active) : undefined,
  }
  return { state, listId, optionId, update, close, pick, setActive, onKeyDown, textareaProps }
}
