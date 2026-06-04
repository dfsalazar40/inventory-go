/**
 * InventoryDashboard — live inventory view + user's active reservations panel.
 *
 * Connects to the backend WebSocket hub via useWebSocket. On every connect or
 * reconnect it fetches a fresh REST snapshot from GET /items, then applies live
 * delta events as they arrive — preventing permanent staleness after a dropped
 * channel (research §6, SC-005).
 *
 * T047 [US7]: reserve errors are mapped to typed ApiErrorCode values and passed
 * down to ItemCard as distinct, user-readable messages (FR-013, SC-007).
 * A double-submit guard prevents concurrent reserve requests on the same item.
 *
 * US5: the ReservationPanel renders below the inventory grid and is kept in sync
 * by the same WebSocket event stream (notifyEvent → refetch).
 */

import { useState, useCallback } from 'react'
import { useWebSocket, type Item, type StockEvent } from '../hooks/useWebSocket'
import { useReservations } from '../hooks/useReservations'
import { ItemCard, type ApiErrorCode } from './ItemCard'
import { ReservationPanel } from './ReservationPanel'
import { apiFetch, ApiRequestError } from '../api/client'
import { generateIdempotencyKey } from '../lib/identity'

/** Extract a typed ApiErrorCode from an unknown thrown error. */
function toErrorCode(err: unknown): ApiErrorCode {
  if (err instanceof ApiRequestError) {
    const code = err.body.error
    // Only forward codes that are part of our typed set.
    const typed: ApiErrorCode[] = [
      'conflict',
      'insufficient_stock',
      'validation_error',
      'idempotency_key_conflict',
      'idempotency_key_required',
      'not_found',
      'not_pending',
      'internal_error',
    ]
    if (typed.includes(code as ApiErrorCode)) return code as ApiErrorCode
    return 'internal_error'
  }
  return 'network_error'
}

export function InventoryDashboard() {
  const [items, setItems] = useState<Item[]>([])
  // Per-item reserving state (double-submit guard).
  const [reservingId, setReservingId] = useState<string | null>(null)
  // Per-item typed error code (null = no error).
  const [itemErrors, setItemErrors] = useState<Record<string, ApiErrorCode | null>>({})

  const {
    reservations,
    loading: reservationsLoading,
    error: reservationsError,
    notifyEvent,
    refresh: refreshReservations,
  } = useReservations()

  // Called on connect/reconnect with the full REST snapshot — always reconciles to truth.
  const handleItems = useCallback((snapshot: Item[]) => {
    setItems(snapshot)
  }, [])

  // Called for each live delta event from the WebSocket hub.
  // We apply the delta optimistically to our local state so the UI updates immediately.
  // On the next reconnect (if the socket drops), the snapshot reconciles to real truth.
  const handleEvent = useCallback(
    (event: StockEvent) => {
      setItems((prev) =>
        prev.map((item) => {
          if (item.id !== event.itemId) return item
          const reserved = event.reserved ?? item.reserved
          const available = Math.max(item.totalStock - reserved, 0)
          return { ...item, reserved, available }
        }),
      )
      // Forward the event to useReservations so it can refetch the panel.
      notifyEvent(event)
    },
    [notifyEvent],
  )

  useWebSocket({ onItems: handleItems, onEvent: handleEvent })

  const handleReserve = useCallback(async (item: Item) => {
    if (reservingId !== null) return // double-submit guard (only one item at a time)

    setReservingId(item.id)
    // Clear any previous error for this item.
    setItemErrors((prev) => ({ ...prev, [item.id]: null }))

    try {
      await apiFetch('/reservations', {
        method: 'POST',
        body: JSON.stringify({ itemId: item.id, quantity: 1 }),
        idempotencyKey: generateIdempotencyKey(),
      })
      // Success: clear the error (already null) — reservation panel will refresh via WebSocket.
      setItemErrors((prev) => ({ ...prev, [item.id]: null }))
    } catch (err: unknown) {
      setItemErrors((prev) => ({ ...prev, [item.id]: toErrorCode(err) }))
    } finally {
      setReservingId(null)
    }
  }, [reservingId])

  return (
    <main style={{ padding: '24px', maxWidth: '900px', margin: '0 auto' }}>
      <h1 style={{ fontSize: '1.5rem', fontWeight: 700, marginBottom: '8px' }}>
        Live Inventory
      </h1>
      <p style={{ color: '#718096', marginBottom: '24px', fontSize: '0.9rem' }}>
        Stock updates in real time — no refresh needed.
      </p>

      {items.length === 0 ? (
        <p style={{ color: '#a0aec0' }}>Loading inventory…</p>
      ) : (
        <div
          style={{
            display: 'grid',
            gridTemplateColumns: 'repeat(auto-fill, minmax(260px, 1fr))',
            gap: '16px',
          }}
        >
          {items.map((item) => (
            <ItemCard
              key={item.id}
              item={item}
              onReserve={handleReserve}
              isReserving={reservingId === item.id}
              errorCode={itemErrors[item.id] ?? null}
            />
          ))}
        </div>
      )}

      {/* US5 — my reservations panel */}
      <ReservationPanel
        reservations={reservations}
        loading={reservationsLoading}
        panelError={reservationsError}
        items={items}
        onRefresh={refreshReservations}
      />
    </main>
  )
}
