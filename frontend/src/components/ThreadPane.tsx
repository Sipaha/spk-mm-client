import { useEffect, useMemo, useRef, useState } from 'react'
import type { FileView, ServerDTO, ThreadDTO } from '../api/types'
import { errorMessage } from '../errors'
import {
  backToResults, copyLink, deletePost, discardPost, downloadFile, editLastOwn, editPost, emojiInfo, executeCommand, loadOlderReplies, loadThreadFocus,
  markUnread, openFile, openLink, openThread, react, reactionUsers, retryPost, retryThreadRevalidation, saveThreadDraft, sendReply,
  setPostSaved, uploadAttachments,
} from '../chat'
import { formatLocale } from '../format'
import { t } from '../i18n'
import { useStore } from '../store'
import { useNarrow } from '../useNarrow'
import { Composer } from './Composer'
import { Feed, type FeedData } from './Feed'
import { fileKind } from './files'
import { IconChevronLeft, IconClose } from './icons'
import type { PostActions } from './PostItem'
import { TOAST_HOST } from './Toast'
import { Viewer } from './Viewer'

interface Props {
  server: ServerDTO
  thread: ThreadDTO
  onClose(): void
}

export function ThreadPane({ server, thread, onClose }: Props) {
  const editingId = useStore((s) => s.editingId)
  const threadAttachments = useStore((s) => s.threadAttachments)
  const threadFocus = useStore((s) => s.threadFocus)
  const focusShown = useStore((s) => s.focusShown)
  // Opened from the kept search results: the way back to them (spec
  // «Поиск», Секция 2 — one right panel at a time); a narrow window keeps
  // "← Back to channel" too (it dismisses the results as well).
  const hasResults = useStore((s) => s.search?.serverId === server.id && s.threadFrom === 'search')
  const narrow = useNarrow()
  const paneRef = useRef<HTMLElement>(null)
  const [viewer, setViewer] = useState<{ rootId: string; files: FileView[]; index: number } | null>(null)
  const [dragActive, setDragActive] = useState(false)

  // onClose churns every render (App.tsx has no selector, so any store
  // change re-renders it with a fresh closure) — keep the latest one in a
  // ref so the Esc effect below can bind its window listener exactly once
  // (fix round 1, Minor 2) instead of tearing it down and re-adding it on
  // every render.
  const onCloseRef = useRef(onClose)
  onCloseRef.current = onClose

  // close: shared by "×", "← back to channel" and Esc — returns focus to
  // the channel's own feed (Feed.tsx tags it data-feed="channel"), since
  // this panel is about to unmount entirely.
  const close = () => {
    onCloseRef.current()
    document.querySelector<HTMLElement>('[data-feed="channel"]')?.focus()
  }
  // toChannel: "← Back to channel" (narrow) — the channel's feed, not the
  // results the thread may have been opened from.
  const toChannel = () => {
    close()
    useStore.getState().setRhs(null)
  }

  // Esc closes the panel — but only while focus is actually inside it and
  // no popover/picker (portalled to document.body, so DOM-outside the
  // panel) has taken it; e.defaultPrevented also skips a post's own
  // Escape handler (e.g. cancelling an inline edit) so the two don't fire
  // together. Bound once (mount/unmount only, via onCloseRef above).
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key !== 'Escape' || e.defaultPrevented) return
      if (!paneRef.current?.contains(document.activeElement)) return
      close()
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [])

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
  // A thread focus (a reply jumped to beyond the replies held — spec
  // «Поиск», Секция 1б): older replies page through the focus (has_older),
  // its gap to the latest replies is the feed's history gap, and its rev
  // the revision the feed's anchors wait for. Centering is threadFocus's
  // nonce (store), never rev.
  const focus = thread.focus
  const data: FeedData = useMemo(
    () => ({
      id: thread.root_id, posts: thread.posts, new_since: thread.new_since, me_id: thread.me_id,
      gap_after: thread.gap_after, has_more: thread.focus ? thread.focus.has_older : thread.has_more, loaded: thread.loaded, crt: thread.crt,
      ...(thread.focus ? { gap: thread.focus.gap, hist_rev: thread.focus.rev } : {}),
    }),
    [thread],
  )

  // "Open in browser": the reply focused, else the thread.
  const permalink = `${server.url}/${thread.team_name}/pl/${focus?.target_id || thread.root_id}`
  // The focus shows a part of the thread (older replies or ones behind
  // the gap not loaded): say so, with the way out to the full thread.
  const partial = !!focus && (focus.has_older || focus.gap.open)
  const retryLoad = () => void openThread(server.id, thread.channel_id, thread.root_id)

  // Drag-and-drop (browser mode; desktop drops never reach the page — see
  // ChannelPane.tsx, whose pattern this mirrors): the whole panel is the
  // drop target (fix round 1, Minor 1 — was just the composer strip).
  // dragDepth survives dragleave firing for every child element the
  // pointer crosses on its way around the drop zone — only 0 means
  // "actually left it".
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
    uploadAttachments(server.id, thread.channel_id, files, thread.root_id).catch((err) =>
      useStore.getState().setThreadAttachError(errorMessage(err)),
    )
  }

  return (
    <aside
      ref={paneRef}
      role="complementary"
      aria-label={t('thread.title')}
      data-file-drop-target
      data-srv={server.id}
      data-channel={thread.channel_id}
      data-root={thread.root_id}
      data-drop-label={t('composer.dropHint')}
      onDragEnter={onDragEnter}
      onDragOver={onDragOver}
      onDragLeave={onDragLeave}
      onDrop={onDrop}
      // Width comes from the --spk-thread-width CSS var the splitter drives
      // (App.tsx; theme brief scope 3a) — narrow (overlay) mode ignores it
      // entirely and is never resizable.
      style={narrow ? undefined : { width: 'var(--spk-thread-width, 420px)' }}
      className={`${narrow ? 'absolute inset-0 z-20 flex min-h-0 flex-col bg-app' : 'relative flex min-h-0 shrink-0 flex-col border-l border-line bg-app'} ${dragActive ? 'file-drop-target-active' : ''}`}
    >
      {/* h-8, no vertical padding (fix round 1, header-markdown-brief
          2026-09-30): matches ChannelPane.tsx's header exactly (32px, border
          included via box-sizing border-box) so the two bars line up side
          by side when this panel is open next to the channel — see that
          file's header comment for the full before/after measurement. */}
      <header className="flex h-8 items-center gap-2 border-b border-line bg-panel px-3">
        {hasResults && (
          <button
            type="button"
            aria-label={t('search.backToResults')}
            title={t('search.backToResults')}
            onClick={backToResults}
            className="flex shrink-0 items-center gap-1 rounded px-1.5 py-1 text-sm text-fg-muted hover:bg-hover hover:text-fg"
          >
            <IconChevronLeft size={18} />
            {t('search.backToResults')}
          </button>
        )}
        {narrow && (
          <button
            type="button"
            aria-label={t('thread.backToChannel')}
            title={t('thread.backToChannel')}
            onClick={toChannel}
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
      {(thread.capped || partial) && (
        <div role="status" className="flex items-center justify-between gap-2 border-b border-line px-3 py-1.5 text-xs text-fg-muted">
          <span>{t(partial ? 'thread.focused' : 'thread.capped')}</span>
          <button type="button" className="shrink-0 underline" onClick={() => openLink(permalink)}>
            {t('thread.openInBrowser')}
          </button>
        </div>
      )}
      <Feed
        key={`feed-${thread.channel_id}-${thread.root_id}`}
        data={data}
        variant="thread"
        serverId={server.id}
        me={me}
        locale={formatLocale()}
        actions={actions}
        editingId={editingId}
        onLoadOlder={() => (focus ? loadThreadFocus(server.id, thread.root_id, false) : loadOlderReplies(server.id, thread.root_id))}
        onLoadNewer={() => loadThreadFocus(server.id, thread.root_id, true)}
        onRetryStale={() => void retryThreadRevalidation(server.id, thread.root_id)}
        focus={threadFocus}
        onFocusShown={focusShown}
        toastHost={TOAST_HOST.thread}
      />
      {thread.root_deleted && (
        <div role="status" className="border-t border-line bg-panel px-3 py-1.5 text-xs text-fg-muted">
          {t('thread.rootDeleted')}
        </div>
      )}
      {/* pt-2 (8px): feed↔composer gap, same value as ChannelPane's — see
          its comment for the full reasoning. Correction (density-review
          2026-09-29, re-review of fix round 1): a prior version of this
          comment claimed `.sidebar--right & { padding-top: 12px }`
          (advanced_text_editor.scss) was the real official number for this
          panel. Wrong — in mm-10.11 the docked RHS thread *is*
          `ThreadViewer` (rhs_thread.tsx renders <ThreadViewer>, which sets
          className="ThreadViewer" on its own root), so `.ThreadViewer
          .AdvancedTextEditor { padding-top: 0 }` also matches there and,
          same specificity, wins by source order — the official RHS
          composer's padding-top is 0, same as the main channel. Its visible
          gap instead comes from `.ThreadViewer .post-list__dynamic--RHS {
          padding-bottom: 8px }`, i.e. the *feed's own* bottom padding — our
          Feed scroller's `pb-2` already provides exactly that. This pt-2 is
          therefore the same approximation as ChannelPane's, not a distinct
          "real" number — kept equal to it for consistency. Kept in the pane
          wrapper rather than Composer.tsx, per ChannelPane.tsx's note. */}
      <div className="pt-2">
        <Composer
          key={`composer-${thread.channel_id}-${thread.root_id}`}
          channelId={thread.channel_id}
          channelName={thread.channel_name}
          draft={thread.draft}
          rootId={thread.root_id}
          disabled={thread.root_deleted}
          serverId={server.id}
          attachments={threadAttachments}
          emojiInfo={() => emojiInfo(server.id)}
          onSend={(m, ids) => sendReply(server.id, thread.channel_id, thread.root_id, m, ids)}
          onCommand={(cmd) => executeCommand(server.id, thread.channel_id, thread.root_id, cmd)}
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
