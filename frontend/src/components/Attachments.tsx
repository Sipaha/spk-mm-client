import type { FileView } from '../api/types'
import { t } from '../i18n'
import { mediaURL, useLoadFailure } from '../media'
import { FileCard, type FileHandlers } from './FileCard'
import { fileKind, fitBox, imageSrc } from './files'
import { MarkdownSnippet } from './MarkdownSnippet'
import { TextSnippet } from './TextSnippet'

const BIG = { w: 480, h: 360 }
const THUMB = { w: 120, h: 100 }

// ImageTile loads its picture eagerly (not loading="lazy" — see Avatar).
function ImageTile({ serverId, file, big, onView, onDownload, onOpen }: { serverId: number; file: FileView; big: boolean } & FileHandlers) {
  const src = imageSrc(file)
  const url = !src ? '' : big ? mediaURL(serverId, 'feed', file.id, { src }) : mediaURL(serverId, 'thumb', file.id)
  // A failed load is a card until the server goes live again.
  const [failed, fail] = useLoadFailure(serverId, url)
  if (failed || !src) return <FileCard file={file} onDownload={onDownload} onOpen={onOpen} />
  const box = big ? fitBox(file.width, file.height, BIG.w, BIG.h) : { width: THUMB.w, height: THUMB.h }
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
        onError={fail}
        className={`block h-full w-full ${big ? 'object-contain' : 'object-cover'}`}
      />
    </button>
  )
}

// Attachments: one image big, several as thumbnails, text files as
// snippets, markdown files rendered, everything else as cards. Every box
// has its size up front.
export function Attachments({
  serverId,
  files,
  me,
  onLink,
  ...h
}: { serverId: number; files: FileView[]; me: string; onLink(href: string): void } & FileHandlers) {
  const images = files.filter((f) => fileKind(f) === 'image')
  const texts = files.filter((f) => fileKind(f) === 'text')
  const markdowns = files.filter((f) => fileKind(f) === 'markdown')
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
      {markdowns.map((f) => (
        <MarkdownSnippet key={f.id} serverId={serverId} file={f} me={me} onLink={onLink} {...h} />
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
