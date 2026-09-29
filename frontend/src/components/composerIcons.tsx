// Composer formatting-toolbar icons (Composer brief 2026-09-29). Kept in
// their own file rather than icons.tsx to avoid touching that file while
// another agent's UI-pass work is also landing there — see the Ruling in
// icons.tsx: inline SVG only, no icon-library dependency, real emoji never
// used as UI glyphs (the noEmojiIcons.test.ts guard scans this file too).
//
// Paths are Google's Material Icons ("ic:baseline-*"), the same source and
// license (Apache License 2.0, http://www.apache.org/licenses/LICENSE-2.0)
// icons.tsx already attributes: https://github.com/google/material-design-icons.
import type { IconProps } from './icons'

function Icon({ size = 18, className, children }: IconProps & { children: React.ReactNode }) {
  return (
    <svg viewBox="0 0 24 24" width={size} height={size} fill="currentColor" aria-hidden="true" focusable="false" className={className}>
      {children}
    </svg>
  )
}

export function IconBold(p: IconProps) {
  return (
    <Icon {...p}>
      <path d="M15.6 10.79c.97-.67 1.65-1.77 1.65-2.79 0-2.26-1.75-4-4-4H7v14h7.04c2.09 0 3.71-1.7 3.71-3.79 0-1.52-.86-2.82-2.15-3.42zM10 6.5h3c.83 0 1.5.67 1.5 1.5s-.67 1.5-1.5 1.5h-3v-3zm3.5 9H10v-3h3.5c.83 0 1.5.67 1.5 1.5s-.67 1.5-1.5 1.5z" />
    </Icon>
  )
}

export function IconItalic(p: IconProps) {
  return (
    <Icon {...p}>
      <path d="M10 4v3h2.21l-3.42 8H6v3h8v-3h-2.21l3.42-8H18V4z" />
    </Icon>
  )
}

export function IconStrikethrough(p: IconProps) {
  return (
    <Icon {...p}>
      <path d="M10 19h4v-3h-4zM5 4v3h5v3h4V7h5V4zM3 14h18v-2H3z" />
    </Icon>
  )
}

export function IconHeading(p: IconProps) {
  return (
    <Icon {...p}>
      <path d="M5 4v3h5.5v12h3V7H19V4z" />
    </Icon>
  )
}

export function IconCode(p: IconProps) {
  return (
    <Icon {...p}>
      <path d="M9.4 16.6L4.8 12l4.6-4.6L8 6l-6 6l6 6zm5.2 0l4.6-4.6l-4.6-4.6L16 6l6 6l-6 6z" />
    </Icon>
  )
}

export function IconQuote(p: IconProps) {
  return (
    <Icon {...p}>
      <path d="M6 17h3l2-4V7H5v6h3zm8 0h3l2-4V7h-6v6h3z" />
    </Icon>
  )
}

export function IconListBulleted(p: IconProps) {
  return (
    <Icon {...p}>
      <path d="M4 10.5c-.83 0-1.5.67-1.5 1.5s.67 1.5 1.5 1.5 1.5-.67 1.5-1.5-.67-1.5-1.5-1.5zm0-6c-.83 0-1.5.67-1.5 1.5S3.17 7.5 4 7.5 5.5 6.83 5.5 6 4.83 4.5 4 4.5zm0 12c-.83 0-1.5.68-1.5 1.5s.68 1.5 1.5 1.5 1.5-.68 1.5-1.5-.67-1.5-1.5-1.5zM7 19h14v-2H7zm0-6h14v-2H7zm0-8v2h14V5z" />
    </Icon>
  )
}

export function IconListNumbered(p: IconProps) {
  return (
    <Icon {...p}>
      <path d="M2 17h2v.5H3v1h1v.5H2v1h3v-4H2zm1-9h1V4H2v1h1zm-1 3h1.8L2 13.1v.9h3v-1H3.2L5 10.9V10H2zm5-6v2h14V5zm0 14h14v-2H7zm0-6h14v-2H7z" />
    </Icon>
  )
}

export function IconMood(p: IconProps) {
  return (
    <Icon {...p}>
      <path d="M11.99 2C6.47 2 2 6.48 2 12s4.47 10 9.99 10C17.52 22 22 17.52 22 12S17.52 2 11.99 2M12 20c-4.42 0-8-3.58-8-8s3.58-8 8-8 8 3.58 8 8-3.58 8-8 8m3.5-9c.83 0 1.5-.67 1.5-1.5S16.33 8 15.5 8 14 8.67 14 9.5s.67 1.5 1.5 1.5m-7 0c.83 0 1.5-.67 1.5-1.5S9.33 8 8.5 8 7 8.67 7 9.5 7.67 11 8.5 11m3.5 6.5c2.33 0 4.31-1.46 5.11-3.5H6.89c.8 2.04 2.78 3.5 5.11 3.5" />
    </Icon>
  )
}

export function IconSend(p: IconProps) {
  return (
    <Icon {...p}>
      <path d="M2.01 21L23 12 2.01 3 2 10l15 2-15 2z" />
    </Icon>
  )
}
