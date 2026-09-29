import { useEffect, useId, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import type { ReactionUsersDTO, ReactionView } from '../api/types'
import { emojiChar, useEmojiIndex } from '../emoji'
import { t } from '../i18n'
import { getCachedReactors, setCachedReactors } from '../reactorCache'
import { reactorWhoParts } from '../reactorText'
import { EmojiGlyph } from './EmojiGlyph'
import { placeBelow } from './panelPosition'
import { ReactorsModal } from './ReactorsModal'
import { IconAddReaction } from './icons'

interface Props {
  serverId: number
  postId: string
  reactions: ReactionView[]
  me: { id: string; avatar?: string }
  onToggle(r: ReactionView): void
  onAdd?(trigger: HTMLElement): void // the "add reaction" chip opens the picker next to itself
  loadReactors(postId: string, emoji: string): Promise<ReactionUsersDTO>
}

const HOVER_DELAY = 300 // ms before a hover/focus fetches and shows the tooltip
const LEAVE_GRACE = 150 // ms grace before hiding, so the pointer can cross into the tooltip
const TOOLTIP_W = 280

// FOCUSABLE_SELECTOR: for finding "the next focusable element after the
// chip" (onOverflowKeyDown below) — a standard, good-enough tab-order
// approximation (no positive tabindex anywhere in this app).
const FOCUSABLE_SELECTOR = 'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

// Reactions: a chip per emoji; clicking toggles our own reaction with the
// chip's own name (aliases stay separate, as on the server). Hovering or
// focusing a chip shows a "who reacted" tooltip (one instance for the
// whole list, portalled) after a short delay; its overflow ("and N
// others") is a real button that opens the full list in a modal — see
// ReactorsModal. Positions/timers live here, not per chip, so there is
// never more than one hover timer or leave timer at a time.
export function Reactions({ serverId, postId, reactions, me, onToggle, onAdd, loadReactors }: Props) {
  const idx = useEmojiIndex(reactions.some((r) => !emojiChar(r.emoji, null)))
  const tooltipId = useId()
  const [hover, setHover] = useState<{ r: ReactionView; anchor: HTMLElement } | null>(null)
  const [result, setResult] = useState<ReactionUsersDTO | 'loading' | null>(null)
  const [modal, setModal] = useState<{ r: ReactionView; anchor: HTMLElement; dto: ReactionUsersDTO } | null>(null)
  const hoverTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const leaveTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const reqID = useRef(0)
  const tooltipRoot = useRef<HTMLDivElement>(null)
  const overflowBtn = useRef<HTMLButtonElement | null>(null)

  const clearTimers = () => {
    clearTimeout(hoverTimer.current)
    clearTimeout(leaveTimer.current)
  }

  const hide = () => {
    clearTimers()
    setHover(null)
    setResult(null)
  }

  // Esc and scroll close the tooltip — a single window-level listener,
  // attached only while it is actually shown (not "per chip": one tooltip
  // instance for the whole reaction list). Scroll doesn't bubble, so this
  // needs the capture phase to see it fire on the feed's scroll container.
  // Inlined rather than calling hide() so the effect only depends on
  // `hover` itself (setHover/setResult/clearTimers are all stable).
  useEffect(() => {
    if (!hover) return
    const close = () => {
      clearTimeout(hoverTimer.current)
      clearTimeout(leaveTimer.current)
      setHover(null)
      setResult(null)
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') close()
    }
    window.addEventListener('keydown', onKey)
    window.addEventListener('scroll', close, true)
    return () => {
      window.removeEventListener('keydown', onKey)
      window.removeEventListener('scroll', close, true)
    }
  }, [hover])

  // If the reaction behind an already-open (or loading) tooltip disappears
  // — the user's own click removed their last reaction, or it arrived
  // removed over the wire while shown — its chip unmounts too, and (same
  // reasoning as the isConnected guard in show() below) nothing else would
  // ever close the tooltip: the chip's own mouseleave/blur can't fire for
  // an element that's no longer in the document.
  useEffect(() => {
    if (hover && !reactions.some((r) => r.emoji === hover.r.emoji)) hide()
  }, [reactions, hover])

  const show = (r: ReactionView, anchor: HTMLElement) => {
    clearTimers()
    // The chip can vanish between scheduling a hover/focus show and the
    // delayed timer firing — e.g. the user clicks it to toggle off their
    // only reaction, the count reaches zero, and the whole chip unmounts
    // before HOVER_DELAY elapses. Showing anyway would anchor the tooltip
    // to a detached node: getBoundingClientRect() on a node with no layout
    // box is always {0,0,0,0}, so placeBelow lands it at the viewport's
    // top-left corner — and nothing would ever close it, since its own
    // mouseleave/blur can't fire for an element no longer in the document
    // (e2e-bisected regression, 2026-09-29: this exact stuck tooltip ate a
    // click meant for the sidebar's "Server menu" button once the rail's
    // hiding shifted the button under the tooltip's fixed position).
    if (!anchor.isConnected) return
    setHover({ r, anchor })
    const cached = getCachedReactors(serverId, postId, r.emoji, r.count)
    if (cached) {
      setResult(cached)
      return
    }
    setResult('loading')
    const myReq = ++reqID.current
    loadReactors(postId, r.emoji).then(
      (dto) => {
        if (reqID.current !== myReq) return // a later hover/emoji superseded this one
        setCachedReactors(serverId, postId, r.emoji, r.count, dto)
        setResult(dto)
      },
      () => {
        if (reqID.current !== myReq) return
        hide() // a failed fetch never leaves a stuck "…" — just closes quietly
      },
    )
  }

  const scheduleShow = (r: ReactionView, anchor: HTMLElement) => {
    clearTimers()
    hoverTimer.current = setTimeout(() => show(r, anchor), HOVER_DELAY)
  }

  const scheduleHide = () => {
    clearTimeout(hoverTimer.current)
    leaveTimer.current = setTimeout(hide, LEAVE_GRACE)
  }

  const cancelHide = () => clearTimeout(leaveTimer.current)

  // onChipBlur: a blur that moves focus into the tooltip itself (the Tab
  // hand-off below) must not close it — only a blur leaving both behind
  // does.
  const onChipBlur = (e: React.FocusEvent<HTMLButtonElement>) => {
    const next = e.relatedTarget as Node | null
    if (next && tooltipRoot.current?.contains(next)) return
    hide()
  }

  // onChipKeyDown: the tooltip is portalled into document.body, so the
  // document's Tab order does not naturally lead from a focused chip into
  // its own tooltip's button — this is the documented equivalent
  // accessible path (UI ruling, 2026-09-28): Tab (not Shift+Tab) while the
  // tooltip for this chip is showing and has an overflow button moves
  // focus straight into it, instead of wherever Tab would otherwise go.
  const onChipKeyDown = (r: ReactionView) => (e: React.KeyboardEvent<HTMLButtonElement>) => {
    if (e.key === 'Tab' && !e.shiftKey && hover?.r.emoji === r.emoji && overflowBtn.current) {
      e.preventDefault()
      overflowBtn.current.focus()
    }
  }

  // onOverflowKeyDown: symmetric to onChipKeyDown, and completing the same
  // "portal breaks the document's tab order" fix (fix round 2, UI ruling):
  // Shift+Tab from the overflow button goes straight back to the chip
  // (mirroring the chip's own forward hand-off), and a plain Tab moves to
  // whatever real tab order says is next *after the chip* — never off to
  // the end of the document, where the portal's button would otherwise
  // physically sit in document order.
  const onOverflowKeyDown = (e: React.KeyboardEvent<HTMLButtonElement>) => {
    if (e.key !== 'Tab' || !hover) return
    e.preventDefault()
    if (e.shiftKey) {
      hover.anchor.focus()
      return
    }
    const chain = [...document.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR)].filter((el) => !tooltipRoot.current?.contains(el))
    const i = chain.indexOf(hover.anchor)
    if (i >= 0) chain[i + 1]?.focus()
  }

  // onTooltipBlur: now that focus can leave the tooltip programmatically
  // (Tab past the overflow button, above) rather than only by the pointer
  // leaving, the tooltip needs the same "hidden on blur" rule the chip
  // already has — closing once focus lands outside both the tooltip and
  // its chip.
  const onTooltipBlur = (e: React.FocusEvent<HTMLDivElement>) => {
    const next = e.relatedTarget as Node | null
    if (next && (tooltipRoot.current?.contains(next) || hover?.anchor.contains(next))) return
    hide()
  }

  const openModal = () => {
    if (!hover || !result || result === 'loading') return
    setModal({ r: hover.r, anchor: hover.anchor, dto: result })
    hide()
  }

  const closeModal = () => setModal(null)

  const tooltipPos = hover ? placeBelow(hover.anchor.getBoundingClientRect(), window.innerWidth, window.innerHeight, TOOLTIP_W, 60) : { left: 0, top: 0 }

  return (
    <div className="mt-1 flex flex-wrap items-center gap-1">
      {reactions.map((r) => {
        const label = emojiChar(r.emoji, idx) ?? `:${r.emoji}:`
        const showingTooltip = hover?.r.emoji === r.emoji
        return (
          <button
            key={r.emoji}
            type="button"
            aria-pressed={r.mine}
            aria-label={t(r.mine ? 'reaction.chipMine' : 'reaction.chip', { emoji: label, n: String(r.count) })}
            aria-describedby={showingTooltip ? tooltipId : undefined}
            onClick={() => onToggle(r)}
            onMouseEnter={(e) => scheduleShow(r, e.currentTarget)}
            onMouseLeave={scheduleHide}
            onFocus={(e) => scheduleShow(r, e.currentTarget)}
            onBlur={onChipBlur}
            onKeyDown={onChipKeyDown(r)}
            className={`flex h-7 items-center gap-1.5 rounded-full border px-2 text-sm ${r.mine ? 'border-accent bg-accent/15 text-fg' : 'border-line text-fg-muted hover:border-fg-subtle'}`}
          >
            <EmojiGlyph serverId={serverId} name={r.emoji} size={18} />
            <span>{r.count}</span>
          </button>
        )
      })}
      {onAdd && (
        <button
          type="button"
          aria-label={t('reaction.add')}
          title={t('reaction.add')}
          onClick={(e) => onAdd(e.currentTarget)}
          className="flex h-7 w-9 items-center justify-center rounded-full border border-line text-fg-muted hover:bg-hover"
        >
          <IconAddReaction size={18} />
        </button>
      )}
      {hover &&
        result &&
        createPortal(
          <div
            ref={tooltipRoot}
            id={tooltipId}
            role="tooltip"
            onMouseEnter={cancelHide}
            onMouseLeave={scheduleHide}
            onBlur={onTooltipBlur}
            className="fixed z-50 max-w-xs rounded-md border border-line bg-panel px-2.5 py-1.5 text-xs text-fg shadow-xl"
            style={{ left: tooltipPos.left, top: tooltipPos.top, width: TOOLTIP_W }}
          >
            {result === 'loading' ? (
              t('reaction.loading')
            ) : (
              <TooltipBody
                dto={result}
                mine={hover.r.mine}
                onShowMore={openModal}
                onButtonKeyDown={onOverflowKeyDown}
                buttonRef={overflowBtn}
              />
            )}
          </div>,
          document.body,
        )}
      {modal &&
        createPortal(
          <ReactorsModal
            serverId={serverId}
            emoji={modal.r.emoji}
            count={modal.r.count}
            mine={modal.r.mine}
            meId={me.id}
            meAvatar={me.avatar ?? ''}
            users={modal.dto.users}
            anchorEl={modal.anchor}
            onClose={closeModal}
          />,
          document.body,
        )}
    </div>
  )
}

// TooltipBody renders just the reactor names — "You, bob and carol" — with
// the overflow tail ("and N others" / "и N других") as a real <button>; the
// rest stays plain text. No "reacted with :emoji:" suffix (UI ruling,
// 2026-09-29, user request: the user already hovered/focused this specific
// chip to get here, so which emoji it is is redundant — they just want the
// list of people). Accessibility is preserved elsewhere: the chip's own
// aria-label (reaction.chip/reaction.chipMine — "{emoji} {n}[, you
// reacted]") is the one place that still names the emoji for assistive
// tech, and this tooltip stays wired to the chip via aria-describedby, so
// nothing is lost by dropping the emoji from the tooltip's own text.
function TooltipBody({
  dto,
  mine,
  onShowMore,
  onButtonKeyDown,
  buttonRef,
}: {
  dto: ReactionUsersDTO
  mine: boolean
  onShowMore(): void
  onButtonKeyDown(e: React.KeyboardEvent<HTMLButtonElement>): void
  buttonRef: React.RefObject<HTMLButtonElement | null>
}) {
  // Only resolvable names are shown by name in the tooltip text; every
  // unknown reactor (dto.unknown, whatever its position in the reaction
  // order) folds into the overflow tail instead of a placeholder inline —
  // the modal is where an individual "Unknown user" row belongs.
  const names = dto.users.filter((u) => u.name).map((u) => u.name)
  const { shown, overflowLabel } = reactorWhoParts({ mine, names, unknown: dto.unknown })
  const items: { text: string; button?: boolean }[] = shown.map((s) => ({ text: s }))
  if (overflowLabel) items.push({ text: overflowLabel, button: true })
  return (
    <>
      {items.map((it, i) => (
        <span key={i}>
          {i > 0 && (i === items.length - 1 ? ` ${t('reaction.and')} ` : ', ')}
          {it.button ? (
            <button type="button" ref={buttonRef} className="underline hover:no-underline" onClick={onShowMore} onKeyDown={onButtonKeyDown}>
              {it.text}
            </button>
          ) : (
            it.text
          )}
        </span>
      ))}
    </>
  )
}
