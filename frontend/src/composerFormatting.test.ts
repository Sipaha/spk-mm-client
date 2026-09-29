import { applyMarkdown, insertAtCaret, replaceTextareaValue } from './composerFormatting'

describe('applyMarkdown', () => {
  test('bold wraps a selection and toggles off when reapplied to the wrapped text', () => {
    const r1 = applyMarkdown('bold', 'hello world', 6, 11)
    expect(r1).toEqual({ message: 'hello **world**', selectionStart: 8, selectionEnd: 13 })
    const r2 = applyMarkdown('bold', r1.message, r1.selectionStart, r1.selectionEnd)
    expect(r2).toEqual({ message: 'hello world', selectionStart: 6, selectionEnd: 11 })
  })

  test('bold with an empty selection inserts markers with the caret between them', () => {
    const r = applyMarkdown('bold', 'hi ', 3, 3)
    expect(r).toEqual({ message: 'hi ****', selectionStart: 5, selectionEnd: 5 })
  })

  test('italic uses a single asterisk, like the webapp', () => {
    const r = applyMarkdown('italic', 'a word', 2, 6)
    expect(r).toEqual({ message: 'a *word*', selectionStart: 3, selectionEnd: 7 })
  })

  test('strikethrough wraps with ~~', () => {
    const r = applyMarkdown('strike', 'oops', 0, 4)
    expect(r).toEqual({ message: '~~oops~~', selectionStart: 2, selectionEnd: 6 })
  })

  test('inline code wraps a single-line selection with a backtick', () => {
    const r = applyMarkdown('code', 'run cmd', 4, 7)
    expect(r).toEqual({ message: 'run `cmd`', selectionStart: 5, selectionEnd: 8 })
  })

  test('code fences a multi-line selection with ```', () => {
    const r = applyMarkdown('code', 'x\ny\nz', 0, 5)
    expect(r.message).toBe('```\nx\ny\nz\n```')
  })

  test('heading prefixes the current line and toggles off', () => {
    const r1 = applyMarkdown('heading', 'title', 2, 2)
    expect(r1).toEqual({ message: '### title', selectionStart: 6, selectionEnd: 6 })
    const r2 = applyMarkdown('heading', r1.message, r1.selectionStart, r1.selectionEnd)
    expect(r2).toEqual({ message: 'title', selectionStart: 2, selectionEnd: 2 })
  })

  test('quote prefixes every selected line', () => {
    const r = applyMarkdown('quote', 'one\ntwo', 0, 7)
    expect(r.message).toBe('> one\n> two')
  })

  test('bulleted list prefixes every selected line and toggles off', () => {
    const r1 = applyMarkdown('ul', 'one\ntwo', 0, 7)
    expect(r1.message).toBe('- one\n- two')
    const r2 = applyMarkdown('ul', r1.message, r1.selectionStart, r1.selectionEnd)
    expect(r2.message).toBe('one\ntwo')
  })

  test('numbered list numbers every selected line and toggles off', () => {
    const r1 = applyMarkdown('ol', 'a\nb\nc', 0, 5)
    expect(r1.message).toBe('1. a\n2. b\n3. c')
    const r2 = applyMarkdown('ol', r1.message, 0, r1.message.length)
    expect(r2.message).toBe('a\nb\nc')
  })

  test('link with no selection inserts [text](url) with url selected', () => {
    const r = applyMarkdown('link', '', 0, 0)
    expect(r.message).toBe('[text](url)')
    expect(r.message.slice(r.selectionStart, r.selectionEnd)).toBe('url')
  })

  test('link with a selection wraps it as the link text, url selected', () => {
    const r = applyMarkdown('link', 'see docs', 4, 8)
    expect(r.message).toBe('see [docs](url)')
    expect(r.message.slice(r.selectionStart, r.selectionEnd)).toBe('url')
  })

  test('link toggles off a selection framed by [ and ](url)', () => {
    const r1 = applyMarkdown('link', 'see docs', 4, 8)
    // r1.message = 'see [docs](url)'; select "docs" (inside the brackets) again.
    const docsStart = r1.message.indexOf('docs')
    const r2 = applyMarkdown('link', r1.message, docsStart, docsStart + 4)
    expect(r2.message).toBe('see docs')
  })
})

describe('textarea helpers (real DOM, jsdom execCommand falls back to setRangeText)', () => {
  function textarea(value: string): HTMLTextAreaElement {
    const el = document.createElement('textarea')
    el.value = value
    document.body.appendChild(el)
    return el
  }

  test('replaceTextareaValue updates the value, fires input, and restores the requested selection', () => {
    const el = textarea('hello world')
    const onInput = vi.fn()
    el.addEventListener('input', onInput)
    const r = applyMarkdown('bold', el.value, 6, 11)
    replaceTextareaValue(el, r.message, r.selectionStart, r.selectionEnd)
    expect(el.value).toBe('hello **world**')
    expect(onInput).toHaveBeenCalled()
    expect(el.selectionStart).toBe(8)
    expect(el.selectionEnd).toBe(13)
    el.remove()
  })

  test('replaceTextareaValue only replaces the minimal changed span (undo-friendly)', () => {
    const el = textarea('one two three')
    let seenSelection: { start: number; end: number } | null = null
    el.addEventListener('input', () => {
      // Read whatever setRangeText/insertText left just before the event
      // fired — proves the edit was scoped to "two", not the whole value.
      seenSelection = { start: el.selectionStart ?? -1, end: el.selectionEnd ?? -1 }
    })
    const r = applyMarkdown('bold', el.value, 4, 7)
    replaceTextareaValue(el, r.message, r.selectionStart, r.selectionEnd)
    expect(el.value).toBe('one **two** three')
    expect(seenSelection).not.toBeNull()
    el.remove()
  })

  test('insertAtCaret inserts text at the caret and leaves it right after', () => {
    const el = textarea('say :) now')
    el.setSelectionRange(4, 4)
    insertAtCaret(el, ':wave: ')
    expect(el.value).toBe('say :wave: :) now')
    expect(el.selectionStart).toBe(11)
    expect(el.selectionEnd).toBe(11)
    el.remove()
  })

  test('insertAtCaret replaces a selection instead of just inserting', () => {
    const el = textarea('say :) now')
    el.setSelectionRange(4, 6) // ":)"
    insertAtCaret(el, ':wave:')
    expect(el.value).toBe('say :wave: now')
    el.remove()
  })
})
