import { useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import type { ChannelDTO, FileView, ServerDTO } from '../api/types'
import {
  clearDownloads, closeDownloadsPanel, copyLink, deletePost, discardPost, downloadFile, downloadPrimaryAction,
  editLastOwn, editPost, emojiInfo, executeCommand, loadOlder, markUnread, openDownload, openDownloadsPanel, openFile, openLink,
  openThread, react, reactionUsers, removeDownload, retryPost, revealDownload, saveDraft, sendPost, setPostSaved, uploadAttachments,
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
import { TOAST_HOST } from './Toast'
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
    uploadAttachments(server.id, channelId, files, '').catch((err) => useStore.getState().setAttachError(errorMessage(err)))
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
      reactionUsers: (p, emoji) => reactionUsers(server.id, p.id, emoji),
      openThread: (p) => void openThread(server.id, channelId, p.root_id || p.id),
    }),
    [server.id, server.url, channelId, teamName],
  )
  const me = useMemo(
    () => ({ id: channel?.me_id ?? '', username: server.username, avatar: channel?.me_avatar ?? '' }),
    [channel?.me_id, channel?.me_avatar, server.username],
  )
  if (!channel) {
    return <div className="flex flex-1 items-center justify-center bg-app text-fg-muted">{t('channel.none')}</div>
  }
  return (
    <section
      aria-label={channel.name}
      data-file-drop-target
      data-srv={server.id}
      data-channel={channel.id}
      data-root=""
      onDragEnter={onDragEnter}
      onDragOver={onDragOver}
      onDragLeave={onDragLeave}
      onDrop={onDrop}
      // min-w-0: without it, this flex item's automatic min-width defaults
      // to its content's intrinsic width (the classic flexbox min-width:
      // auto trap), so it refuses to shrink below that once a sibling
      // splitter (sidebar or thread panel) grows past the point where
      // there's still "naturally" enough room — the row then overflows
      // the actual window instead of the feed shrinking, which reads as
      // "the splitter freezes while the panel's content and right edge
      // run off the screen" (splitter-drag bug, 2026-09-29; found via the
      // WebKit inspector + a real XTest drag: ChannelPane plateaued at a
      // fixed intrinsic width — e.g. 448px on a 1200px window — no matter
      // how wide the thread panel grew, so document.scrollingElement.
      // scrollWidth outgrew clientWidth by exactly the panel's excess).
      className={`relative flex min-h-0 min-w-0 flex-1 flex-col bg-app ${dragActive ? 'file-drop-target-active' : ''}`}
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
            <span aria-hidden className="absolute -right-0.5 -top-0.5 flex h-3.5 min-w-3.5 items-center justify-center rounded-full bg-accent px-0.5 text-[9px] font-semibold leading-none text-accent-fg">
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
      <Feed key={`feed-${channel.id}`} data={channel} variant="channel" serverId={server.id} me={me} locale={formatLocale()} actions={actions} editingId={editingId} onLoadOlder={() => loadOlder(server.id, channel.id)} toastHost={TOAST_HOST.channel} />
      {/* pt-2 (8px): feed↔composer gap (density-brief 2026-09-29) — the
          official client leaves visible air above the input box. The
          composer itself isn't the source: advanced_text_editor.scss's
          .AdvancedTextEditor rule sets `padding: 0 24px` — padding-top *is*
          0, confirmed. Corrected understanding (density-review 2026-09-29,
          re-review of fix round 1 — the same pattern the reviewer found for
          ThreadPane's composer): the real official gap is the *feed's own*
          bottom padding, `.post-list__content { padding: 14px 0 7px }`
          (_post.scss) — 7px, which our Feed scroller's `pb-2` (8px) already
          approximates almost exactly. This pt-2 wrapper is therefore extra
          air beyond the literal official value, kept because it read right
          against the brief's own screenshot comparison (the user's original
          complaint was "too close", not "too far") — a deliberate, admitted
          approximation, not a ported metric. Same value as ThreadPane's, for
          consistency. Kept here in the pane wrapper rather than in
          Feed.tsx/Composer.tsx. */}
      <div className="pt-2">
        <Composer
          key={`composer-${channel.id}`}
          channelId={channel.id}
          channelName={channel.name}
          draft={channel.draft}
          serverId={server.id}
          attachments={attachments}
          emojiInfo={() => emojiInfo(server.id)}
          onSend={(m, ids) => sendPost(server.id, channel.id, m, ids)}
          onCommand={(cmd) => executeCommand(server.id, channel.id, '', cmd)}
          onDraft={(text) => saveDraft(server.id, channel.id, text)}
          onEditLast={() => editLastOwn(channel)}
        />
      </div>
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
