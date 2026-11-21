import { useEffect, useState } from 'react'
import { useSearchParams } from 'react-router'

/** Returns value after it has stopped changing for delay ms (for search boxes). */
export function useDebounced<T>(value: T, delay = 300): T {
  const [debounced, setDebounced] = useState(value)
  useEffect(() => {
    const t = setTimeout(() => setDebounced(value), delay)
    return () => clearTimeout(t)
  }, [value, delay])
  return debounced
}

/** Re-renders every interval ms and returns the current time (for countdowns). */
export function useNow(interval = 1000): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), interval)
    return () => clearInterval(t)
  }, [interval])
  return now
}

/** Keeps a string filter in the URL query so lists are linkable and survive reloads. */
export function useQueryParam(name: string, fallback = ''): [string, (value: string) => void] {
  const [params, setParams] = useSearchParams()
  const value = params.get(name) ?? fallback
  const set = (next: string) => {
    setParams(
      (prev) => {
        const p = new URLSearchParams(prev)
        if (next) p.set(name, next)
        else p.delete(name)
        if (name !== 'page') p.delete('page')
        return p
      },
      { replace: true },
    )
  }
  return [value, set]
}
