import { useEffect, useId, useRef, useState } from 'react'
import type { AppInfo } from '../api/types'
import { client, isDesktop } from '../api/client'
import { getLocale, t } from '../i18n'

/** Product information overlays the chat; it never replaces its mounted state. */
export function AboutDialog({ info, onClose }: { info: AppInfo; onClose(): void }) {
  const title = useId()
  const box = useRef<HTMLElement>(null)
  const close = useRef<HTMLButtonElement>(null)
  const [previous] = useState(() => document.activeElement as HTMLElement | null)
  const [error, setError] = useState('')
  const alive = useRef(true)
  const locale = getLocale()
  const suffix = locale === 'ru' ? '?lang=ru' : 'en/'
  useEffect(() => {
    alive.current = true
    close.current?.focus()
    return () => {
      alive.current = false
      if (previous?.isConnected) previous.focus()
    }
  }, [previous])
  const external = (event: React.MouseEvent<HTMLAnchorElement>) => {
    if (!isDesktop()) return
    event.preventDefault()
    setError('')
    void client.openURL(event.currentTarget.href).catch((failure: unknown) => {
      if (alive.current) setError(t('about.linkFailed', { detail: failure instanceof Error ? failure.message : String(failure) }))
    })
  }
  const link = 'text-accent underline-offset-4 hover:underline focus-visible:underline'
  return <div className="fixed inset-0 z-50 flex items-start justify-center bg-black/40 pt-[8vh]" onMouseDown={event => {
    if (event.target === event.currentTarget) { event.preventDefault(); onClose() }
  }}>
    <section ref={box} role="dialog" aria-modal="true" aria-labelledby={title}
      className="flex max-h-[84vh] w-[min(640px,92%)] min-w-0 flex-col rounded-lg border border-line bg-panel p-4 text-sm shadow-xl"
      onKeyDown={event => {
        event.stopPropagation()
        if (event.key === 'Escape') { event.preventDefault(); onClose() }
        if (event.key === 'Tab') {
          const controls = [...(box.current?.querySelectorAll<HTMLElement>('button:not(:disabled), a[href]') ?? [])]
          const index = controls.indexOf(document.activeElement as HTMLElement)
          event.preventDefault()
          controls[(index + (event.shiftKey ? controls.length - 1 : 1)) % controls.length]?.focus()
        }
      }}>
      <header className="mb-4 flex shrink-0 items-start gap-3">
        <img src="./icon.png" width="48" height="48" alt="" className="shrink-0" />
        <div className="min-w-0 flex-1"><h2 id={title} className="mb-1 text-lg font-semibold">{t('about.title')}</h2><p className="text-fg-muted">{t('about.description')}</p></div>
        <button ref={close} type="button" className="rounded border border-line px-2 py-1 hover:bg-hover" onClick={onClose}>{t('about.close')}</button>
      </header>
      <div className="min-h-0 overflow-y-auto">
        <dl className="mb-4 grid grid-cols-[max-content_minmax(0,1fr)] gap-x-4 gap-y-2">
          <dt className="text-fg-subtle">{t('about.version')}</dt><dd className="break-all font-mono">{info.version ?? '—'}</dd>
          <dt className="text-fg-subtle">{t('about.license')}</dt><dd><a className={link} href="https://github.com/Sipaha/spk-mm-client/blob/main/LICENSE" target="_blank" rel="noopener noreferrer" onClick={external}>Apache License 2.0</a></dd>
        </dl>
        <nav className="mb-5 flex flex-wrap gap-x-5 gap-y-2" aria-label="SPK MM Client">
          <a className={link} href={'https://sipaha.github.io/spk-mm-client/' + suffix} target="_blank" rel="noopener noreferrer" onClick={external}>{t('about.website')}</a>
          <a className={link} href="https://github.com/Sipaha/spk-mm-client" target="_blank" rel="noopener noreferrer" onClick={external}>{t('about.source')}</a>
        </nav>
        <section className="border-t border-line pt-4">
          <h3 className="mb-2 text-xs text-fg-subtle">{t('about.author')}</h3>
          <p className="mb-2 font-semibold">{locale === 'ru' ? 'Павел Симонов' : 'Pavel Simonov'} <span className="font-normal text-fg-subtle">· Sipaha</span></p>
          <a className={link} href={'https://sipaha.github.io/about/' + suffix} target="_blank" rel="noopener noreferrer" onClick={external}>{t('about.profile')}</a>
        </section>
        {error && <p role="alert" className="mt-3 text-danger">{error}</p>}
      </div>
    </section>
  </div>
}
