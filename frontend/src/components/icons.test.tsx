import { render } from '@testing-library/react'
import * as Icons from './icons'

// Every exported Icon* is a small wrapper component; this iterates all of
// them instead of hand-listing each name, so a newly added icon is covered
// automatically and a removed one can't leave a stale test behind.
const names = Object.keys(Icons).filter((k) => k.startsWith('Icon')) as (keyof typeof Icons)[]

test('there is at least one icon to test (glob sanity)', () => {
  expect(names.length).toBeGreaterThan(15)
})

test.each(names)('%s renders an svg with the fixed attributes and the default 18px size', (name) => {
  const Component = Icons[name] as (p: { size?: number; className?: string }) => React.ReactElement
  const { container } = render(<Component />)
  const svg = container.querySelector('svg')!
  expect(svg).toBeInTheDocument()
  expect(svg).toHaveAttribute('viewBox', '0 0 24 24')
  expect(svg).toHaveAttribute('fill', 'currentColor')
  expect(svg).toHaveAttribute('aria-hidden', 'true')
  expect(svg).toHaveAttribute('focusable', 'false')
  expect(svg).toHaveAttribute('width', '18')
  expect(svg).toHaveAttribute('height', '18')
  // Never a text/emoji fallback rendered alongside or instead of the svg.
  expect(container).not.toHaveTextContent(/[\u{1F300}-\u{1FAFF}]/u)
})

test.each(names)('%s honors a custom size and className', (name) => {
  const Component = Icons[name] as (p: { size?: number; className?: string }) => React.ReactElement
  const { container } = render(<Component size={24} className="text-danger" />)
  const svg = container.querySelector('svg')!
  expect(svg).toHaveAttribute('width', '24')
  expect(svg).toHaveAttribute('height', '24')
  expect(svg).toHaveClass('text-danger')
})

test.each(names)('%s has at least one path or group with drawable content', (name) => {
  const Component = Icons[name] as (p: { size?: number; className?: string }) => React.ReactElement
  const { container } = render(<Component />)
  expect(container.querySelector('svg path')).toBeInTheDocument()
})

// ChannelTypeMarker isn't a plain Icon* wrapper (it switches on channel
// type, and direct messages render plain "@" text, not an icon), so it's
// covered separately rather than by the generic sweep above.
test('ChannelTypeMarker: private/group/public are icons, direct message stays plain "@" text', () => {
  const { container: priv } = render(<Icons.ChannelTypeMarker type="P" />)
  expect(priv.querySelector('svg')).toBeInTheDocument()

  const { container: group } = render(<Icons.ChannelTypeMarker type="G" />)
  expect(group.querySelector('svg')).toBeInTheDocument()

  const { container: pub } = render(<Icons.ChannelTypeMarker type="O" />)
  expect(pub.querySelector('svg')).toBeInTheDocument()

  const { container: dm } = render(<Icons.ChannelTypeMarker type="D" />)
  expect(dm.querySelector('svg')).toBeNull()
  expect(dm).toHaveTextContent('@')
})
