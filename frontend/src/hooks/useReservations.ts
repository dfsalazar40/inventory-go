/**
 * useReservations — fetches and live-updates the current user's active reservations.
 *
 * Protocol (FR-012, research §6):
 *   1. On mount, fetches GET /reservations (scoped to X-User-Id via the api client).
 *   2. When a WebSocket event arrives (reserved/confirmed/released/expired), refetches
 *      to keep the panel in sync with backend truth. This is a reconcile-on-event
 *      pattern — simple and correct without trying to apply delta patches to complex
 *      reservation state.
 *
 * The hook does NOT open its own WebSocket; it receives events from the parent
 * component that owns useWebSocket. Call notifyEvent() whenever a StockEvent arrives.
 */

import { useCallback, useEffect, useRef, useState } from 'react'
import { apiFetch } from '../api/client'
import type { StockEvent } from './useWebSocket'

/** Reservation shape as returned by GET /reservations (OpenAPI Reservation schema). */
export interface Reservation {
  id: string
  itemId: string
  userId: string
  quantity: number
  status: 'pending' | 'confirmed' | 'released' | 'expired'
  createdAt: string
  /** ISO 8601 string when pending; null once confirmed. */
  expiresAt: string | null
  confirmedAt: string | null
  releasedAt: string | null
}

export interface UseReservationsResult {
  reservations: Reservation[]
  loading: boolean
  error: string | null
  /** Call this when a WebSocket event is received to trigger a refresh. */
  notifyEvent: (event: StockEvent) => void
  /** Manually refresh (e.g. after a confirm/release action). */
  refresh: () => void
}

export function useReservations(): UseReservationsResult {
  const [reservations, setReservations] = useState<Reservation[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const unmountedRef = useRef(false)

  const fetchReservations = useCallback(async () => {
    try {
      const data = await apiFetch<Reservation[]>('/reservations')
      if (!unmountedRef.current) {
        setReservations(data)
        setError(null)
      }
    } catch {
      if (!unmountedRef.current) {
        setError('Unable to load reservations. Please try again.')
      }
    } finally {
      if (!unmountedRef.current) {
        setLoading(false)
      }
    }
  }, [])

  useEffect(() => {
    unmountedRef.current = false
    fetchReservations()
    return () => {
      unmountedRef.current = true
    }
  }, [fetchReservations])

  const notifyEvent = useCallback(
    (_event: StockEvent) => {
      // Any stock/reservation event may affect our reservation list — refetch.
      fetchReservations()
    },
    [fetchReservations],
  )

  return {
    reservations,
    loading,
    error,
    notifyEvent,
    refresh: fetchReservations,
  }
}
