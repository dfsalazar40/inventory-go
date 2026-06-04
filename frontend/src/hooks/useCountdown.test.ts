/**
 * T042 [US5] — useCountdown hook tests (test-first; must fail before implementation).
 *
 * Asserts:
 *   (a) Counts down each second from a future expiresAt.
 *   (b) Does not go negative — clamps at 0.
 *   (c) Returns 0 immediately when expiresAt is in the past (already expired).
 *   (d) Returns null when expiresAt is null (confirmed reservation — no countdown).
 */

import { renderHook, act } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { vi } from 'vitest'
import { useCountdown } from './useCountdown'

// ── Setup / teardown ──────────────────────────────────────────────────────────

beforeEach(() => {
  vi.useFakeTimers()
})

afterEach(() => {
  vi.useRealTimers()
})

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('useCountdown', () => {
  it('(a) counts down each second from a future expiresAt', () => {
    // expiresAt is 10 seconds from "now" (fake-timer epoch)
    const expiresAt = new Date(Date.now() + 10_000).toISOString()
    const { result } = renderHook(() => useCountdown(expiresAt))

    // Initial value is ~10 s
    expect(result.current).toBeGreaterThanOrEqual(9)
    expect(result.current).toBeLessThanOrEqual(10)

    // Advance 3 seconds — should drop by ~3
    act(() => {
      vi.advanceTimersByTime(3_000)
    })

    expect(result.current).toBeGreaterThanOrEqual(6)
    expect(result.current).toBeLessThanOrEqual(7)
  })

  it('(b) does not go negative — clamps at 0 after expiry', () => {
    const expiresAt = new Date(Date.now() + 2_000).toISOString()
    const { result } = renderHook(() => useCountdown(expiresAt))

    // Advance well past expiry
    act(() => {
      vi.advanceTimersByTime(10_000)
    })

    expect(result.current).toBe(0)
  })

  it('(c) returns 0 immediately when expiresAt is already in the past', () => {
    const expiresAt = new Date(Date.now() - 5_000).toISOString()
    const { result } = renderHook(() => useCountdown(expiresAt))

    expect(result.current).toBe(0)
  })

  it('(d) returns null when expiresAt is null (confirmed reservation)', () => {
    const { result } = renderHook(() => useCountdown(null))

    expect(result.current).toBeNull()
  })

  it('(e) cleans up the interval on unmount (no state updates after unmount)', () => {
    const expiresAt = new Date(Date.now() + 30_000).toISOString()
    const { result, unmount } = renderHook(() => useCountdown(expiresAt))

    const countBeforeUnmount = result.current

    unmount()

    // Advancing time after unmount must not cause errors or state updates
    act(() => {
      vi.advanceTimersByTime(5_000)
    })

    // result.current stays at the value it had at unmount (hook is gone)
    expect(result.current).toBe(countBeforeUnmount)
  })
})
