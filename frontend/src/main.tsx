import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'

const root = document.getElementById('root')
if (!root) throw new Error('Root element not found')

createRoot(root).render(
  <StrictMode>
    <div>
      <h1>Inventory Reservation System</h1>
      <p>Coming soon — components are being built.</p>
    </div>
  </StrictMode>,
)
