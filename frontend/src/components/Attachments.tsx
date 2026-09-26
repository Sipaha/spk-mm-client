import { useState } from 'react'
import type { FileView } from '../api/types'
import { t } from '../i18n'
import { mediaURL } from '../media'
import { FileCard, type FileHandlers } from './FileCard'
import { fileKind, fitBox, imageSrc } from './files'
import { TextSnippet } from './TextSnippet'

const BIG = { w: 480, h: 360 }
const THUMB = { w: 120, h: 100 }

// ImageTile loads its picture eagerly (not loading="lazy" — see Avatar).
function ImageTile({ serverId, file, big, onView, onDownload, onOpen }: { serverId: number; file: FileView; big: boolean } & FileHandlers) {
  const [failed, setFailed] = useState(false)
  const src = imageSrc(file)
  if (failed || !src) return <FileCard file={file} onDownload={onDownload} onOpen={onOpen} />
  const box = big ? fitBox(file.width, file.height, BIG.w, BIG.h) : { width: THUMB.w, height: THUMB.h }
  const url = big ? mediaURL(serverId, 'feed', file.id, { src }) : mediaURL(serverId, 'thumb', file.id)
  return (
    <button
      type="button"
      aria-label={t('file.view', { name: file.name })}
      title={file.name}
      onClick={() => onView(file)}
      className="block shrink-0 overflow-hidden rounded border border-line bg-panel focus:outline-none focus-visible:ring-2 focus-visible:ring-accent"
      style={{ width: box.width, height: box.height }}
    >
      <img
        src={url}
        alt={file.name}
        width={box.width}
        height={box.height}
        decoding="async"
        draggable={false}
        onError={() => setFailed(true)}
        className={`block h-full w-full ${big ? 'object-contain' : 'object-cover'}`}
      />
    </button>
  )
}

// Attachments: one image big, several as thumbnails, text files as
// snippets, everything else as cards. Every box has its size up front.
export function Attachments({ serverId, files, ...h }: { serverId: number; files: FileView[] } & FileHandlers) {
  const images = files.filter((f) => fileKind(f) === 'image')
  const texts = files.filter((f) => fileKind(f) === 'text')
  const others = files.filter((f) => fileKind(f) === 'other')
  return (
    <div className="mt-1 flex flex-col items-start gap-2">
      {images.length > 0 && (
        <div className="flex flex-wrap gap-2">
          {images.map((f) => (
            <ImageTile key={f.id} serverId={serverId} file={f} big={images.length === 1} {...h} />
          ))}
        </div>
      )}
      {texts.map((f) => (
        <TextSnippet key={f.id} serverId={serverId} file={f} {...h} />
      ))}
      {others.length > 0 && (
        <div className="flex flex-wrap gap-2">
          {others.map((f) => (
            <FileCard key={f.id} file={f} onDownload={h.onDownload} onOpen={h.onOpen} />
          ))}
        </div>
      )}
    </div>
  )
}
