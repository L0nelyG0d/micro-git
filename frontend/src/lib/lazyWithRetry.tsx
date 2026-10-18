import { lazy } from "react"
import type { ComponentProps, ComponentType } from "react"

type Entry = { reset: () => void; retried: boolean }
const failed = new Set<Entry>()

/**
 * Like React.lazy, but a rejected import can be tried again. React.lazy caches
 * the rejection forever, so after a failure resetFailedLazies() makes the next
 * render create a fresh lazy component, which imports the chunk again.
 */
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function lazyWithRetry<T extends ComponentType<any>>(factory: () => Promise<{ default: T }>) {
  const entry: Entry = {
    retried: false,
    reset: () => {
      Inner = make()
    },
  }
  const make = () =>
    lazy(() =>
      factory().then(
        (m) => {
          entry.retried = false
          return m
        },
        (e) => {
          failed.add(entry)
          throw e
        },
      ),
    )
  let Inner = make()
  return function LazyPage(props: ComponentProps<T>) {
    return <Inner {...props} />
  }
}

/**
 * Called by the error boundary on Retry and on navigation: gives every lazy page
 * that failed a fresh import. Returns true when one of them already had that
 * second chance and failed again: browsers remember a failed module URL, so only
 * a page reload can fetch the chunk then and the caller should reload.
 */
export function resetFailedLazies(): boolean {
  let reload = false
  for (const entry of failed) {
    if (entry.retried) reload = true
    else {
      entry.retried = true
      entry.reset()
    }
  }
  failed.clear()
  return reload
}
