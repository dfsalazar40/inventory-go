/**
 * useWebSocket — live inventory synchronization hook.
 *
 * Protocol (research §6, FR-011, SC-005):
 *   1. On mount, open a WebSocket to /ws.
 *   2. On open, immediately fetch GET /items (REST snapshot) and call onItems with it.
 *      This reconciles the client to backend truth on every fresh connection.
 *   3. While connected, receive delta events and call onEvent with each one.
 *   4. On disconnect, reconnect with exponential backoff (starts at 1 s, caps at 30 s).
 *   5. On reconnect, repeat step 2 (fresh snapshot) before resuming delta processing.
 *   6. On unmount, close the socket and cancel pending reconnect timers.
 *
 * This pattern prevents permanent staleness after a dropped channel (spec edge case
 * "realtime channel drops").
 */

import { useCallback, useEffect, useRef, useState } from 'react'
import { apiFetch } from '../api/client'

/** Item shape as returned by GET /items (OpenAPI Item schema). */
export interface Item {
  id: string
  name: string
  totalStock: number
  reserved: number
  available: number
}

export interface StockEvent {
  type: 'reserved' | 'confirmed' | 'released' | 'expired'
  itemId: string
  reserved: number
  available: number
}

export interface UseWebSocketOptions {
  /** Called with the full item list immediately after connect/reconnect (snapshot). */
  onItems: (items: Item[]) => void
  /** Called for each delta event received while connected. */
  onEvent?: (event: StockEvent) => void
  /** WebSocket URL. Defaults to ws://<current host>/ws. */
  wsUrl?: string
  /** REST base URL for snapshot fetches. Defaults to http://<current host>. */
  apiBaseUrl?: string
}

// Derive the WebSocket URL from the same base as the REST client (client.ts),
// NOT from window.location — the frontend (e.g. :5173) and backend (:8080) are
// different origins, so using the page host points the socket at the frontend
// and the connection fails. Convert the http(s) scheme to ws(s).
const API_BASE = import.meta.env.VITE_API_URL ?? 'http://localhost:8080'
const WS_BASE = `${API_BASE.replace(/^http/, 'ws')}/ws`

const MIN_BACKOFF_MS = 1_000
const MAX_BACKOFF_MS = 30_000

/**
 * @returns `connected` — true while the WebSocket is open. Drives the live-status
 * indicator in the UI ("● Live" vs "Offline").
 */
export function useWebSocket({
  onItems,
  onEvent,
  wsUrl = WS_BASE,
}: UseWebSocketOptions): boolean {
  const [connected, setConnected] = useState(false)
  const wsRef = useRef<WebSocket | null>(null)
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const backoffRef = useRef<number>(MIN_BACKOFF_MS)
  const unmountedRef = useRef(false)

  // Keep stable callback references so the connect closure doesn't go stale.
  const onItemsRef = useRef(onItems)
  onItemsRef.current = onItems
  const onEventRef = useRef(onEvent)
  onEventRef.current = onEvent

  const fetchSnapshot = useCallback(async () => {
    try {
      const items = await apiFetch<Item[]>('/items')
      onItemsRef.current(items)
    } catch {
      // Snapshot fetch failure is non-fatal; next reconnect will retry.
    }
  }, [])

  const connect = useCallback(() => {
    if (unmountedRef.current) return

    const ws = new WebSocket(wsUrl)
    wsRef.current = ws

    ws.onopen = () => {
      if (unmountedRef.current) {
        ws.close()
        return
      }
      // Reset backoff on successful connection.
      backoffRef.current = MIN_BACKOFF_MS
      setConnected(true)
      // Fetch REST snapshot immediately after opening — reconciles to backend truth.
      fetchSnapshot()
    }

    ws.onmessage = (ev) => {
      if (unmountedRef.current) return
      try {
        const event = JSON.parse(ev.data as string) as StockEvent
        onEventRef.current?.(event)
      } catch {
        // Malformed message — ignore.
      }
    }

    ws.onclose = () => {
      setConnected(false)
      if (unmountedRef.current) return
      wsRef.current = null

      // Reconnect with exponential backoff.
      const delay = backoffRef.current
      backoffRef.current = Math.min(backoffRef.current * 2, MAX_BACKOFF_MS)

      reconnectTimerRef.current = setTimeout(() => {
        if (!unmountedRef.current) {
          connect()
        }
      }, delay)
    }

    ws.onerror = () => {
      // onclose will fire after onerror; let it handle reconnect.
      ws.close()
    }
  }, [wsUrl, fetchSnapshot])

  useEffect(() => {
    unmountedRef.current = false
    connect()

    return () => {
      unmountedRef.current = true
      if (reconnectTimerRef.current !== null) {
        clearTimeout(reconnectTimerRef.current)
        reconnectTimerRef.current = null
      }
      if (wsRef.current) {
        wsRef.current.close()
        wsRef.current = null
      }
    }
  }, [connect])

  return connected
}
