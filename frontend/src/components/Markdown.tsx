import { memo } from 'react'
import ReactMarkdown, { type Components } from 'react-markdown'
import remarkBreaks from 'remark-breaks'
import remarkGfm from 'remark-gfm'
import { remarkMentions } from './remarkMentions'

const plugins = [remarkGfm, remarkBreaks, remarkMentions]
const SPECIAL = new Set(['channel', 'here', 'all'])

function linkTo(href: string | undefined, onLink: (href: string) => void) {
  return (e: React.MouseEvent) => {
    e.preventDefault()
    if (href) onLink(href)
  }
}

// Markdown renders a message the way Mattermost does, without ever loading
// remote content: images become links, every link opens in the system browser.
export const Markdown = memo(function Markdown({ text, me, onLink }: { text: string; me: string; onLink(href: string): void }) {
  const components: Components = {
    a: ({ href, children }) => (
      <a href={href} title={href} className="text-accent hover:underline" onClick={linkTo(href, onLink)}>
        {children}
      </a>
    ),
    img: ({ src, alt }) => {
      const href = typeof src === 'string' ? src : ''
      return (
        <a href={href} title={href} className="text-accent hover:underline" onClick={linkTo(href, onLink)}>
          🖼 {alt || href}
        </a>
      )
    },
    span: (props) => {
      const name = (props as Record<string, unknown>)['data-mention']
      if (typeof name !== 'string') return <span className={props.className}>{props.children}</span>
      const loud = name === me.toLowerCase() || SPECIAL.has(name)
      return (
        <span data-mention={name} className={loud ? 'rounded bg-mention-bg px-0.5 font-medium text-mention-fg' : 'font-medium text-accent'}>
          {props.children}
        </span>
      )
    },
    table: ({ children }) => (
      <div className="overflow-x-auto">
        <table>{children}</table>
      </div>
    ),
  }
  return (
    <div className="md break-words">
      <ReactMarkdown remarkPlugins={plugins} skipHtml components={components}>
        {text}
      </ReactMarkdown>
    </div>
  )
})
