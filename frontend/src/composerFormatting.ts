// composerFormatting: the composer's formatting-toolbar markdown logic
// (Composer brief 2026-09-29, scope 2) — pure string transforms plus a
// small textarea-mutation helper. Behaviour follows what the Mattermost
// webapp inserts (read for reference at
// .agents/tmp/mm-10.11/webapp/channels/src/utils/markdown/apply_markdown.ts;
// not copied — that file is a from-scratch reimplementation tuned to this
// component's needs): wrap the selection (or place the caret between fresh
// markers when nothing is selected), toggle the markup off when the
// selection already carries it, and apply line-prefix markers (heading,
// quote, bulleted/numbered list) to every line the selection touches.

export type MarkdownMode = 'bold' | 'italic' | 'strike' | 'code' | 'heading' | 'link' | 'quote' | 'ul' | 'ol'

export interface MarkdownResult {
  message: string
  selectionStart: number
  selectionEnd: number
}

// toggleWrap: bold/italic/strike/inline-code — surround the selection with
// delimiterStart/delimiterEnd, or strip them if the selection is already
// framed by exactly that pair. Empty selection: markers land around the
// caret, wich then sits between them ready to type.
function toggleWrap(message: string, start: number, end: number, delimiterStart: string, delimiterEnd: string = delimiterStart): MarkdownResult {
  const before = message.slice(0, start)
  const selected = message.slice(start, end)
  const after = message.slice(end)
  const alreadyWrapped = before.endsWith(delimiterStart) && after.startsWith(delimiterEnd)
  if (alreadyWrapped) {
    return {
      message: before.slice(0, before.length - delimiterStart.length) + selected + after.slice(delimiterEnd.length),
      selectionStart: start - delimiterStart.length,
      selectionEnd: end - delimiterStart.length,
    }
  }
  return {
    message: before + delimiterStart + selected + delimiterEnd + after,
    selectionStart: start + delimiterStart.length,
    selectionEnd: end + delimiterStart.length,
  }
}

const BOLD_DELIM = '**'
const ITALIC_DELIM = '*'

// applyBoldOrItalic: bold and italic share a delimiter character ('*'),
// which a plain toggleWrap cannot handle — '*' is a prefix of '**', so
// asking "does the selection already have my delimiter" for italic matches
// text that is only bold. Review fix round 1 (I1), reimplementing the
// webapp's own guard against this (apply_markdown.ts's
// applyBoldItalicMarkdown/isItalicFollowedByBold, not copied): italic
// right next to bold markers adds italic on top (**word** -> ***word***)
// instead of mistaking them for its own; toggling either mode off a
// bold+italic selection removes only that one layer.
function applyBoldOrItalic(mode: 'bold' | 'italic', message: string, start: number, end: number): MarkdownResult {
  const before = message.slice(0, start)
  const selected = message.slice(start, end)
  const after = message.slice(end)
  const delimiter = mode === 'bold' ? BOLD_DELIM : ITALIC_DELIM
  // Only italic can be confused with bold's markers (not the reverse: '**'
  // is not a prefix relationship problem for bold's own check).
  const italicFollowedByBold = mode === 'italic' && before.endsWith(BOLD_DELIM) && after.startsWith(BOLD_DELIM)
  const alreadyBoldAndItalic = before.endsWith(BOLD_DELIM + ITALIC_DELIM) && after.startsWith(BOLD_DELIM + ITALIC_DELIM)
  const hasThisMarkdown = before.endsWith(delimiter) && after.startsWith(delimiter)
  if (alreadyBoldAndItalic || (hasThisMarkdown && !italicFollowedByBold)) {
    return {
      message: before.slice(0, before.length - delimiter.length) + selected + after.slice(delimiter.length),
      selectionStart: start - delimiter.length,
      selectionEnd: end - delimiter.length,
    }
  }
  return {
    message: before + delimiter + selected + delimiter + after,
    selectionStart: start + delimiter.length,
    selectionEnd: end + delimiter.length,
  }
}

function countChar(text: string, ch: string): number {
  let n = 0
  for (const c of text) if (c === ch) n++
  return n
}

// lineBounds: the [start, end) of every whole line the selection touches,
// so a line-prefix marker (heading/quote/list) is applied per line, not
// just at the selection's own edges.
function lineBounds(message: string, start: number, end: number) {
  const blockStart = message.lastIndexOf('\n', Math.max(start - 1, 0)) + 1
  const nlAfterEnd = message.indexOf('\n', end)
  const blockEnd = nlAfterEnd === -1 ? message.length : nlAfterEnd
  return { blockStart, blockEnd }
}

// applyLinePrefix: heading ("### "), quote ("> "), bulleted list ("- ") —
// a fixed-length prefix on every touched line. Review fix round 1 (I2),
// reimplementing the webapp's own normalization (apply_markdown.ts's
// applyMarkdownToSelectedLines, not copied): only when *every* touched
// line already has the prefix does the action remove it from all of them;
// otherwise the prefix is added only to the lines missing it — a mixed
// selection (some lines already prefixed) never doubles the prefix on the
// ones that had it. Caret math tracks each line's own length delta (0 for
// a line left alone) rather than assuming a uniform per-line shift, the
// same technique applyOl below uses for its variable-width markers.
function applyLinePrefix(message: string, start: number, end: number, prefix: string): MarkdownResult {
  const { blockStart, blockEnd } = lineBounds(message, start, end)
  const block = message.slice(blockStart, blockEnd)
  const lines = block.split('\n')
  const allHavePrefix = lines.every((l) => l.startsWith(prefix))
  const newLines = allHavePrefix
    ? lines.map((l) => l.slice(prefix.length))
    : lines.map((l) => (l.startsWith(prefix) ? l : prefix + l))
  const newMessage = message.slice(0, blockStart) + newLines.join('\n') + message.slice(blockEnd)
  const deltas = lines.map((l, i) => newLines[i].length - l.length)
  const shiftUpTo = (pos: number) => {
    const idx = countChar(message.slice(blockStart, pos), '\n')
    let sum = 0
    for (let i = 0; i <= idx && i < deltas.length; i++) sum += deltas[i]
    return sum
  }
  return {
    message: newMessage,
    selectionStart: Math.max(blockStart, start + shiftUpTo(start)),
    selectionEnd: Math.max(blockStart, end + shiftUpTo(end)),
  }
}

// applyOl: numbered list — same idea as applyLinePrefix, but each line's
// marker ("1. ", "2. ", …) has its own length, so the caret shift is
// tracked per line rather than with one uniform delta.
function applyOl(message: string, start: number, end: number): MarkdownResult {
  const { blockStart, blockEnd } = lineBounds(message, start, end)
  const block = message.slice(blockStart, blockEnd)
  const lines = block.split('\n')
  const applied = lines.every((l) => /^\d+\.\s/.test(l))
  const newLines = applied ? lines.map((l) => l.replace(/^\d+\.\s/, '')) : lines.map((l, i) => `${i + 1}. ${l}`)
  const deltas = lines.map((l, i) => newLines[i].length - l.length)
  const newMessage = message.slice(0, blockStart) + newLines.join('\n') + message.slice(blockEnd)
  const shiftUpTo = (pos: number) => {
    const idx = countChar(message.slice(blockStart, pos), '\n')
    let sum = 0
    for (let i = 0; i <= idx && i < deltas.length; i++) sum += deltas[i]
    return sum
  }
  return {
    message: newMessage,
    selectionStart: Math.max(blockStart, start + shiftUpTo(start)),
    selectionEnd: Math.max(blockStart, end + shiftUpTo(end)),
  }
}

// applyCode: inline `code` for a single-line selection, a fenced ```
// block when the selection spans more than one line.
function applyCode(message: string, start: number, end: number): MarkdownResult {
  if (message.slice(start, end).includes('\n')) return toggleWrap(message, start, end, '```\n', '\n```')
  return toggleWrap(message, start, end, '`')
}

const LINK_PLACEHOLDER_URL = 'url'
const LINK_PLACEHOLDER_TEXT = 'text'

// applyLink: `[text](url)` with `url` selected, ready to be typed over —
// matches the webapp's placeholder scheme. Toggles off a selection framed
// by exactly `[` … `](url)` (i.e. re-clicking right after inserting it).
function applyLink(message: string, start: number, end: number): MarkdownResult {
  const before = message.slice(0, start)
  const selected = message.slice(start, end)
  const after = message.slice(end)
  const close = `](${LINK_PLACEHOLDER_URL})`
  if (before.endsWith('[') && after.startsWith(close)) {
    return {
      message: before.slice(0, -1) + selected + after.slice(close.length),
      selectionStart: start - 1,
      selectionEnd: end - 1,
    }
  }
  const text = selected || LINK_PLACEHOLDER_TEXT
  const newMessage = before + '[' + text + close + after
  const urlStart = before.length + 1 + text.length + 2 // '[' + text + ']('
  return { message: newMessage, selectionStart: urlStart, selectionEnd: urlStart + LINK_PLACEHOLDER_URL.length }
}

export function applyMarkdown(mode: MarkdownMode, message: string, start: number, end: number): MarkdownResult {
  switch (mode) {
    case 'bold':
      return applyBoldOrItalic('bold', message, start, end)
    case 'italic':
      return applyBoldOrItalic('italic', message, start, end)
    case 'strike':
      return toggleWrap(message, start, end, '~~')
    case 'code':
      return applyCode(message, start, end)
    case 'link':
      return applyLink(message, start, end)
    case 'heading':
      return applyLinePrefix(message, start, end, '### ')
    case 'quote':
      return applyLinePrefix(message, start, end, '> ')
    case 'ul':
      return applyLinePrefix(message, start, end, '- ')
    case 'ol':
      return applyOl(message, start, end)
  }
}

// replaceTextareaValue: applies a full before/after string pair to a live
// textarea as one minimal edit — diffing out the common prefix/suffix so
// only the actually-changed middle span is selected and replaced. That
// keeps the browser's own undo stack intact (spec: "keep native undo
// working where feasible"): `execCommand('insertText', …)` simulates a
// real keystroke, which WebKitGTK (and every other engine) records as one
// undoable step, exactly like typing over a selection. `setRangeText` is
// the fallback for engines where `execCommand` is unsupported/returns
// false (e.g. jsdom in tests) — it does not feed undo, so it fires its own
// `input` event by hand for React's onChange to pick up.
export function replaceTextareaValue(el: HTMLTextAreaElement, newValue: string, newSelectionStart: number, newSelectionEnd: number): void {
  const oldValue = el.value
  let prefixLen = 0
  const maxPrefix = Math.min(oldValue.length, newValue.length)
  while (prefixLen < maxPrefix && oldValue[prefixLen] === newValue[prefixLen]) prefixLen++
  let oldSuffixLen = 0
  while (
    oldSuffixLen < oldValue.length - prefixLen &&
    oldSuffixLen < newValue.length - prefixLen &&
    oldValue[oldValue.length - 1 - oldSuffixLen] === newValue[newValue.length - 1 - oldSuffixLen]
  ) {
    oldSuffixLen++
  }
  const selStart = prefixLen
  const selEnd = oldValue.length - oldSuffixLen
  const insertText = newValue.slice(prefixLen, newValue.length - oldSuffixLen)

  el.focus()
  el.setSelectionRange(selStart, selEnd)
  const applied = typeof document.execCommand === 'function' && document.execCommand('insertText', false, insertText)
  if (!applied) {
    el.setRangeText(insertText, selStart, selEnd, 'end')
    el.dispatchEvent(new Event('input', { bubbles: true }))
  }
  el.setSelectionRange(newSelectionStart, newSelectionEnd)
}

// insertAtCaret: emoji insertion — replaces the current selection (or just
// inserts at the caret) with `text`, same undo-friendly mechanism as
// replaceTextareaValue, and leaves the caret right after it.
export function insertAtCaret(el: HTMLTextAreaElement, text: string): void {
  const start = el.selectionStart ?? el.value.length
  const end = el.selectionEnd ?? el.value.length
  el.focus()
  el.setSelectionRange(start, end)
  const applied = typeof document.execCommand === 'function' && document.execCommand('insertText', false, text)
  if (!applied) {
    el.setRangeText(text, start, end, 'end')
    el.dispatchEvent(new Event('input', { bubbles: true }))
  }
}
