// The most common reaction names; the full emoji set (and custom emoji) is stage 3.
const EMOJI: Record<string, string> = {
  '+1': '👍', thumbsup: '👍', '-1': '👎', thumbsdown: '👎', heart: '❤️', smile: '😄', slightly_smiling_face: '🙂',
  grinning: '😀', laughing: '😆', joy: '😂', wink: '😉', tada: '🎉', eyes: '👀', fire: '🔥', pray: '🙏',
  clap: '👏', ok_hand: '👌', rocket: '🚀', thinking_face: '🤔', white_check_mark: '✅', heavy_check_mark: '✔️',
  x: '❌', '100': '💯', warning: '⚠️', muscle: '💪', wave: '👋', sob: '😭', cry: '😢', raised_hands: '🙌',
}

export const emojiFor = (name: string) => EMOJI[name] ?? `:${name}:`
