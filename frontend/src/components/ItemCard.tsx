/**
 * ItemCard — displays a single inventory item with live available count.
 *
 * Shows "Out of Stock" when available === 0 (US2 acceptance scenario 2).
 * The available value is always provided from the parent; this component is
 * pure/presentational so it renders predictably in tests.
 */

import type { Item } from '../hooks/useWebSocket'

interface ItemCardProps {
  item: Item
  onReserve?: (item: Item) => void
  isReserving?: boolean
}

export function ItemCard({ item, onReserve, isReserving = false }: ItemCardProps) {
  const outOfStock = item.available === 0

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
