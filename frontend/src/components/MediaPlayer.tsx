import { useEffect, useRef, useState } from 'react'
import type { FileView } from '../api/types'
import { formatSize } from '../format'
import { t } from '../i18n'
import { streamURL, useLoadFailure } from '../media'
import { useLiveEpoch } from '../store'
import { FileCard, IconButton, type FileHandlers } from './FileCard'
import { boxStyle, videoBox } from './files'
import { IconAudio, IconExpand, IconPlay } from './icons'
import { registerMediaElement, releaseMediaElement } from './mediaSession'

interface Props {
  serverId: number
  file: FileView
  kind: 'video' | 'audio'
  // The viewer's large pane: a bare element with native controls only (the
  // user already gestured to open the viewer). Omitted: the feed's fixed
  // box with a poster (video) or compact row (audio).
  big?: boolean
  onDownload: FileHandlers['onDownload']
  onOpen: FileHandlers['onOpen']
  // Not used when big — the viewer is already the "view" destination.
  onView?: FileHandlers['onView']
}

// Arrow keys inside the player seek (the <video>/<audio controls> native
// behaviour); stopping propagation here keeps them from also reaching the
// viewer's own window-level ←/→ listener, which would otherwise page to
// the post's next/previous file instead.
function stopArrowKeys(e: React.KeyboardEvent) {
  if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') e.stopPropagation()
}

// MediaPlayer is the <video>/<audio> element for one streamed file (Task
// 7's loopback/`/media` stream, `serverId/stream/fileId`). It is
// preload="none" everywhere: the feed's video starts as a fixed-size poster
// (icon, name, size) and only fetches/plays on the user's click (the
// desktop webview never autoplays without a gesture — spike S6); the
// viewer's big pane and the feed's compact audio row use native controls
// directly, so the user's own click on them is the gesture. Only one
// player plays at a time across the app (mediaSession), and unmounting
// pauses and releases the element (`removeAttribute('src'); load()`) so
// WebKit doesn't keep a decoder/buffers alive for a tile that scrolled out
// of the feed or a channel that was switched away from (Task 13b).
export function MediaPlayer({ serverId, file, kind, big = false, onDownload, onOpen, onView }: Props) {
  const epoch = useLiveEpoch(serverId)
  const [failed, fail] = useLoadFailure(serverId, `stream/${file.id}`)
  const [url, setUrl] = useState<string | undefined>(undefined)
  const [wantsPlay, setWantsPlay] = useState(false)
  const ref = useRef<HTMLVideoElement & HTMLAudioElement>(null)

  // Resolve the stream URL as soon as the player mounts (not on click): the
  // base is a module-level cache (Task 7), so this costs one real call per
  // app run and every later player/file gets it near-instantly. Depending
  // on `epoch` retries after a rejection (MediaStreamBase failing, e.g. the
  // worker wasn't live yet) once the server actually goes live — the same
  // "remembered only until the next live epoch" story as a media/image
  // load failure. A rejection falls back to a card exactly like the
  // element's own `error` event does (fail()).
  useEffect(() => {
    let live = true
    streamURL(serverId, file.id).then(
      (u) => {
        if (live) setUrl(u)
      },
      () => {
        if (live) fail()
      },
    )
    return () => {
      live = false
    }
    // `fail`'s identity changes every render (it closes over the current
    // epoch/tag); `epoch` itself is the right thing to retrigger this on.
  }, [serverId, file.id, epoch])

  // Depends on `failed`, not `[]`: a stream failure swaps this component's
  // own JSX for a FileCard (below) without unmounting MediaPlayer itself,
  // so `ref.current` going away on that flip would otherwise never run
  // this cleanup — leaving the detached element registered forever.
  useEffect(() => {
    const el = ref.current
    if (!el) return
    const off = registerMediaElement(el)
    return () => {
      off()
      releaseMediaElement(el)
    }
  }, [failed])

  if (failed) return <FileCard serverId={serverId} file={file} onDownload={onDownload} onOpen={onOpen} />

  if (kind === 'audio') {
    const el = (
      <audio ref={ref} controls preload="none" src={url} onError={fail} onKeyDown={stopArrowKeys} className={big ? 'w-full max-w-xl' : 'h-8 min-w-0 flex-1'} />
    )
    if (big) return el
    return (
      <div className="flex w-full max-w-xl items-center gap-2 rounded border border-line bg-panel px-2 py-1 text-xs">
        <IconAudio className="shrink-0 text-fg-muted" />
        <span className="min-w-0 shrink-0 truncate font-medium" title={file.name} style={{ maxWidth: '40%' }}>
          {file.name}
        </span>
        {el}
        <IconButton label={t('file.view', { name: file.name })} onClick={() => onView?.(file)}>
          <IconExpand />
        </IconButton>
      </div>
    )
  }

  // kind === 'video'
  if (big) {
    // h-full w-full (not just max-h/max-w): before the user presses play,
    // preload="none" means no metadata has loaded, so the element has no
    // intrinsic size and would otherwise fall back to the browser's tiny
    // 300×150 default. Filling the pane keeps "large" true before and
    // after metadata arrives; object-contain letterboxes once it does.
    return (
      <video ref={ref} controls preload="none" src={url} onError={fail} onKeyDown={stopArrowKeys} className="h-full w-full max-h-full max-w-full bg-black object-contain" />
    )
  }

  const box = videoBox(file)
  // Call play() synchronously, inside this same click — WebKitGTK (spike
  // S6) silently refuses playback that isn't started within the user
  // gesture's own call stack, so deferring it to an effect (even one that
  // fires on the very next tick) can lose the gesture. The URL is almost
  // always already resolved by click time (fetched on mount, from a
  // module-level cache — see the effect above), so this is the common
  // path. If it genuinely isn't in yet, there is nothing to play: reveal
  // the native controls instead (`wantsPlay`), so the user's *next* click
  // — on the control itself — is its own fresh gesture; nothing here plays
  // it automatically once the URL arrives.
  const play = () => {
    setWantsPlay(true)
    if (url) ref.current?.play().catch(() => {})
  }
  return (
    <div className="relative shrink-0 overflow-hidden rounded border border-line bg-black" style={boxStyle(box)}>
      <video
        ref={ref}
        controls={wantsPlay}
        preload="none"
        src={url}
        onError={fail}
        onKeyDown={stopArrowKeys}
        className="block h-full w-full object-contain"
      />
      {!wantsPlay && (
        <button
          type="button"
          aria-label={t('file.play', { name: file.name })}
          onClick={play}
          className="absolute inset-0 flex flex-col items-center justify-center gap-1 bg-panel text-fg hover:bg-hover"
        >
          <IconPlay size={32} />
          <span className="max-w-full truncate px-2 text-xs" title={file.name}>
            {file.name}
          </span>
          <span className="text-xs text-fg-muted">{formatSize(file.size)}</span>
        </button>
      )}
      <IconButton
        label={t('file.view', { name: file.name })}
        onClick={() => onView?.(file)}
        className="absolute right-1 top-1 bg-black/50 text-white hover:bg-black/70"
      >
        <IconExpand />
      </IconButton>
    </div>
  )
}
