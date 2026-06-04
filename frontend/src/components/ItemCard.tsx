/**
 * ItemCard — displays a single inventory item with live available count.
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

/** Typed API error codes from the OpenAPI Error schema. */
export type ApiErrorCode =
  | 'conflict'
  | 'insufficient_stock'
  | 'validation_error'
  | 'idempotency_key_conflict'
  | 'idempotency_key_required'
  | 'not_found'
  | 'not_pending'
  | 'network_error'
  | 'internal_error'

/** Maps each typed error code to a distinct, user-readable message (SC-007). */
const ERROR_MESSAGES: Record<ApiErrorCode, string> = {
  conflict: 'Item Taken — reserved by another user. Try again shortly.',
  insufficient_stock: 'Not enough stock available. Another user may have taken the last unit.',
  validation_error: 'Invalid request. Please check your quantity and try again.',
  idempotency_key_conflict:
    'A conflicting request was already made. Please refresh and try again.',
  idempotency_key_required: 'Request error. Please refresh the page and try again.',
  not_found: 'Item not found. Please refresh the page.',
  not_pending: 'This action is no longer valid. Please refresh the page.',
  network_error: 'Network error. Please check your connection and try again.',
  internal_error: 'Something went wrong on our end. Please try again in a moment.',
}

interface ItemCardProps {
  item: Item
  onReserve?: (item: Item) => void
  isReserving?: boolean
  /** Typed API error code to display as a non-blocking inline alert (null = no error). */
  errorCode?: ApiErrorCode | null
}

export function ItemCard({ item, onReserve, isReserving = false, errorCode }: ItemCardProps) {
  const outOfStock = item.available === 0
  const errorMessage = errorCode ? (ERROR_MESSAGES[errorCode] ?? 'Something went wrong. Please try again.') : null

  return (
    <article
      aria-label={item.name}
      style={{
        border: '1px solid #e2e8f0',
        borderRadius: '8px',
        padding: '16px',
        display: 'flex',
        flexDirection: 'column',
        gap: '8px',
        background: outOfStock ? '#f7f7f7' : '#ffffff',
      }}
    >
      <h3 style={{ margin: 0, fontSize: '1rem', fontWeight: 600 }}>{item.name}</h3>

      <dl style={{ margin: 0, display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: '4px' }}>
        <div>
          <dt style={{ fontSize: '0.75rem', color: '#718096' }}>Total</dt>
          <dd style={{ margin: 0, fontWeight: 500 }}>{item.totalStock}</dd>
        </div>
        <div>
          <dt style={{ fontSize: '0.75rem', color: '#718096' }}>Reserved</dt>
          <dd style={{ margin: 0, fontWeight: 500 }}>{item.reserved}</dd>
        </div>
        <div>
          <dt style={{ fontSize: '0.75rem', color: outOfStock ? '#e53e3e' : '#38a169' }}>
            Available
          </dt>
          <dd
            style={{
              margin: 0,
              fontWeight: 700,
              color: outOfStock ? '#e53e3e' : '#38a169',
            }}
          >
            {item.available}
          </dd>
        </div>
      </dl>

      {/* T047 [US7]: typed error feedback — non-blocking, distinct per error code */}
      {errorMessage && (
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
          {errorMessage}
        </div>
      )}

      {outOfStock ? (
        <span
          aria-label="Out of Stock"
          style={{
            display: 'inline-block',
            padding: '4px 10px',
            borderRadius: '4px',
            background: '#fed7d7',
            color: '#9b2c2c',
            fontSize: '0.8rem',
            fontWeight: 600,
            textAlign: 'center',
          }}
        >
          Out of Stock
        </span>
      ) : (
        <button
          type="button"
          disabled={isReserving}
          onClick={() => onReserve?.(item)}
          aria-label={`Reserve ${item.name}`}
          style={{
            padding: '6px 14px',
            borderRadius: '4px',
            border: 'none',
            background: '#3182ce',
            color: '#fff',
            fontWeight: 600,
            cursor: isReserving ? 'not-allowed' : 'pointer',
            opacity: isReserving ? 0.6 : 1,
          }}
        >
          {isReserving ? 'Reserving…' : 'Reserve Item'}
        </button>
      )}
    </article>
  )
}
