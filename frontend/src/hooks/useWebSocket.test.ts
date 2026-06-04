/**
 * T032 [US2] — useWebSocket hook tests (test-first, must fail before implementation).
 *
 * Asserts:
 *   (a) The hook connects to the WebSocket URL on mount.
 *   (b) On a dropped connection it reconnects with exponential backoff.
 *   (c) On connect/reconnect it fetches a REST snapshot (GET /items) and reconciles
 *       to backend truth — snapshot-on-connect prevents stale state after a dropped channel.
 *
 * Strategy: fake WebSocket via a configurable mock class that records calls and allows
 * the test to trigger open/close/message events. Fake fetch for the REST snapshot.
 */

import { renderHook, act } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useWebSocket } from './useWebSocket'

// ── Mock WebSocket ────────────────────────────────────────────────────────────

type WSEvent = 'open' | 'close' | 'message' | 'error'

class MockWebSocket {
  static instances: MockWebSocket[] = []

  url: string
  readyState: number = WebSocket.CONNECTING
  onopen: ((ev: Event) => void) | null = null
  onclose: ((ev: CloseEvent) => void) | null = null
  onmessage: ((ev: MessageEvent) => void) | null = null
  onerror: ((ev: Event) => void) | null = null

  constructor(url: string) {
    this.url = url
    MockWebSocket.instances.push(this)
  }

  close() {
    this.readyState = WebSocket.CLOSED
  }

  send(_data: string) {
    /* no-op in tests */
  }

  /** Helper: simulate a successful connection. */
  triggerOpen() {
    this.readyState = WebSocket.OPEN
    this.onopen?.(new Event('open'))
  }

  /** Helper: simulate a server-side close. */
  triggerClose(code = 1006) {
    this.readyState = WebSocket.CLOSED
    this.onclose?.(new CloseEvent('close', { code, wasClean: code === 1000 }))
  }

  /** Helper: simulate receiving a JSON message. */
  triggerMessage(data: unknown) {
    this.onmessage?.(new MessageEvent('message', { data: JSON.stringify(data) }))
  }
}

// ── Fake items snapshot ───────────────────────────────────────────────────────

const SNAPSHOT = [
  { id: 'item-1', name: 'Gadget', totalStock: 10, reserved: 3, available: 7 },
  { id: 'item-2', name: 'Widget', totalStock: 5, reserved: 5, available: 0 },
]

// ── Setup / teardown ──────────────────────────────────────────────────────────

beforeEach(() => {
  MockWebSocket.instances = []
  vi.useFakeTimers()

  // Inject mock WebSocket into global scope.
  vi.stubGlobal('WebSocket', MockWebSocket)

  // Fake fetch returns the snapshot on GET /items.
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      if (url.includes('/items')) {
        return {
          ok: true,
          status: 200,
          json: async () => [...SNAPSHOT],
        }
      }
      return { ok: false, status: 404, json: async () => ({}) }
    }),
  )
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
  vi.clearAllMocks()
})

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('useWebSocket', () => {
  it('(a) connects to the WebSocket URL on mount', () => {
    const onItems = vi.fn()
    renderHook(() => useWebSocket({ onItems }))

    expect(MockWebSocket.instances).toHaveLength(1)
    expect(MockWebSocket.instances[0].url).toMatch(/ws/)
  })

  it('(c) fetches a REST snapshot on connect and calls onItems with it', async () => {
    const onItems = vi.fn()
    renderHook(() => useWebSocket({ onItems }))

    // Simulate the WebSocket opening.
    await act(async () => {
      MockWebSocket.instances[0].triggerOpen()
    })

    // apiFetch calls fetch with (url, options) — assert first arg contains /items.
    const [fetchUrl] = (fetch as ReturnType<typeof vi.fn>).mock.calls[0] as [string, ...unknown[]]
    expect(fetchUrl).toContain('/items')
    expect(onItems).toHaveBeenCalledWith(SNAPSHOT)
  })

  it('(b) reconnects after a dropped connection with backoff', async () => {
    const onItems = vi.fn()
    renderHook(() => useWebSocket({ onItems }))

    // Let first connection open and complete snapshot.
    await act(async () => {
      MockWebSocket.instances[0].triggerOpen()
    })

    const firstInstance = MockWebSocket.instances[0]

    // Drop the connection — simulates a network disruption.
    await act(async () => {
      firstInstance.triggerClose(1006)
    })

    // Should NOT reconnect immediately — backoff must elapse first.
    expect(MockWebSocket.instances).toHaveLength(1)

    // Advance timers past the minimum backoff (the implementation uses at least 1 s).
    await act(async () => {
      vi.advanceTimersByTime(2000)
    })

    // A second WebSocket must be created.
    expect(MockWebSocket.instances.length).toBeGreaterThan(1)
  })

  it('(c) fetches a REST snapshot on reconnect (reconcile to backend truth)', async () => {
    const onItems = vi.fn()
    renderHook(() => useWebSocket({ onItems }))

    // First connect → first snapshot.
    await act(async () => {
      MockWebSocket.instances[0].triggerOpen()
    })
    expect(onItems).toHaveBeenCalledTimes(1)

    // Drop connection.
    await act(async () => {
      MockWebSocket.instances[0].triggerClose(1006)
    })

    // Advance past backoff.
    await act(async () => {
      vi.advanceTimersByTime(2000)
    })

    // Open the reconnected socket.
    const second = MockWebSocket.instances[1]
    await act(async () => {
      second?.triggerOpen()
    })

    // Second snapshot fetch must have happened.
    expect(onItems).toHaveBeenCalledTimes(2)
  })

  it('calls onEvent when the server pushes a delta message', async () => {
    const onItems = vi.fn()
    const onEvent = vi.fn()
    renderHook(() => useWebSocket({ onItems, onEvent }))

    await act(async () => {
      MockWebSocket.instances[0].triggerOpen()
    })

    const delta = { type: 'reserved', itemId: 'item-1', reserved: 4, available: 6 }
    await act(async () => {
      MockWebSocket.instances[0].triggerMessage(delta)
    })

    expect(onEvent).toHaveBeenCalledWith(delta)
  })

  it('cleans up the WebSocket connection on unmount', async () => {
    const onItems = vi.fn()
    const { unmount } = renderHook(() => useWebSocket({ onItems }))

    await act(async () => {
      MockWebSocket.instances[0].triggerOpen()
    })

    const ws = MockWebSocket.instances[0]
    unmount()

    expect(ws.readyState).toBe(WebSocket.CLOSED)
  })
})
