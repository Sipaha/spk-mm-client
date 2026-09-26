import type { FileView } from '../api/types'
import { t } from '../i18n'
import { mediaURL } from '../media'
import { useLiveEpoch } from '../store'
import { Markdown } from './Markdown'
import { useTextFile } from './textFile'

// MarkdownView is the viewer's rendered pane for a markdown file: full
// width, scrolling, up to 1 MiB via ?full=1 (same source TextView's source
// pane reads). Remote images become links and links go to the system
// browser, same as the post's Markdown component.
export function MarkdownView({ serverId, file, me, onLink }: { serverId: number; file: FileView; me: string; onLink(href: string): void }) {
  const res = useTextFile(mediaURL(serverId, 'text', file.id, { full: '1' }), useLiveEpoch(serverId))
  return (
    // bg-panel, not bg-code-bg: .md pre/.md code (index.css) paint fenced
    // and inline code with --color-code-bg, the same token — a panel in
    // that shade would make code blocks invisible against it. bg-panel
    // keeps the panel opaque over the backdrop while letting code stand
    // out, same as it does over the feed's bg-app.
    <div className="h-full w-full overflow-auto rounded bg-panel px-6 py-4">
      {res.status === 'ok' ? (
        <>
          {res.truncated && <p className="mb-2 text-xs text-fg-muted">{t('file.truncated')}</p>}
          <Markdown text={res.text} me={me} onLink={onLink} />
        </>
      ) : (
        <p className="text-fg-muted">{res.status === 'loading' ? t('file.loading') : t('err.no_file')}</p>
      )}
    </div>
  )
}
