// Capture is bounded and local. LAIHub consumes PCM16 mono 16 kHz WAV.
export const MAX_RECORDING_SECONDS = 60
export type Recording = { stop(): Promise<string>; cancel(): void }

export function encodeWAV(samples: Float32Array): Uint8Array {
  const bytes = new Uint8Array(44 + samples.length * 2)
  const view = new DataView(bytes.buffer)
  const word = (offset: number, text: string) => {
    for (let i = 0; i < text.length; i++) bytes[offset + i] = text.charCodeAt(i)
  }
  word(0, 'RIFF'); word(8, 'WAVEfmt '); word(36, 'data')
  view.setUint32(4, bytes.length - 8, true)
  view.setUint32(16, 16, true)
  view.setUint16(20, 1, true); view.setUint16(22, 1, true)
  view.setUint32(24, 16000, true); view.setUint32(28, 32000, true)
  view.setUint16(32, 2, true); view.setUint16(34, 16, true)
  view.setUint32(40, samples.length * 2, true)
  for (let i = 0; i < samples.length; i++) {
    const value = Math.max(-1, Math.min(1, samples[i]))
    view.setInt16(44 + i * 2, Math.round(value * (value < 0 ? 32768 : 32767)), true)
  }
  return bytes
}

export function base64(bytes: Uint8Array): string {
  let binary = ''
  for (let i = 0; i < bytes.length; i += 8192) binary += String.fromCharCode(...bytes.subarray(i, i + 8192))
  return btoa(binary)
}

export async function recordAudio(signal: AbortSignal, onLimit: () => void, onEnded: () => void): Promise<Recording> {
  if (!navigator.mediaDevices?.getUserMedia || typeof AudioContext === 'undefined') throw new Error('microphone_unsupported')
  signal.throwIfAborted()
  // Start/resume inside the click gesture, before awaiting the permission prompt.
  const context = new AudioContext()
  let stream: MediaStream | undefined
  let source: MediaStreamAudioSourceNode | undefined
  let node: ScriptProcessorNode | undefined
  let chunks: Float32Array[] = []
  let count = 0
  let closed = false
  let limitReached = false
  const resume = context.resume()
  void resume.catch(() => {})
  const cleanup = () => {
    if (closed) return
    closed = true
    signal.removeEventListener('abort', cleanup)
    if (node) { node.onaudioprocess = null; node.disconnect() }
    source?.disconnect()
    stream?.getTracks().forEach((track) => { track.onended = null; track.stop() })
    void context.close().catch(() => {})
  }
  signal.addEventListener('abort', cleanup, { once: true })
  try {
    stream = await navigator.mediaDevices.getUserMedia({ audio: { channelCount: 1, echoCancellation: true, noiseSuppression: true }, video: false })
    // A permission response may arrive after cancellation/unmount.
    if (signal.aborted) { stream.getTracks().forEach((track) => track.stop()); signal.throwIfAborted() }
    await resume
    signal.throwIfAborted()
    source = context.createMediaStreamSource(stream)
    node = context.createScriptProcessor(4096, 1, 1)
    node.onaudioprocess = (event) => {
      if (closed) return
      const input = event.inputBuffer.getChannelData(0)
      const remaining = Math.floor(context.sampleRate * MAX_RECORDING_SECONDS) - count
      const chunk = input.slice(0, remaining)
      if (chunk.length) { chunks.push(chunk); count += chunk.length }
      if (count >= context.sampleRate * MAX_RECORDING_SECONDS && !limitReached) {
        limitReached = true
        // Leave the audio callback before tearing down its node.
        queueMicrotask(onLimit)
      }
    }
    stream.getAudioTracks().forEach((track) => { track.onended = onEnded })
    source.connect(node)
    node.connect(context.destination) // output is silent; input is never monitored
    return {
      cancel: () => { cleanup(); chunks = [] },
      stop: async () => {
        cleanup()
        signal.throwIfAborted()
        if (count < context.sampleRate / 10) throw new Error('microphone_too_short')
        const pcm = new Float32Array(count)
        let offset = 0
        for (const chunk of chunks) { pcm.set(chunk, offset); offset += chunk.length }
        chunks = []
        const offline = new OfflineAudioContext(1, Math.min(16000 * MAX_RECORDING_SECONDS, Math.round(count * 16000 / context.sampleRate)), 16000)
        const buffer = offline.createBuffer(1, pcm.length, context.sampleRate)
        buffer.copyToChannel(pcm, 0)
        const input = offline.createBufferSource()
        input.buffer = buffer
        input.connect(offline.destination)
        input.start()
        const result = await offline.startRendering()
        signal.throwIfAborted()
        return base64(encodeWAV(result.getChannelData(0)))
      },
    }
  } catch (error) {
    cleanup()
    chunks = []
    // Consume a rejected resume promise even when permission acquisition failed.
    void resume.catch(() => {})
    throw error
  }
}
