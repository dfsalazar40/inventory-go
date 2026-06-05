import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { InventoryDashboard } from './components/InventoryDashboard'

const root = document.getElementById('root')
if (!root) throw new Error('Root element not found')

createRoot(root).render(
  <StrictMode>
    <InventoryDashboard />
  </StrictMode>,
)
