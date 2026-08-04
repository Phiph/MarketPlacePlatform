import { useEffect, useRef } from 'react'

// Calls `fn` immediately and then every `intervalMs` for as long as the
// component stays mounted with the same `deps`. Used to keep request-status
// views live without a manual refresh, since provisioning happens
// asynchronously on the cluster after a request is submitted.
export function usePolling(fn: () => void, intervalMs: number, deps: unknown[]) {
  const fnRef = useRef(fn)
  fnRef.current = fn

  useEffect(() => {
    fnRef.current()
    const id = setInterval(() => fnRef.current(), intervalMs)
    return () => clearInterval(id)
    // fn is intentionally excluded - callers pass a fresh closure each
    // render, only `deps` should restart the interval.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps)
}
