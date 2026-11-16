// createFocusSync serializes setFocused calls to Go so concurrent binding
// goroutines never apply a rapid blur/focus pair out of order. While one call
// is in flight, intermediate transitions coalesce to the latest boolean.
export function createFocusSync(send: (focused: boolean) => Promise<unknown>) {
  let inFlight = false
  let hasPending = false
  let pending = false
  let disposed = false

  const flush = () => {
    if (disposed || inFlight || !hasPending) return
    const next = pending
    hasPending = false
    inFlight = true
    let p: Promise<unknown>
    try {
      p = send(next)
    } catch {
      inFlight = false
      flush()
      return
    }
    void Promise.resolve(p)
      .catch(() => {})
      .finally(() => {
        inFlight = false
        flush()
      })
  }

  const update = (focused: boolean) => {
    if (disposed) return
    pending = focused
    hasPending = true
    flush()
  }

  update.dispose = () => {
    disposed = true
    hasPending = false
  }

  return update
}
