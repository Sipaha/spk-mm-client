import { useCallback, useEffect, useRef, useState } from 'react'
import { client } from '../api/client'
import { recordAudio, type Recording, MAX_RECORDING_SECONDS } from '../dictationAudio'
import { errorMessage } from '../errors'
import { t } from '../i18n'

// Both composers share the microphone; beginning another cancels the old lease.
type Attempt = { controller: AbortController; recording?: Recording; timer?: ReturnType<typeof setInterval> }
let cancelOwner: (() => void) | undefined
export function useDictation(onText: (text: string) => void, disabled: boolean) {
  const [state, setState] = useState<'idle' | 'requesting' | 'recording' | 'transcribing'>('idle')
  const [seconds, setSeconds] = useState(0)
  const [error, setError] = useState<string | null>(null)
  const callback = useRef(onText)
  callback.current = onText
  const attempt = useRef<Attempt | null>(null)
  const cancel = useCallback(() => {
    const current = attempt.current
    attempt.current = null
    if (current) { clearInterval(current.timer); current.controller.abort(); current.recording?.cancel() }
    if (cancelOwner === cancel) cancelOwner = undefined
    setState('idle')
  }, [])
  const cancelRef = useRef(cancel)
  cancelRef.current = cancel
  const finish = async () => {
    const current = attempt.current
    if (!current?.recording || current.controller.signal.aborted) return
    const recording = current.recording
    current.recording = undefined // stop is consumed once, including automatic stop
    clearInterval(current.timer)
    setState('transcribing')
    try {
      const audio = await recording.stop()
      if (attempt.current !== current) return
      const text = await client.transcribeAudio(audio, current.controller.signal)
      if (attempt.current !== current) return
      callback.current(text)
      cancelRef.current()
    } catch (e) {
      if (attempt.current !== current) return
      cancelRef.current()
      setError(e instanceof Error && e.message === 'microphone_too_short' ? t('dictation.tooShort') : errorMessage(e))
    }
  }
  const finishRef = useRef(finish)
  finishRef.current = finish
  const start = async () => {
    if (disabled || attempt.current) return
    cancelOwner?.()
    const current: Attempt = { controller: new AbortController() }
    attempt.current = current
    cancelOwner = cancelRef.current
    setError(null)
    setSeconds(0)
    setState('requesting')
    try {
      const recording = await recordAudio(current.controller.signal, () => void finishRef.current(), () => {
        if (attempt.current === current) { cancelRef.current(); setError(t('dictation.disconnected')) }
      })
      if (attempt.current !== current) { recording.cancel(); return }
      current.recording = recording
      setState('recording')
      const started = Date.now()
      current.timer = setInterval(() => {
        setSeconds(Math.min(MAX_RECORDING_SECONDS, Math.floor((Date.now() - started) / 1000)))
        if (Date.now() - started >= MAX_RECORDING_SECONDS * 1000) void finishRef.current()
      }, 250)
    } catch (e) {
      if (attempt.current !== current) return
      cancelRef.current()
      const denied = e instanceof DOMException && (e.name === 'NotAllowedError' || e.name === 'SecurityError')
      setError(denied ? t('dictation.denied') : t('dictation.microphoneError'))
    }
  }
  useEffect(() => {
    const hide = () => { if (document.hidden) cancelRef.current() }
    document.addEventListener('visibilitychange', hide)
    return () => { document.removeEventListener('visibilitychange', hide); cancelRef.current() }
  }, [])
  useEffect(() => { if (disabled) cancelRef.current() }, [disabled])
  return { state, seconds, error, busy: state !== 'idle', start, finish, cancel }
}
