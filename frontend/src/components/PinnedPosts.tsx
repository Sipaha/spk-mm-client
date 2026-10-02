import { useEffect, useRef, useState } from 'react'
import type { PostView, ServerDTO } from '../api/types'
import { client } from '../api/client'
import { jumpToPost, openLink } from '../chat'
import { formatDay, formatLocale, formatTime } from '../format'
import { t } from '../i18n'
import { IconClose } from './icons'
import { Markdown } from './Markdown'

export function PinnedPosts({ server, channelId, onClose }: { server: ServerDTO; channelId: string; onClose(): void }) {
  const [posts, setPosts] = useState<PostView[] | null>(null)
  const [failed, setFailed] = useState(false)
  const pane = useRef<HTMLDivElement>(null)
  useEffect(() => {
    let live = true
    client.pinnedPosts(server.id, channelId).then((x) => { if (live) setPosts(x) }).catch(() => { if (live) setFailed(true) })
    return () => { live = false }
  }, [server.id, channelId])
  useEffect(() => {
    pane.current?.focus()
    const key = (e: KeyboardEvent) => { if (e.key === 'Escape' && !e.defaultPrevented) onClose() }
    window.addEventListener('keydown', key)
    return () => window.removeEventListener('keydown', key)
  }, [onClose])
  return <div className="fixed inset-0 z-[90] flex justify-end bg-black/35" onMouseDown={onClose}>
    <div ref={pane} role="dialog" aria-modal="true" aria-label={t('pins.title')} tabIndex={-1} onMouseDown={(e) => e.stopPropagation()} className="flex h-full w-[min(440px,100vw)] flex-col border-l border-line bg-app shadow-2xl outline-none">
      <header className="flex h-10 items-center border-b border-line bg-panel px-4"><h2 className="flex-1 font-semibold">{t('pins.title')}</h2><button aria-label={t('pins.close')} onClick={onClose} className="rounded p-1 text-fg-muted hover:bg-hover"><IconClose /></button></header>
      <div className="min-h-0 flex-1 overflow-y-auto p-3">
        {!posts && !failed && <p role="status" className="py-8 text-center text-fg-muted">{t('pins.loading')}</p>}
        {failed && <p role="alert" className="py-8 text-center text-danger">{t('pins.error')}</p>}
        {posts?.length === 0 && <p role="status" className="py-8 text-center text-fg-muted">{t('pins.empty')}</p>}
        {posts?.map((p) => <button key={p.id} className="mb-2 block w-full rounded border border-line bg-panel p-3 text-left hover:bg-hover" onClick={() => { onClose(); void jumpToPost(server.id, channelId, p.id, p.root_id).catch(() => {}) }}>
          <div className="mb-1 flex items-baseline gap-2"><strong className="truncate text-fg">{p.author || t('post.system')}</strong><span className="text-xs text-fg-muted">{formatDay(p.create_at, formatLocale())}, {formatTime(p.create_at, formatLocale())}</span></div>
          <div className="line-clamp-4 text-sm text-fg"><Markdown text={p.message} me={server.username} onLink={openLink} serverId={server.id} /></div>
        </button>)}
      </div>
    </div>
  </div>
}
