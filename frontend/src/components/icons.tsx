// Monochrome inline SVG icons (UI pass, 2026-09-28 — see the Ruling in
// docs/plans/2026-09-28-*). No icon library dependency: a bundle plus its
// runtime memory cost more than these ~25 paths.
//
// Paths are taken from Google's Material Symbols / Material Icons
// (https://github.com/google/material-design-icons), licensed under the
// Apache License 2.0 (http://www.apache.org/licenses/LICENSE-2.0). Three
// icons (IconExpand, IconAddReaction, IconMarkUnread) only exist as
// Material Symbols, whose source grid is 24px sized but authored on a
// "0 -960 960 960" viewBox; SYMBOL normalizes that onto our own
// "0 0 24 24" box via a transform so every icon shares one wrapper.
//
// Real emoji (message text, reactions, channel-type glyphs) never go
// through here — see EmojiGlyph.tsx and glyph.ts.

export interface IconProps {
  size?: number
  className?: string
}

function Icon({ size = 18, className, children }: IconProps & { children: React.ReactNode }) {
  return (
    <svg viewBox="0 0 24 24" width={size} height={size} fill="currentColor" aria-hidden="true" focusable="false" className={className}>
      {children}
    </svg>
  )
}

// SYMBOL wraps a Material *Symbols* path (authored on a 960-unit grid,
// viewBox "0 -960 960 960") in a transform that maps it onto our shared
// 24x24 box: scale(24/960) then shift the origin from (0,-960) to (0,0).
function Symbol({ size, className, children }: IconProps & { children: React.ReactNode }) {
  return (
    <Icon size={size} className={className}>
      <g transform="translate(0 24) scale(0.025)">{children}</g>
    </Icon>
  )
}

export function IconDownload(p: IconProps) {
  return (
    <Icon {...p}>
      <path d="M19 9h-4V3H9v6H5l7 7 7-7zM5 18v2h14v-2H5z" />
    </Icon>
  )
}

export function IconOpenExternal(p: IconProps) {
  return (
    <Icon {...p}>
      <path d="M19 19H5V5h7V3H5c-1.11 0-2 .9-2 2v14c0 1.1.89 2 2 2h14c1.1 0 2-.9 2-2v-7h-2v7zM14 3v2h3.59l-9.83 9.83 1.41 1.41L19 6.41V10h2V3h-7z" />
    </Icon>
  )
}

export function IconAttach(p: IconProps) {
  return (
    <Icon {...p}>
      <path d="M16.5 6v11.5c0 2.21-1.79 4-4 4s-4-1.79-4-4V5c0-1.38 1.12-2.5 2.5-2.5s2.5 1.12 2.5 2.5v10.5c0 .55-.45 1-1 1s-1-.45-1-1V6H10v9.5c0 1.38 1.12 2.5 2.5 2.5s2.5-1.12 2.5-2.5V5c0-2.21-1.79-4-4-4S7 2.79 7 5v12.5c0 3.04 2.46 5.5 5.5 5.5s5.5-2.46 5.5-5.5V6h-1.5z" />
    </Icon>
  )
}

export function IconExpand(p: IconProps) {
  return (
    <Symbol {...p}>
      <path d="M120-120v-320h80v184l504-504H520v-80h320v320h-80v-184L256-200h184v80H120Z" />
    </Symbol>
  )
}

export function IconFile(p: IconProps) {
  return (
    <Icon {...p}>
      <path d="M6 2c-1.1 0-1.99.9-1.99 2L4 20c0 1.1.89 2 1.99 2H18c1.1 0 2-.9 2-2V8l-6-6H6zm7 7V3.5L18.5 9H13z" />
    </Icon>
  )
}

export function IconImage(p: IconProps) {
  return (
    <Icon {...p}>
      <path d="M21 19V5c0-1.1-.9-2-2-2H5c-1.1 0-2 .9-2 2v14c0 1.1.9 2 2 2h14c1.1 0 2-.9 2-2zM8.5 13.5l2.5 3.01L14.5 12l4.5 6H5l3.5-4.5z" />
    </Icon>
  )
}

export function IconVideo(p: IconProps) {
  return (
    <Icon {...p}>
      <path d="M17 10.5V7c0-.55-.45-1-1-1H4c-.55 0-1 .45-1 1v10c0 .55.45 1 1 1h12c.55 0 1-.45 1-1v-3.5l4 4v-11l-4 4z" />
    </Icon>
  )
}

export function IconAudio(p: IconProps) {
  return (
    <Icon {...p}>
      <path d="M12 3v9.28c-.47-.17-.97-.28-1.5-.28C8.01 12 6 14.01 6 16.5S8.01 21 10.5 21c2.31 0 4.2-1.75 4.45-4H15V6h4V3h-7z" />
    </Icon>
  )
}

export function IconNote(p: IconProps) {
  return (
    <Icon {...p}>
      <path d="M14 2H6c-1.1 0-1.99.9-1.99 2L4 20c0 1.1.89 2 1.99 2H18c1.1 0 2-.9 2-2V8l-6-6zm2 16H8v-2h8v2zm0-4H8v-2h8v2zm-3-5V3.5L18.5 9H13z" />
    </Icon>
  )
}

export function IconFolder(p: IconProps) {
  return (
    <Icon {...p}>
      <path d="M10 4H4c-1.1 0-1.99.9-1.99 2L2 18c0 1.1.9 2 2 2h16c1.1 0 2-.9 2-2V8c0-1.1-.9-2-2-2h-8l-2-2z" />
    </Icon>
  )
}

export function IconClose(p: IconProps) {
  return (
    <Icon {...p}>
      <path d="M19 6.41L17.59 5 12 10.59 6.41 5 5 6.41 10.59 12 5 17.59 6.41 19 12 13.41 17.59 19 19 17.59 13.41 12z" />
    </Icon>
  )
}

export function IconAddReaction(p: IconProps) {
  return (
    <Symbol {...p}>
      <path d="M480-480Zm0 400q-83 0-156-31.5T197-197q-54-54-85.5-127T80-480q0-83 31.5-156T197-763q54-54 127-85.5T480-880q43 0 83 8.5t77 24.5v90q-35-20-75.5-31.5T480-800q-133 0-226.5 93.5T160-480q0 133 93.5 226.5T480-160q133 0 226.5-93.5T800-480q0-32-6.5-62T776-600h86q9 29 13.5 58.5T880-480q0 83-31.5 156T763-197q-54 54-127 85.5T480-80Zm320-600v-80h-80v-80h80v-80h80v80h80v80h-80v80h-80ZM620-520q25 0 42.5-17.5T680-580q0-25-17.5-42.5T620-640q-25 0-42.5 17.5T560-580q0 25 17.5 42.5T620-520Zm-280 0q25 0 42.5-17.5T400-580q0-25-17.5-42.5T340-640q-25 0-42.5 17.5T280-580q0 25 17.5 42.5T340-520Zm140 260q68 0 123.5-38.5T684-400H276q25 63 80.5 101.5T480-260Z" />
    </Symbol>
  )
}

export function IconEdit(p: IconProps) {
  return (
    <Icon {...p}>
      <path d="M3 17.25V21h3.75L17.81 9.94l-3.75-3.75L3 17.25zM20.71 7.04c.39-.39.39-1.02 0-1.41l-2.34-2.34c-.39-.39-1.02-.39-1.41 0l-1.83 1.83 3.75 3.75 1.83-1.83z" />
    </Icon>
  )
}

export function IconMarkUnread(p: IconProps) {
  return (
    <Symbol {...p}>
      <path d="M80-80v-720q0-33 23.5-56.5T160-880h404q-4 20-4 40t4 40H160v525l46-45h594v-324q23-5 43-13.5t37-22.5v360q0 33-23.5 56.5T800-240H240L80-80Zm80-720v480-480Zm600 80q-50 0-85-35t-35-85q0-50 35-85t85-35q50 0 85 35t35 85q0 50-35 85t-85 35Z" />
    </Symbol>
  )
}

export function IconLink(p: IconProps) {
  return (
    <Icon {...p}>
      <path d="M3.9 12c0-1.71 1.39-3.1 3.1-3.1h4V7H7c-2.76 0-5 2.24-5 5s2.24 5 5 5h4v-1.9H7c-1.71 0-3.1-1.39-3.1-3.1zM8 13h8v-2H8v2zm9-6h-4v1.9h4c1.71 0 3.1 1.39 3.1 3.1s-1.39 3.1-3.1 3.1h-4V17h4c2.76 0 5-2.24 5-5s-2.24-5-5-5z" />
    </Icon>
  )
}

export function IconDelete(p: IconProps) {
  return (
    <Icon {...p}>
      <path d="M6 19c0 1.1.9 2 2 2h8c1.1 0 2-.9 2-2V7H6v12zM19 4h-3.5l-1-1h-5l-1 1H5v2h14V4z" />
    </Icon>
  )
}

export function IconMore(p: IconProps) {
  return (
    <Icon {...p}>
      <path d="M6 10c-1.1 0-2 .9-2 2s.9 2 2 2 2-.9 2-2-.9-2-2-2zm12 0c-1.1 0-2 .9-2 2s.9 2 2 2 2-.9 2-2-.9-2-2-2zm-6 0c-1.1 0-2 .9-2 2s.9 2 2 2 2-.9 2-2-.9-2-2-2z" />
    </Icon>
  )
}

export function IconBookmark(p: IconProps) {
  return (
    <Icon {...p}>
      <path d="M17 3H7c-1.1 0-1.99.9-1.99 2L5 21l7-3 7 3V5c0-1.1-.9-2-2-2zm0 15l-5-2.18L7 18V5h10v13z" />
    </Icon>
  )
}

export function IconBookmarkFilled(p: IconProps) {
  return (
    <Icon {...p}>
      <path d="M17 3H7c-1.1 0-1.99.9-1.99 2L5 21l7-3 7 3V5c0-1.1-.9-2-2-2z" />
    </Icon>
  )
}

export function IconChevronRight(p: IconProps) {
  return (
    <Icon {...p}>
      <path d="M10 6L8.59 7.41 13.17 12l-4.58 4.59L10 18l6-6z" />
    </Icon>
  )
}

export function IconChevronDown(p: IconProps) {
  return (
    <Icon {...p}>
      <path d="M16.59 8.59L12 13.17 7.41 8.59 6 10l6 6 6-6z" />
    </Icon>
  )
}

export function IconPlay(p: IconProps) {
  return (
    <Icon {...p}>
      <path d="M8 5v14l11-7z" />
    </Icon>
  )
}

export function IconReply(p: IconProps) {
  return (
    <Icon {...p}>
      <path d="M10 9V5l-7 7 7 7v-4.1c5 0 8.5 1.6 11 5.1-1-5-4-10-11-11z" />
    </Icon>
  )
}
