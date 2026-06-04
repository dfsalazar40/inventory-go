/**
 * ReservationPanel — per-user active reservations view (US5, US8, FR-012).
 *
 * Shows:
 *   - Each PENDING reservation: item name, units, live countdown, Confirm (above)
 *     + Release (below) buttons — the two-phase model layout.
 *   - Each CONFIRMED reservation: item name, units, "Confirmed" badge, Release button.
 *     Confirmed reservations have no countdown (expiresAt is null; they never expire).
 *
 * Design notes:
 *   - Release MUST work even when the countdown shows 00:00 (clock skew).
 *     The Release button is NEVER disabled based on the local timer (spec US4, FR-007).
 *   - Double-submit guard: each action button is disabled while its specific async
 *     request is in flight (per-reservation-id set of in-flight ids).
 *   - Live update: the parent passes notifyEvent from useWebSocket; any event triggers
 *     a refetch so the panel reconciles to backend truth (FR-011).
 *   - Item name: looked up from the items list passed in. Falls back to itemId if unknown.
 */

import { useCallback, useState } from 'react'
import { apiFetch, ApiRequestError } from '../api/client'
import type { Reservation } from '../hooks/useReservations'
import type { Item } from '../hooks/useWebSocket'
import { useCountdown } from '../hooks/useCountdown'

// ── Typed error → user-readable messages (US7, FR-013, SC-007) ───────────────

function reservationErrorMessage(err: unknown): string {
  if (err instanceof ApiRequestError) {
    switch (err.body.error) {
      case 'conflict':
        return 'Item Taken — reserved by another user. Try another item.'
      case 'insufficient_stock':
        return 'Not enough stock available for your request.'
      case 'not_pending':
        return 'This reservation is no longer pending and cannot be confirmed.'
      case 'not_found':
        return 'Reservation not found. It may have expired or been released already.'
      case 'validation_error':
        return 'Invalid request. Please check your input and try again.'
      case 'idempotency_key_conflict':
        return 'A conflicting request was already made. Please refresh and try again.'
      default:
        return err.body.message || 'Something went wrong. Please try again.'
    }
  }
  return 'Network error. Please check your connection and try again.'
}

// ── Sub-components ────────────────────────────────────────────────────────────

interface CountdownDisplayProps {
  expiresAt: string | null
}

function CountdownDisplay({ expiresAt }: CountdownDisplayProps) {
  const seconds = useCountdown(expiresAt)

  if (seconds === null) return null

  const mins = Math.floor(seconds / 60)
    .toString()
    .padStart(2, '0')
  const secs = (seconds % 60).toString().padStart(2, '0')

  return (
    <span
      aria-label="time remaining"
      style={{
        fontVariantNumeric: 'tabular-nums',
        color: seconds <= 10 ? '#e53e3e' : '#d69e2e',
        fontWeight: 600,
        fontSize: '0.9rem',
      }}
    >
      {mins}:{secs}
    </span>
  )
}

// ── Main component ────────────────────────────────────────────────────────────

interface ReservationLineProps {
  reservation: Reservation
  itemName: string
  onConfirm: (id: string) => Promise<void>
  onRelease: (id: string) => Promise<void>
  actionError: string | null
  isActing: boolean
}

function ReservationLine({
  reservation,
  itemName,
  onConfirm,
  onRelease,
  actionError,
  isActing,
}: ReservationLineProps) {
  const isPending = reservation.status === 'pending'

  return (
    <li
      style={{
        border: '1px solid #e2e8f0',
        borderRadius: '8px',
        padding: '14px 16px',
        display: 'flex',
        flexDirection: 'column',
        gap: '8px',
        background: isPending ? '#fffff0' : '#f0fff4',
      }}
    >
      {/* Header: item name + status badge */}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <strong style={{ fontSize: '0.95rem' }}>{itemName}</strong>
        <span
          style={{
            fontSize: '0.75rem',
            fontWeight: 600,
            padding: '2px 8px',
            borderRadius: '4px',
            background: isPending ? '#fefcbf' : '#c6f6d5',
            color: isPending ? '#744210' : '#276749',
            textTransform: 'uppercase',
          }}
        >
          {reservation.status}
        </span>
      </div>

      {/* Units + countdown */}
      <div style={{ display: 'flex', gap: '16px', fontSize: '0.85rem', color: '#4a5568' }}>
        <span>
          <strong>{reservation.quantity}</strong>{' '}
          {reservation.quantity === 1 ? 'unit' : 'units'} held
        </span>
        {isPending && (
          <span style={{ display: 'flex', alignItems: 'center', gap: '4px' }}>
            Expires in: <CountdownDisplay expiresAt={reservation.expiresAt} />
          </span>
        )}
      </div>

      {/* Per-line error */}
      {actionError && (
        <div
          role="alert"
          style={{
            padding: '8px 10px',
            borderRadius: '4px',
            background: '#fff5f5',
            border: '1px solid #fc8181',
            color: '#c53030',
            fontSize: '0.8rem',
          }}
        >
          {actionError}
        </div>
      )}

      {/* Action buttons: Confirm above, Release below (spec two-phase model) */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: '6px' }}>
        {isPending && (
          <button
            type="button"
            disabled={isActing}
            onClick={() => onConfirm(reservation.id)}
            aria-label={`Confirm reservation for ${itemName}`}
            style={{
              padding: '6px 12px',
              borderRadius: '4px',
              border: 'none',
              background: isActing ? '#9ae6b4' : '#38a169',
              color: '#fff',
              fontWeight: 600,
              cursor: isActing ? 'not-allowed' : 'pointer',
              opacity: isActing ? 0.7 : 1,
              fontSize: '0.85rem',
            }}
          >
            {isActing ? 'Confirming…' : 'Confirm'}
          </button>
        )}

        {/* Release is ALWAYS enabled — never disabled based on countdown (FR-007, US4) */}
        <button
          type="button"
          disabled={isActing}
          onClick={() => onRelease(reservation.id)}
          aria-label={`Release reservation for ${itemName}`}
          style={{
            padding: '6px 12px',
            borderRadius: '4px',
            border: '1px solid #fc8181',
            background: '#fff',
            color: '#c53030',
            fontWeight: 600,
            cursor: isActing ? 'not-allowed' : 'pointer',
            opacity: isActing ? 0.7 : 1,
            fontSize: '0.85rem',
          }}
        >
          {isActing && !isPending ? 'Releasing…' : 'Release'}
        </button>
      </div>
    </li>
  )
}

// ── Panel ─────────────────────────────────────────────────────────────────────

interface ReservationPanelProps {
  reservations: Reservation[]
  loading: boolean
  panelError: string | null
  /** Items list — used to resolve item names. */
  items: Item[]
  /** Called after any confirm/release so the parent can refresh. */
  onRefresh: () => void
}

export function ReservationPanel({
  reservations,
  loading,
  panelError,
  items,
  onRefresh,
}: ReservationPanelProps) {
  // Per-reservation in-flight tracking for double-submit guard.
  const [actingIds, setActingIds] = useState<Set<string>>(new Set())
  // Per-reservation action errors.
  const [lineErrors, setLineErrors] = useState<Record<string, string>>({})

  const setActing = (id: string, v: boolean) =>
    setActingIds((prev) => {
      const next = new Set(prev)
      if (v) next.add(id)
      else next.delete(id)
      return next
    })

  const setLineError = (id: string, msg: string | null) =>
    setLineErrors((prev) => {
      if (msg === null) {
        const { [id]: _, ...rest } = prev
        return rest
      }
      return { ...prev, [id]: msg }
    })

  const handleConfirm = useCallback(
    async (id: string) => {
      if (actingIds.has(id)) return // double-submit guard
      setActing(id, true)
      setLineError(id, null)
      try {
        await apiFetch(`/reservations/${id}/confirm`, { method: 'POST' })
        onRefresh()
      } catch (err) {
        setLineError(id, reservationErrorMessage(err))
      } finally {
        setActing(id, false)
      }
    },
    [actingIds, onRefresh],
  )

  const handleRelease = useCallback(
    async (id: string) => {
      if (actingIds.has(id)) return // double-submit guard
      setActing(id, true)
      setLineError(id, null)
      try {
        await apiFetch(`/reservations/${id}`, { method: 'DELETE' })
        onRefresh()
      } catch (err) {
        setLineError(id, reservationErrorMessage(err))
      } finally {
        setActing(id, false)
      }
    },
    [actingIds, onRefresh],
  )

  const itemNameMap = Object.fromEntries(items.map((i) => [i.id, i.name]))

  return (
    <section aria-label="My Reservations" style={{ marginTop: '32px' }}>
      <h2 style={{ fontSize: '1.25rem', fontWeight: 700, marginBottom: '12px' }}>
        My Reservations
      </h2>

      {panelError && (
        <div
          role="alert"
          style={{
            padding: '10px 12px',
            marginBottom: '12px',
            borderRadius: '6px',
            background: '#fff5f5',
            border: '1px solid #fc8181',
            color: '#c53030',
            fontSize: '0.85rem',
          }}
        >
          {panelError}
        </div>
      )}

      {loading ? (
        <p style={{ color: '#a0aec0', fontSize: '0.9rem' }}>Loading reservations…</p>
      ) : reservations.length === 0 ? (
        <p style={{ color: '#a0aec0', fontSize: '0.9rem' }}>
          You have no active reservations. Reserve an item above to get started.
        </p>
      ) : (
        <ul style={{ listStyle: 'none', padding: 0, margin: 0, display: 'flex', flexDirection: 'column', gap: '10px' }}>
          {reservations.map((r) => (
            <ReservationLine
              key={r.id}
              reservation={r}
              itemName={itemNameMap[r.itemId] ?? r.itemId}
              onConfirm={handleConfirm}
              onRelease={handleRelease}
              actionError={lineErrors[r.id] ?? null}
              isActing={actingIds.has(r.id)}
            />
          ))}
        </ul>
      )}
    </section>
  )
}
