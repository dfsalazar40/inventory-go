/**
 * InventoryDashboard — live inventory view.
 *
 * Connects to the backend WebSocket hub via useWebSocket. On every connect or
 * reconnect it fetches a fresh REST snapshot from GET /items, then applies live
 * delta events as they arrive — preventing permanent staleness after a dropped
 * channel (research §6, SC-005).
 *
 * Available count updates without any manual refresh.
 */

import { useState, useCallback } from 'react'
import { useWebSocket, type Item, type StockEvent } from '../hooks/useWebSocket'
import { ItemCard } from './ItemCard'

export function InventoryDashboard() {
  const [items, setItems] = useState<Item[]>([])
  const [reservingId, setReservingId] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  // Called on connect/reconnect with the full REST snapshot — always reconciles to truth.
  const handleItems = useCallback((snapshot: Item[]) => {
    setItems(snapshot)
  }, [])

  // Called for each live delta event from the WebSocket hub.
  // We apply the delta optimistically to our local state so the UI updates immediately.
  // On the next reconnect (if the socket drops), the snapshot reconciles to real truth.
  const handleEvent = useCallback((event: StockEvent) => {
    setItems((prev) =>
      prev.map((item) => {
        if (item.id !== event.itemId) return item
        // Re-compute available from the server-provided reserved count.
        const reserved = event.reserved ?? item.reserved
        const available = Math.max(item.totalStock - reserved, 0)
        return { ...item, reserved, available }
      }),
    )
  }, [])

  useWebSocket({ onItems: handleItems, onEvent: handleEvent })

  const handleReserve = useCallback(async (item: Item) => {
    setReservingId(item.id)
    setError(null)
    try {
      const { apiFetch } = await import('../api/client')
      const { generateIdempotencyKey } = await import('../lib/identity')
      await apiFetch('/reservations', {
        method: 'POST',
        body: JSON.stringify({ itemId: item.id, quantity: 1 }),
        idempotencyKey: generateIdempotencyKey(),
      })
    } catch (err: unknown) {
      const message =
        err instanceof Error ? err.message : 'Failed to reserve item. Please try again.'
      setError(message)
    } finally {
      setReservingId(null)
    }
  }, [])

  return (
    <main style={{ padding: '24px', maxWidth: '900px', margin: '0 auto' }}>
      <h1 style={{ fontSize: '1.5rem', fontWeight: 700, marginBottom: '8px' }}>
        Live Inventory
      </h1>
      <p style={{ color: '#718096', marginBottom: '24px', fontSize: '0.9rem' }}>
        Stock updates in real time — no refresh needed.
      </p>

      {error && (
        <div
          role="alert"
          style={{
            padding: '12px',
            marginBottom: '16px',
            borderRadius: '6px',
            background: '#fff5f5',
            border: '1px solid #fc8181',
            color: '#c53030',
          }}
        >
          {error}
        </div>
      )}

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
            />
          ))}
        </div>
      )}
    </main>
  )
}
