/**
 * ItemCard — displays a single inventory item with a live stock meter.
 *
 * Shows "Out of Stock" when available === 0 (US2 acceptance scenario 2).
 * The available value is always provided from the parent; this component is
 * pure/presentational so it renders predictably in tests.
 *
 * T047 [US7] Error feedback:
 *   - Accepts an `errorCode` prop (typed API error code string).
 *   - Maps each code to a DISTINCT, user-readable message (FR-013, SC-007).
 *   - Shows a non-blocking alert; the Reserve button stays enabled after an error.
 */

import type { Item } from '../hooks/useWebSocket'
import { ERROR_MESSAGES, type ApiErrorCode } from '../lib/errors'

interface ItemCardProps {
  item: Item
  onReserve?: (item: Item) => void
  isReserving?: boolean
  /** Typed API error code to display as a non-blocking inline alert (null = no error). */
  errorCode?: ApiErrorCode | null
}

export function ItemCard({ item, onReserve, isReserving = false, errorCode }: ItemCardProps) {
  const outOfStock = item.available === 0
  // Occupancy meter: the bar FILLS as units get reserved (reserved / total),
  // so reserving an item visibly advances the meter toward "fully reserved"
  // instead of draining it. Fully reserved (available === 0) === Out of Stock.
  const reservedPercent =
    item.totalStock > 0 ? Math.round((item.reserved / item.totalStock) * 100) : 0
  const initial = item.name.charAt(0).toUpperCase()
  const errorMessage = errorCode
    ? (ERROR_MESSAGES[errorCode] ?? 'Something went wrong. Please try again.')
    : null

  return (
    <article
      aria-label={item.name}
      className="flex flex-col gap-4 rounded-xl border border-slate-100 bg-white p-5 shadow-sm"
    >
      {/* Header: avatar + name */}
      <div className="flex items-center gap-3">
        <div
          aria-hidden="true"
          className="flex h-12 w-12 shrink-0 items-center justify-center rounded-full bg-brand text-lg font-bold text-white"
        >
          {initial}
        </div>
        <h3 className="text-lg leading-tight font-bold text-slate-800">{item.name}</h3>
      </div>

      {/* Stock meter */}
      <div className="flex flex-col gap-1.5">
        <span className="text-xs font-medium tracking-wide text-slate-500">Total Stock Meter</span>
        <div
          className="h-2 w-full overflow-hidden rounded-full bg-slate-200"
          role="progressbar"
          aria-valuenow={item.reserved}
          aria-valuemin={0}
          aria-valuemax={item.totalStock}
        >
          <div
            className={`h-full rounded-full transition-[width] duration-500 ${
              outOfStock ? 'bg-red-500' : 'bg-brand'
            }`}
            style={{ width: `${reservedPercent}%` }}
          />
        </div>
        <div className="flex items-center justify-between text-sm">
          <span className="font-medium text-slate-600">
            {item.reserved} / {item.totalStock} Reserved
          </span>
          {outOfStock ? (
            <span className="font-semibold text-red-600">Out of Stock</span>
          ) : (
            <span className="font-semibold text-slate-500">{item.available} Available</span>
          )}
        </div>
      </div>

      {/* T047 [US7]: typed error feedback — non-blocking, distinct per error code */}
      {errorMessage && (
        <div
          role="alert"
          className="rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700"
        >
          {errorMessage}
        </div>
      )}

      {/* Reserve action / out-of-stock state. When there is no stock the button is
          disabled (frontend guard); the backend also rejects the reserve (atomic
          conditional UPDATE → insufficient_stock), so over-selling is impossible. */}
      {outOfStock ? (
        <button
          type="button"
          disabled
          aria-label="Out of Stock"
          className="cursor-not-allowed rounded-lg bg-slate-100 py-2.5 text-center text-sm font-semibold text-slate-400"
        >
          Out of Stock
        </button>
      ) : (
        <button
          type="button"
          disabled={isReserving}
          onClick={() => onReserve?.(item)}
          aria-label={`Reserve ${item.name}`}
          className="rounded-lg bg-brand py-2.5 text-center text-sm font-semibold text-white transition-colors hover:bg-brand-dark disabled:cursor-not-allowed disabled:opacity-60"
        >
          {isReserving ? 'Reserving…' : 'Reserve Item'}
        </button>
      )}
    </article>
  )
}
