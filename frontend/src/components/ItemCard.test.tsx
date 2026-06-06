/**
 * T033 — Light render tests for ItemCard.
 * T046 [US7] — Error-state tests (test-first; must fail before typed-error implementation).
 *
 * Verifies:
 *   - Shows item name and stock figures.
 *   - Shows "Out of Stock" badge and disables reserve button when available === 0.
 *   - Shows "Reserve Item" button and calls onReserve when available > 0.
 *   - (T046) Reserve happy-path: success clears any error state and keeps UI usable.
 *   - (T046) Conflict error ("Item Taken") shows a distinct, user-readable message.
 *   - (T046) Insufficient-stock error shows a distinct, user-readable message.
 *   - (T046) Transient/network error shows a fallback non-blocking message.
 *   - (T046) UI stays usable after each error (button is re-enabled, not blocked/crashed).
 */

import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { ItemCard } from './ItemCard'
import type { Item } from '../hooks/useWebSocket'

const baseItem: Item = {
  id: 'item-1',
  name: 'Vintage Camera',
  totalStock: 10,
  reserved: 3,
  available: 7,
}

describe('ItemCard', () => {
  it('renders item name and the occupancy meter (reserved / total + available)', () => {
    render(<ItemCard item={baseItem} />)

    expect(screen.getByText('Vintage Camera')).toBeInTheDocument()
    // Occupancy meter: the bar tracks RESERVED out of total (it fills as items are
    // reserved), and the available count is shown alongside it.
    expect(screen.getByText('3 / 10 Reserved')).toBeInTheDocument()
    expect(screen.getByText('7 Available')).toBeInTheDocument()
    const meter = screen.getByRole('progressbar')
    expect(meter).toHaveAttribute('aria-valuenow', '3') // 3 of 10 reserved
    expect(meter).toHaveAttribute('aria-valuemax', '10')
  })

  it('shows Reserve Item button when available > 0', () => {
    render(<ItemCard item={baseItem} />)
    expect(screen.getByRole('button', { name: /reserve vintage camera/i })).toBeInTheDocument()
    expect(screen.queryByText('Out of Stock')).not.toBeInTheDocument()
  })

  it('calls onReserve when Reserve Item is clicked', async () => {
    const onReserve = vi.fn()
    render(<ItemCard item={baseItem} onReserve={onReserve} />)

    await userEvent.click(screen.getByRole('button', { name: /reserve/i }))
    expect(onReserve).toHaveBeenCalledWith(baseItem)
  })

  it('shows Out of Stock badge when available === 0', () => {
    const outOfStockItem: Item = { ...baseItem, reserved: 10, available: 0 }
    render(<ItemCard item={outOfStockItem} />)

    expect(screen.getByLabelText('Out of Stock')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /reserve/i })).not.toBeInTheDocument()
  })

  it('disables the button and shows Reserving… during reservation', () => {
    render(<ItemCard item={baseItem} isReserving={true} />)

    const btn = screen.getByRole('button')
    expect(btn).toBeDisabled()
    expect(btn).toHaveTextContent('Reserving…')
  })

  // ── T046 [US7] error-state tests ────────────────────────────────────────────

  it('(T046) shows no error message by default (happy path)', () => {
    render(<ItemCard item={baseItem} />)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('(T046) shows a conflict-specific message for the conflict error code', () => {
    render(<ItemCard item={baseItem} errorCode="conflict" />)

    const alert = screen.getByRole('alert')
    expect(alert).toBeInTheDocument()
    // Must be a DISTINCT, user-readable message — not a raw code or generic text
    expect(alert.textContent).toMatch(/item taken|reserved by another/i)
  })

  it('(T046) shows an insufficient-stock-specific message for insufficient_stock', () => {
    render(<ItemCard item={baseItem} errorCode="insufficient_stock" />)

    const alert = screen.getByRole('alert')
    expect(alert).toBeInTheDocument()
    expect(alert.textContent).toMatch(/not enough stock|insufficient/i)
    // Must be DISTINCT from the conflict message
    expect(alert.textContent).not.toMatch(/item taken|reserved by another/i)
  })

  it('(T046) shows a validation message for validation_error', () => {
    render(<ItemCard item={baseItem} errorCode="validation_error" />)
    const alert = screen.getByRole('alert')
    expect(alert.textContent).toMatch(/invalid|quantity/i)
  })

  it('(T046) shows a transient-error message for network/unknown errors', () => {
    render(<ItemCard item={baseItem} errorCode="network_error" />)
    const alert = screen.getByRole('alert')
    expect(alert).toBeInTheDocument()
    // Must be non-blocking (alert exists but button is still usable)
    const btn = screen.getByRole('button', { name: /reserve/i })
    expect(btn).not.toBeDisabled()
  })

  it('(T046) UI stays usable after a conflict error (button is re-enabled)', () => {
    render(<ItemCard item={baseItem} errorCode="conflict" isReserving={false} />)

    // The reserve button must still be present and enabled
    const btn = screen.getByRole('button', { name: /reserve/i })
    expect(btn).not.toBeDisabled()
  })

  it('(T046) UI stays usable after an insufficient-stock error (button is re-enabled)', () => {
    render(<ItemCard item={baseItem} errorCode="insufficient_stock" isReserving={false} />)

    const btn = screen.getByRole('button', { name: /reserve/i })
    expect(btn).not.toBeDisabled()
  })
})
