import type { ReactNode } from 'react'

type Rule = { token: string; pattern: RegExp }

const common: Rule[] = [
  { token: 'comment', pattern: /^\/\/[^\n]*|^\/\*[\s\S]*?\*\// },
  { token: 'string', pattern: /^"(?:\\.|[^"\\])*"|^'(?:\\.|[^'\\])*'|^`(?:\\.|[^`\\])*`/ },
  { token: 'number', pattern: /^\b(?:0x[\da-f]+|\d+(?:\.\d+)?)\b/i },
  { token: 'literal', pattern: /^\b(?:true|false|null|nil|undefined)\b/ },
]

const rules: Record<string, Rule[]> = {
  shell: [
    { token: 'comment', pattern: /^#[^\n]*/ },
    { token: 'string', pattern: /^"(?:\\.|[^"\\])*"|^'[^']*'/ },
    { token: 'variable', pattern: /^\$\{?[A-Za-z_][\w]*\}?/ },
    { token: 'keyword', pattern: /^\b(?:if|then|elif|else|fi|for|while|in|do|done|case|esac|function)\b/ },
    { token: 'command', pattern: /^\b(?:kubectl|docker|podman|git|npm|pnpm|go|make|curl|wget|ssh|sudo)\b/ },
    { token: 'property', pattern: /^--?[\w-]+/ },
    ...common.slice(2),
  ],
  yaml: [
    { token: 'comment', pattern: /^#[^\n]*/ },
    { token: 'property', pattern: /^[A-Za-z_][\w.-]*(?=\s*:)/ },
    { token: 'string', pattern: /^"(?:\\.|[^"\\])*"|^'[^']*'/ },
    { token: 'literal', pattern: /^\b(?:true|false|null|yes|no|on|off)\b/i },
    { token: 'number', pattern: /^\b\d+(?:\.\d+)?\b/ },
    { token: 'punctuation', pattern: /^[{}[\],:&*!>|-]/ },
  ],
  json: [
    { token: 'property', pattern: /^"(?:\\.|[^"\\])*"(?=\s*:)/ },
    { token: 'string', pattern: /^"(?:\\.|[^"\\])*"/ },
    ...common.slice(2),
    { token: 'punctuation', pattern: /^[{}[\],:]/ },
  ],
  go: [
    ...common,
    { token: 'keyword', pattern: /^\b(?:break|case|chan|const|continue|default|defer|else|fallthrough|for|func|go|goto|if|import|interface|map|package|range|return|select|struct|switch|type|var)\b/ },
    { token: 'type', pattern: /^\b(?:bool|byte|complex64|complex128|error|float32|float64|int|int8|int16|int32|int64|rune|string|uint|uint8|uint16|uint32|uint64|uintptr)\b/ },
  ],
  javascript: [
    ...common,
    { token: 'keyword', pattern: /^\b(?:async|await|break|case|catch|class|const|continue|default|delete|do|else|export|extends|finally|for|from|function|if|import|in|instanceof|let|new|of|return|static|switch|throw|try|typeof|var|void|while|yield)\b/ },
  ],
  python: [
    { token: 'comment', pattern: /^#[^\n]*/ },
    { token: 'string', pattern: /^(?:[rubf]{0,2})(?:"""[\s\S]*?"""|'''[\s\S]*?'''|"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*')/i },
    ...common.slice(2),
    { token: 'keyword', pattern: /^\b(?:and|as|assert|async|await|break|class|continue|def|del|elif|else|except|finally|for|from|global|if|import|in|is|lambda|nonlocal|not|or|pass|raise|return|try|while|with|yield)\b/ },
  ],
}

const aliases: Record<string, string> = {
  bash: 'shell',
  sh: 'shell',
  zsh: 'shell',
  yml: 'yaml',
  js: 'javascript',
  jsx: 'javascript',
  ts: 'javascript',
  tsx: 'javascript',
  py: 'python',
  golang: 'go',
}

function highlight(source: string, language: string): ReactNode[] {
  const selected = rules[aliases[language] ?? language] ?? common
  const out: ReactNode[] = []
  let plain = ''
  let offset = 0
  const flush = () => {
    if (!plain) return
    out.push(plain)
    plain = ''
  }
  while (offset < source.length) {
    const rest = source.slice(offset)
    const match = selected.map((rule) => ({ rule, value: rule.pattern.exec(rest)?.[0] })).find((it) => it.value)
    if (!match?.value) {
      plain += source[offset]
      offset++
      continue
    }
    flush()
    out.push(<span key={offset} className={`syntax-${match.rule.token}`}>{match.value}</span>)
    offset += match.value.length
  }
  flush()
  return out
}

export function HighlightedCode({ className, children }: { className?: string; children?: ReactNode }) {
  const source = String(children ?? '')
  const language = /(?:^|\s)language-([\w+-]+)/.exec(className ?? '')?.[1]?.toLowerCase() ?? ''
  const fenced = !!language || source.endsWith('\n')
  if (!fenced) return <code className={className}>{children}</code>
  return <code className={className} data-language={language || undefined}>{highlight(source, language)}</code>
}
