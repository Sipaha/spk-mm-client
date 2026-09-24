// Channel type marker shown before names (no icon font in stage 2).
export function channelGlyph(type: string): string {
  switch (type) {
    case 'P':
      return '🔒'
    case 'D':
      return '@'
    case 'G':
      return '👥'
    default:
      return '#'
  }
}
