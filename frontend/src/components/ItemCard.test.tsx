/**
 * T033 — Light render tests for ItemCard.
 *
 * Verifies:
 *   - Shows item name and stock figures.
 *   - Shows "Out of Stock" badge and disables reserve button when available === 0.
 *   - Shows "Reserve Item" button and calls onReserve when available > 0.
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
  it('renders item name and stock figures', () => {
    render(<ItemCard item={baseItem} />)

    expect(screen.getByText('Vintage Camera')).toBeInTheDocument()
    expect(screen.getByText('10')).toBeInTheDocument() // total
    expect(screen.getByText('3')).toBeInTheDocument()  // reserved
    expect(screen.getByText('7')).toBeInTheDocument()  // available
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
})
