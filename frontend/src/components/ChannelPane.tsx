import { useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import type { ChannelDTO, FileView, ServerDTO } from '../api/types'
import {
  clearDownloads, closeDownloadsPanel, copyLink, deletePost, discardPost, downloadFile, downloadPrimaryAction,
  editLastOwn, editPost, emojiInfo, loadOlder, markUnread, openDownload, openDownloadsPanel, openFile, openLink,
  react, removeDownload, retryPost, revealDownload, saveDraft, sendPost, setPostSaved, uploadAttachments,
} from '../chat'
import { errorMessage } from '../errors'
import { formatLocale } from '../format'
import { t } from '../i18n'
import { useStore } from '../store'
import { Composer } from './Composer'
import Downloads from './Downloads'
import { Feed } from './Feed'
import { fileKind } from './files'
import { ChannelTypeMarker, IconDownload } from './icons'
import type { PostActions } from './PostItem'
import { Viewer } from './Viewer'

export function ChannelPane({ server, channel, onReauth }: { server: ServerDTO; channel: ChannelDTO | null; onReauth(): void }) {
  const channelId = channel?.id ?? ''
  const teamName = channel?.team_name ?? ''
  const editingId = useStore((s) => s.editingId)
  const downloads = useStore((s) => s.downloads)
  const downloadsOpen = useStore((s) => s.downloadsOpen)
  const attachments = useStore((s) => s.attachments)
  const activeDownloads = downloads.filter((d) => d.state === 'downloading').length
  const downloadsLabel = activeDownloads > 0 ? t('downloads.buttonActive', { n: String(activeDownloads) }) : t('downloads.button')
  const downloadsBtnRef = useRef<HTMLButtonElement>(null)
  // The viewer belongs to the channel it was opened in.
  const [viewer, setViewer] = useState<{ channelId: string; files: FileView[]; index: number } | null>(null)

  // Drag-and-drop (browser mode; desktop drops never reach the page —
  // WebKitGTK takes them at the GTK level and Wails toggles
  // .file-drop-target-active on data-file-drop-target itself, styled in
  // index.css). dragDepth survives dragleave firing for every child
  // element the pointer crosses on its way around the drop zone — only 0
  // means "actually left it".
  const [dragActive, setDragActive] = useState(false)
  const dragDepth = useRef(0)
  const hasFiles = (e: React.DragEvent) => Array.from(e.dataTransfer.types).includes('Files')
  const onDragEnter = (e: React.DragEvent) => {
    if (!hasFiles(e)) return
    e.preventDefault()
    dragDepth.current++
    setDragActive(true)
  }
  const onDragOver = (e: React.DragEvent) => {
    if (hasFiles(e)) e.preventDefault() // required for the drop event to fire at all
  }
  const onDragLeave = () => {
    dragDepth.current = Math.max(0, dragDepth.current - 1)
    if (dragDepth.current === 0) setDragActive(false)
  }
  const onDrop = (e: React.DragEvent) => {
    dragDepth.current = 0
    setDragActive(false)
    const files = Array.from(e.dataTransfer.files)
    if (files.length === 0) return
    e.preventDefault()
    uploadAttachments(server.id, channelId, files).catch((err) => useStore.getState().setAttachError(errorMessage(err)))
  }
  // Stable per channel: PostItem is memoized on its props.
  const actions = useMemo<PostActions>(
    () => ({
      link: openLink,
      retry: (p) => retryPost(server.id, channelId, p.id),
      discard: (p) => discardPost(server.id, channelId, p.id),
      edit: (p) => useStore.getState().setEditing(p.id),
      saveEdit: (p, message) => editPost(server.id, p.id, message),
      cancelEdit: () => useStore.getState().setEditing(null),
      remove: (p) => {
        if (confirm(t('post.deleteConfirm'))) deletePost(server.id, p.id)
      },
      markUnread: (p) => markUnread(server.id, p.id),
      save: (p, saved) => setPostSaved(server.id, p, saved),
      copyLink: (p) => copyLink(server.url, teamName, p.id),
      view: (p, fileId) => {
        const files = (p.files ?? []).filter((f) => fileKind(f) !== 'other')
        const index = files.findIndex((f) => f.id === fileId)
        if (index >= 0) setViewer({ channelId, files, index })
      },
      download: (f) => void downloadFile(server.id, f),
      open: (f) => void openFile(server.id, f),
      react: (p, emoji, add) => react(server.id, p.id, emoji, add),
      emojiInfo: () => emojiInfo(server.id),
    }),
    [server.id, server.url, channelId, teamName],
  )
  const me = useMemo(() => ({ id: channel?.me_id ?? '', username: server.username }), [channel?.me_id, server.username])
  if (!channel) {
    return <div className="flex flex-1 items-center justify-center bg-app text-fg-muted">{t('channel.none')}</div>
  }
  return (
    <section
      aria-label={channel.name}
      data-file-drop-target
      data-srv={server.id}
      data-channel={channel.id}
      onDragEnter={onDragEnter}
      onDragOver={onDragOver}
      onDragLeave={onDragLeave}
      onDrop={onDrop}
      className={`relative flex min-h-0 flex-1 flex-col bg-app ${dragActive ? 'file-drop-target-active' : ''}`}
      data-drop-label={t('composer.dropHint')}
    >
      <header className="flex min-w-0 items-baseline gap-3 border-b border-line bg-panel px-4 py-2">
        <h1 className="shrink-0 font-semibold text-fg">
          <span className="mr-1 inline-flex items-center text-fg-subtle">
            <ChannelTypeMarker type={channel.type} />
          </span>
          {channel.name}
        </h1>
        {channel.header && (
          <p className="truncate text-xs text-fg-muted" title={channel.header}>
            {channel.header}
          </p>
        )}
        <button
          ref={downloadsBtnRef}
          type="button"
          title={downloadsLabel}
          aria-label={downloadsLabel}
          onClick={() => (downloadsOpen ? closeDownloadsPanel() : openDownloadsPanel())}
          className="relative ml-auto flex shrink-0 items-center justify-center rounded px-1.5 py-1 text-fg-muted hover:bg-hover hover:text-fg"
        >
          <IconDownload />
          {activeDownloads > 0 && (
            <span aria-hidden className="absolute -right-0.5 -top-0.5 flex h-3.5 min-w-3.5 items-center justify-center rounded-full bg-accent px-0.5 text-[9px] font-semibold leading-none text-white">
              {activeDownloads}
            </span>
          )}
        </button>
      </header>
      {server.state === 'needs_reauth' && (
        <div role="status" className="flex items-center gap-3 bg-mention-bg px-4 py-1.5 text-sm text-mention-fg">
          {t('channel.sessionExpired')}
          <button className="font-medium underline" onClick={onReauth}>
            {t('status.signInAgain')}
          </button>
        </div>
      )}
      {channel.loaded && channel.syncing && server.state !== 'needs_reauth' && (
        <div role="status" className="border-b border-line px-4 py-0.5 text-xs text-fg-muted">
          {t('channel.syncing')}
        </div>
      )}
      <Feed key={`feed-${channel.id}`} serverId={server.id} channel={channel} me={me} locale={formatLocale()} actions={actions} editingId={editingId} onLoadOlder={() => loadOlder(server.id, channel.id)} />
      <Composer
        key={`composer-${channel.id}`}
        channel={channel}
        serverId={server.id}
        attachments={attachments}
        onSend={(m, ids) => sendPost(server.id, channel.id, m, ids)}
        onDraft={(text) => saveDraft(server.id, channel.id, text)}
        onEditLast={() => editLastOwn(channel)}
      />
      {viewer && viewer.channelId === channel.id && (
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
      {downloadsOpen &&
        downloadsBtnRef.current &&
        createPortal(
          <Downloads
            anchorEl={downloadsBtnRef.current}
            downloads={downloads}
            locale={formatLocale()}
            primaryAction={downloadPrimaryAction}
            onOpen={openDownload}
            onReveal={revealDownload}
            onRemove={removeDownload}
            onClear={clearDownloads}
            onClose={closeDownloadsPanel}
          />,
          document.body,
        )}
    </section>
  )
}
