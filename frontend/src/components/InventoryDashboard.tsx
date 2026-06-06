/**
 * InventoryDashboard — live inventory view + the user's active reservations panel.
 *
 * Connects to the backend WebSocket hub via useWebSocket. On every connect or
 * reconnect it fetches a fresh REST snapshot from GET /items, then applies live
 * delta events as they arrive — preventing permanent staleness after a dropped
 * channel (research §6, SC-005).
 *
 * T047 [US7]: reserve errors are mapped to typed ApiErrorCode values and surfaced
 * as a transient global toast with a distinct, user-readable message (FR-013,
 * SC-007). A double-submit guard prevents concurrent reserve requests on the same item.
 *
 * US5: the ReservationPanel renders in the right sidebar and is kept in sync by
 * the same WebSocket event stream (notifyEvent → refetch).
 */

import { useState, useCallback, useEffect } from 'react'
import { useWebSocket, type Item, type StockEvent } from '../hooks/useWebSocket'
import { useReservations } from '../hooks/useReservations'
import { ItemCard } from './ItemCard'
import { ReservationPanel } from './ReservationPanel'
import { Toast, type ToastData } from './Toast'
import { apiFetch } from '../api/client'
import { errorToToast } from '../lib/errors'
import { generateIdempotencyKey } from '../lib/identity'

export function InventoryDashboard() {
  const [items, setItems] = useState<Item[]>([])
  // Per-item reserving state (double-submit guard).
  const [reservingId, setReservingId] = useState<string | null>(null)
  // Transient global error notification (top-center toast).
  const [toast, setToast] = useState<ToastData | null>(null)

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

  // Called for each live event from the WebSocket hub. The backend broadcasts
  // signal-only events (reserve/confirm/release carry no post-mutation counts),
  // so we reconcile to backend truth by refetching the /items snapshot rather
  // than trusting per-event deltas. This keeps the stock meters accurate for
  // every event type (reserve, confirm, release, expire, reset).
  const handleEvent = useCallback(
    (event: StockEvent) => {
      apiFetch<Item[]>('/items')
        .then(setItems)
        .catch(() => {
          // Non-fatal; the next event or reconnect snapshot will reconcile.
        })
      // Forward the event to useReservations so it can refetch the panel.
      notifyEvent(event)
    },
    [notifyEvent],
  )

  const connected = useWebSocket({ onItems: handleItems, onEvent: handleEvent })

  // Auto-dismiss the toast after a few seconds.
  useEffect(() => {
    if (!toast) return
    const timer = setTimeout(() => setToast(null), 5000)
    return () => clearTimeout(timer)
  }, [toast])

  // Shared toast trigger — passed down so the reservation panel surfaces its
  // confirm/release errors through the same top-center toast instead of inline.
  const showToast = useCallback((next: ToastData) => setToast(next), [])

  // Reset button — restores the demo to its initial seeded state: clears all
  // reservations and resets the catalog on the backend (POST /reset), then
  // reconciles this client's inventory + reservations. Other connected clients
  // reconcile via the reset events the backend broadcasts.
  const handleReset = useCallback(async () => {
    try {
      const fresh = await apiFetch<Item[]>('/reset', { method: 'POST' })
      setItems(fresh)
    } catch {
      showToast({
        title: 'Reset Failed',
        message: 'Could not reset the inventory. Please try again.',
      })
    }
    refreshReservations()
  }, [refreshReservations, showToast])

  const handleReserve = useCallback(
    async (item: Item) => {
      if (reservingId !== null) return // double-submit guard (only one item at a time)

      setReservingId(item.id)
      try {
        await apiFetch('/reservations', {
          method: 'POST',
          body: JSON.stringify({ itemId: item.id, quantity: 1 }),
          idempotencyKey: generateIdempotencyKey(),
        })
        // Success — the reservation panel and stock refresh via the WebSocket event.
      } catch (err: unknown) {
        showToast(errorToToast(err, { itemName: item.name }))
      } finally {
        setReservingId(null)
      }
    },
    [reservingId, showToast],
  )

  return (
    <div className="min-h-full px-4 py-8">
      {/* Transient global toast (top-center) */}
      {toast && <Toast toast={toast} />}

      <div className="mx-auto max-w-6xl overflow-hidden rounded-2xl bg-white shadow-xl">
        {/* Header bar */}
        <header className="flex items-center justify-between bg-brand px-8 py-5">
          <h1 className="font-display text-3xl font-bold text-white">Atomic Inventory</h1>
          <div className="flex items-center gap-3">
            <span className="flex items-center gap-2 rounded-full bg-white/10 px-3 py-1.5 text-sm font-medium text-white">
              Status:
              <span
                aria-hidden="true"
                className={`h-2 w-2 rounded-full ${connected ? 'bg-emerald-400' : 'bg-slate-400'}`}
              />
              {connected ? 'Live' : 'Offline'}
            </span>
            <button
              type="button"
              onClick={handleReset}
              aria-label="Reset to initial state"
              className="flex items-center gap-2 rounded-lg border border-white/30 px-3 py-1.5 text-sm font-medium text-white transition-colors hover:bg-white/10"
            >
              <span aria-hidden="true">⟳</span> Reset
            </button>
          </div>
        </header>

        {/* Body: inventory grid + reservations sidebar */}
        <div className="grid grid-cols-1 lg:grid-cols-[1fr_320px]">
          {/* Available inventory */}
          <main className="p-8">
            <h2 className="mb-5 text-xl font-bold text-slate-800">Available Inventory</h2>
            {items.length === 0 ? (
              <p className="text-slate-400">Loading inventory…</p>
            ) : (
              <div className="grid grid-cols-1 gap-5 sm:grid-cols-2 xl:grid-cols-3">
                {items.map((item) => (
                  <ItemCard
                    key={item.id}
                    item={item}
                    onReserve={handleReserve}
                    isReserving={reservingId === item.id}
                  />
                ))}
              </div>
            )}
          </main>

          {/* Your reservations sidebar */}
          <aside className="bg-slate-100 p-6">
            <ReservationPanel
              reservations={reservations}
              loading={reservationsLoading}
              panelError={reservationsError}
              items={items}
              onRefresh={refreshReservations}
              onError={showToast}
            />
          </aside>
        </div>
      </div>
    </div>
  )
}
