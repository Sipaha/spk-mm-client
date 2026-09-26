// mediaSession: only one <video>/<audio> plays at a time across the whole
// app (feed tiles and the viewer's big pane share this) — starting one
// pauses every other mounted player. releaseMediaElement is the Task 13b
// lesson applied to media: a player that leaves the DOM (feed
// virtualization, a channel switch, closing the viewer) must be paused and
// let go of its source, or WebKit keeps the decoder/buffers alive for a
// hidden/removed element.
const active = new Set<HTMLMediaElement>()

function pauseOthers(playing: HTMLMediaElement) {
  for (const el of active) {
    if (el !== playing) el.pause()
  }
}

// registerMediaElement: call on mount. Returns the unregister function —
// call it on unmount, before releaseMediaElement.
export function registerMediaElement(el: HTMLMediaElement): () => void {
  const onPlay = () => pauseOthers(el)
  el.addEventListener('play', onPlay)
  active.add(el)
  return () => {
    el.removeEventListener('play', onPlay)
    active.delete(el)
  }
}

// releaseMediaElement: stop playback and drop the source so the element
// holds nothing after it unmounts.
export function releaseMediaElement(el: HTMLMediaElement) {
  el.pause()
  el.removeAttribute('src')
  el.load()
}
