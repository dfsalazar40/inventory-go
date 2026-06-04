/**
 * useCountdown — live countdown timer synchronized to a backend expiresAt timestamp.
 *
 * Returns:
 *   - number of whole seconds remaining (clamped to 0, never negative) when
 *     expiresAt is a non-null ISO string (pending reservations only).
 *   - null when expiresAt is null (confirmed reservations — no countdown).
 *
 * The hook ticks every second via setInterval and cleans up on unmount.
 * Because it derives remaining time from Date.now() vs the expiresAt timestamp
 * on every tick, it is naturally synchronized with the backend — no drift
 * accumulates from interval jitter.
 */

import { useEffect, useState } from 'react'

function remaining(expiresAt: string): number {
  return Math.max(0, Math.floor((new Date(expiresAt).getTime() - Date.now()) / 1000))
}

/**
 * @param expiresAt - ISO 8601 date string or null.
 *   null means the reservation is confirmed (no countdown).
 * @returns remaining seconds (≥ 0) or null if no countdown applies.
 */
export function useCountdown(expiresAt: string | null): number | null {
  const [seconds, setSeconds] = useState<number | null>(() => {
    if (expiresAt === null) return null
    return remaining(expiresAt)
  })

  useEffect(() => {
    if (expiresAt === null) {
      setSeconds(null)
      return
    }

    // Update immediately in case the expiresAt changed.
    setSeconds(remaining(expiresAt))

    const id = setInterval(() => {
      setSeconds(remaining(expiresAt))
    }, 1_000)

    return () => clearInterval(id)
  }, [expiresAt])

  return seconds
}
