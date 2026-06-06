/**
 * ReservationPanel — per-user active reservations view (US5, US8, FR-012).
 *
 * Shows:
 *   - Each PENDING reservation: item name, units, live countdown, Confirm + Release.
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
import { apiFetch } from '../api/client'
import type { Reservation } from '../hooks/useReservations'
import type { Item } from '../hooks/useWebSocket'
import { useCountdown } from '../hooks/useCountdown'
import type { ToastData } from './Toast'
import { errorToToast } from '../lib/errors'

// Confirm/Release failures surface through the shared top-center toast (passed
// down as onError) via the centralized errorToToast mapper — so error feedback
// looks identical to reserve errors and matches the design mock (no inline
// boxes inside the reservation card).

/** Short, human-friendly reference derived from the reservation UUID (e.g. RES-1A2B). */
function shortRef(id: string): string {
  return `RES-${id.replace(/-/g, '').slice(0, 4).toUpperCase()}`
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

  // Color-coded urgency: red in the final 10s, amber otherwise.
  const color = seconds <= 10 ? 'text-red-600' : 'text-amber-500'

  return (
    <span
      aria-label="time remaining"
      className={`text-3xl font-bold tabular-nums ${color}`}
    >
      {mins}:{secs}
    </span>
  )
}

// ── Reservation line ────────────────────────────────────────────────────────

interface ReservationLineProps {
  reservation: Reservation
  itemName: string
  onConfirm: (id: string) => Promise<void>
  onRelease: (id: string) => Promise<void>
  isActing: boolean
}

function ReservationLine({
  reservation,
  itemName,
  onConfirm,
  onRelease,
  isActing,
}: ReservationLineProps) {
  const isPending = reservation.status === 'pending'

  return (
    <li className="flex flex-col gap-3 rounded-xl bg-white p-4 shadow-sm">
      {/* Header: item name + reference badge */}
      <div className="flex items-start justify-between gap-2">
        <strong className="text-base font-bold text-slate-800">{itemName}</strong>
        <span className="rounded-md bg-slate-100 px-2 py-0.5 text-xs font-medium text-slate-500">
          #{shortRef(reservation.id)}
        </span>
      </div>

      {/* Countdown (pending) or confirmed badge */}
      {isPending ? (
        <div className="flex flex-col">
          <span className="text-xs font-medium tracking-wide text-slate-500">Countdown</span>
          <CountdownDisplay expiresAt={reservation.expiresAt} />
        </div>
      ) : (
        <span className="w-fit rounded-md bg-emerald-50 px-2 py-1 text-xs font-semibold tracking-wide text-emerald-700 uppercase">
          Confirmed
        </span>
      )}

      <span className="text-sm text-slate-600">
        {reservation.quantity} {reservation.quantity === 1 ? 'Unit' : 'Units'} Held
      </span>

      {/* Action buttons only while PENDING: Confirm above, Release below (two-phase
          model). Once CONFIRMED, the units are locked in — no Release button is
          shown and the line just reflects the confirmed state. */}
      {isPending && (
        <div className="flex flex-col gap-2">
          <button
            type="button"
            disabled={isActing}
            onClick={() => onConfirm(reservation.id)}
            aria-label={`Confirm reservation for ${itemName}`}
            className="rounded-lg bg-brand py-2 text-sm font-semibold text-white transition-colors hover:bg-brand-dark disabled:cursor-not-allowed disabled:opacity-60"
          >
            {isActing ? 'Confirming…' : 'Confirm'}
          </button>

          {/* Release is never disabled based on the countdown (FR-007, US4). */}
          <button
            type="button"
            disabled={isActing}
            onClick={() => onRelease(reservation.id)}
            aria-label={`Release reservation for ${itemName}`}
            className="rounded-lg border border-slate-200 bg-white py-2 text-sm font-semibold text-slate-600 transition-colors hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-60"
          >
            {isActing ? 'Releasing…' : 'Release'}
          </button>
        </div>
      )}
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
  /** Surfaces confirm/release errors through the shared top-center toast. */
  onError: (toast: ToastData) => void
}

export function ReservationPanel({
  reservations,
  loading,
  panelError,
  items,
  onRefresh,
  onError,
}: ReservationPanelProps) {
  // Per-reservation in-flight tracking for double-submit guard.
  const [actingIds, setActingIds] = useState<Set<string>>(new Set())

  const setActing = (id: string, v: boolean) =>
    setActingIds((prev) => {
      const next = new Set(prev)
      if (v) next.add(id)
      else next.delete(id)
      return next
    })

  const itemNameMap = Object.fromEntries(items.map((i) => [i.id, i.name]))

  // Resolve the display name for the item behind a given reservation id.
  const resolveItemName = useCallback(
    (id: string) => {
      const itemId = reservations.find((r) => r.id === id)?.itemId
      return (itemId && itemNameMap[itemId]) || 'item'
    },
    [reservations, itemNameMap],
  )

  const handleConfirm = useCallback(
    async (id: string) => {
      if (actingIds.has(id)) return // double-submit guard
      setActing(id, true)
      try {
        await apiFetch(`/reservations/${id}/confirm`, { method: 'POST' })
        onRefresh()
      } catch (err) {
        onError(errorToToast(err, { itemName: resolveItemName(id) }))
      } finally {
        setActing(id, false)
      }
    },
    [actingIds, onRefresh, onError, resolveItemName],
  )

  const handleRelease = useCallback(
    async (id: string) => {
      if (actingIds.has(id)) return // double-submit guard
      setActing(id, true)
      try {
        await apiFetch(`/reservations/${id}`, { method: 'DELETE' })
        onRefresh()
      } catch (err) {
        onError(errorToToast(err, { itemName: resolveItemName(id) }))
      } finally {
        setActing(id, false)
      }
    },
    [actingIds, onRefresh, onError, resolveItemName],
  )

  return (
    <section aria-label="Your Reservations" className="flex flex-col gap-4">
      <h2 className="text-xl font-bold text-slate-800">Your Reservations</h2>

      {panelError && (
        <div
          role="alert"
          className="rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700"
        >
          {panelError}
        </div>
      )}

      {loading ? (
        <p className="text-sm text-slate-400">Loading reservations…</p>
      ) : reservations.length === 0 ? (
        <p className="text-sm text-slate-400">
          You have no active reservations. Reserve an item to get started.
        </p>
      ) : (
        <ul className="flex list-none flex-col gap-3 p-0">
          {reservations.map((r) => (
            <ReservationLine
              key={r.id}
              reservation={r}
              itemName={itemNameMap[r.itemId] ?? r.itemId}
              onConfirm={handleConfirm}
              onRelease={handleRelease}
              isActing={actingIds.has(r.id)}
            />
          ))}
        </ul>
      )}
    </section>
  )
}
