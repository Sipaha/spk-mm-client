import { useEffect, useMemo, useRef, useState } from 'react'
import type { FileView, ServerDTO, ThreadDTO } from '../api/types'
import { errorMessage } from '../errors'
import {
  copyLink, deletePost, discardPost, downloadFile, editLastOwn, editPost, emojiInfo, loadOlderReplies, markUnread,
  openFile, openLink, openThread, react, reactionUsers, retryPost, saveThreadDraft, sendReply, setPostSaved, uploadAttachments,
} from '../chat'
import { formatLocale } from '../format'
import { t } from '../i18n'
import { useStore } from '../store'
import { Composer } from './Composer'
import { Feed, type FeedData } from './Feed'
import { fileKind } from './files'
import { IconChevronLeft, IconClose } from './icons'
import type { PostActions } from './PostItem'
import { Viewer } from './Viewer'

const NARROW_QUERY = '(max-width: 1023px)'

// useNarrow: the panel overlays the feed instead of sitting beside it below
// 1024px window width (Task 6 brief, spike's "variant C"). Guarded for
// environments without matchMedia (defensive only — not expected in a real
// browser or WebKitGTK).
function useNarrow(): boolean {
  const supported = typeof window !== 'undefined' && typeof window.matchMedia === 'function'
  const [narrow, setNarrow] = useState(() => (supported ? window.matchMedia(NARROW_QUERY).matches : false))
  useEffect(() => {
    if (!supported) return
    const mql = window.matchMedia(NARROW_QUERY)
    const onChange = () => setNarrow(mql.matches)
    onChange()
    mql.addEventListener('change', onChange)
    return () => mql.removeEventListener('change', onChange)
  }, [supported])
  return narrow
}

interface Props {
  server: ServerDTO
  thread: ThreadDTO
  onClose(): void
}

export function ThreadPane({ server, thread, onClose }: Props) {
  const editingId = useStore((s) => s.editingId)
  const threadAttachments = useStore((s) => s.threadAttachments)
  const narrow = useNarrow()
  const paneRef = useRef<HTMLElement>(null)
  const [viewer, setViewer] = useState<{ rootId: string; files: FileView[]; index: number } | null>(null)

  // close: shared by "×", "← back to channel" and Esc — returns focus to
  // the channel's own feed (Feed.tsx tags it data-feed="channel"), since
  // this panel is about to unmount entirely.
  const close = () => {
    onClose()
    document.querySelector<HTMLElement>('[data-feed="channel"]')?.focus()
  }

  // Esc closes the panel — but only while focus is actually inside it and
  // no popover/picker (portalled to document.body, so DOM-outside the
  // panel) has taken it; e.defaultPrevented also skips a post's own
  // Escape handler (e.g. cancelling an inline edit) so the two don't fire
  // together.
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key !== 'Escape' || e.defaultPrevented) return
      if (!paneRef.current?.contains(document.activeElement)) return
      close()
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [onClose])

  const actions = useMemo<PostActions>(
    () => ({
      link: openLink,
      retry: (p) => retryPost(server.id, thread.channel_id, p.id),
      discard: (p) => discardPost(server.id, thread.channel_id, p.id),
      edit: (p) => useStore.getState().setEditing(p.id),
      saveEdit: (p, message) => editPost(server.id, p.id, message),
      cancelEdit: () => useStore.getState().setEditing(null),
      remove: (p) => {
        if (confirm(t('post.deleteConfirm'))) deletePost(server.id, p.id)
      },
      markUnread: (p) => markUnread(server.id, p.id),
      save: (p, saved) => setPostSaved(server.id, p, saved),
      copyLink: (p) => copyLink(server.url, thread.team_name, p.id),
      view: (p, fileId) => {
        const files = (p.files ?? []).filter((f) => fileKind(f) !== 'other')
        const index = files.findIndex((f) => f.id === fileId)
        if (index >= 0) setViewer({ rootId: thread.root_id, files, index })
      },
      download: (f) => void downloadFile(server.id, f),
      open: (f) => void openFile(server.id, f),
      react: (p, emoji, add) => react(server.id, p.id, emoji, add),
      emojiInfo: () => emojiInfo(server.id),
      reactionUsers: (p, emoji) => reactionUsers(server.id, p.id, emoji),
      openThread: (p) => {
        const rootId = p.root_id || p.id
        if (rootId !== thread.root_id) void openThread(server.id, thread.channel_id, rootId)
      },
    }),
    [server.id, server.url, thread.channel_id, thread.team_name, thread.root_id],
  )
  const me = useMemo(() => ({ id: thread.me_id, username: server.username }), [thread.me_id, server.username])
  const data: FeedData = useMemo(
    () => ({
      id: thread.root_id, posts: thread.posts, new_since: thread.new_since, me_id: thread.me_id,
      gap_after: thread.gap_after, has_more: thread.has_more, loaded: thread.loaded, crt: thread.crt,
    }),
    [thread],
  )

  const permalink = `${server.url}/${thread.team_name}/pl/${thread.root_id}`
  const retryLoad = () => void openThread(server.id, thread.channel_id, thread.root_id)

  return (
    <aside
      ref={paneRef}
      role="complementary"
      aria-label={t('thread.title')}
      className={narrow ? 'absolute inset-0 z-20 flex min-h-0 flex-col bg-app' : 'flex min-h-0 w-[420px] shrink-0 flex-col border-l border-line bg-app'}
    >
      <header className="flex items-center gap-2 border-b border-line bg-panel px-3 py-2">
        {narrow && (
          <button
            type="button"
            aria-label={t('thread.backToChannel')}
            title={t('thread.backToChannel')}
            onClick={close}
            className="flex shrink-0 items-center gap-1 rounded px-1.5 py-1 text-sm text-fg-muted hover:bg-hover hover:text-fg"
          >
            <IconChevronLeft size={18} />
            {t('thread.backToChannel')}
          </button>
        )}
        <h2 className="min-w-0 flex-1 truncate font-semibold text-fg">
          {t('thread.title')} · {thread.channel_name}
        </h2>
        <button
          type="button"
          aria-label={t('thread.close')}
          title={t('thread.close')}
          onClick={close}
          className="flex shrink-0 items-center justify-center rounded px-1.5 py-1 text-fg-muted hover:bg-hover hover:text-fg"
        >
          <IconClose />
        </button>
      </header>
      {thread.error !== '' && (
        <div role="alert" className="flex items-center justify-between gap-2 border-b border-line bg-panel px-3 py-1.5 text-xs text-danger">
          <span>{t('thread.loadFailed')}</span>
          <button type="button" className="shrink-0 underline" onClick={retryLoad}>
            {t('post.retry')}
          </button>
        </div>
      )}
      {thread.capped && (
        <div role="status" className="flex items-center justify-between gap-2 border-b border-line px-3 py-1.5 text-xs text-fg-muted">
          <span>{t('thread.capped')}</span>
          <button type="button" className="shrink-0 underline" onClick={() => openLink(permalink)}>
            {t('thread.openInBrowser')}
          </button>
        </div>
      )}
      <Feed
        data={data}
        variant="thread"
        serverId={server.id}
        me={me}
        locale={formatLocale()}
        actions={actions}
        editingId={editingId}
        onLoadOlder={() => loadOlderReplies(server.id, thread.root_id)}
      />
      {thread.root_deleted && (
        <div role="status" className="border-t border-line bg-panel px-3 py-1.5 text-xs text-fg-muted">
          {t('thread.rootDeleted')}
        </div>
      )}
      <div
        data-file-drop-target
        data-srv={server.id}
        data-channel={thread.channel_id}
        data-root={thread.root_id}
        data-drop-label={t('composer.dropHint')}
        onDragOver={(e) => {
          if (Array.from(e.dataTransfer.types).includes('Files')) e.preventDefault()
        }}
        onDrop={(e) => {
          const files = Array.from(e.dataTransfer.files)
          if (files.length === 0) return
          e.preventDefault()
          uploadAttachments(server.id, thread.channel_id, files, thread.root_id).catch((err) =>
            useStore.getState().setThreadAttachError(errorMessage(err)),
          )
        }}
      >
        <Composer
          key={`composer-${thread.channel_id}-${thread.root_id}`}
          channelId={thread.channel_id}
          channelName={thread.channel_name}
          draft={thread.draft}
          rootId={thread.root_id}
          disabled={thread.root_deleted}
          serverId={server.id}
          attachments={threadAttachments}
          onSend={(m, ids) => sendReply(server.id, thread.channel_id, thread.root_id, m, ids)}
          onDraft={(text) => saveThreadDraft(server.id, thread.root_id, text)}
          onEditLast={() => editLastOwn(thread)}
        />
      </div>
      {viewer && viewer.rootId === thread.root_id && (
        <Viewer
          serverId={server.id}
          files={viewer.files}
          index={viewer.index}
          me={server.username}
          onLink={openLink}
          onIndex={(index) => setViewer({ ...viewer, index })}
          onClose={() => setViewer(null)}
          onDownload={(f) => void downloadFile(server.id, f)}
          onOpen={(f) => void openFile(server.id, f)}
        />
      )}
    </aside>
  )
}
