/**
 * Toast — transient, top-center global notification (US7, FR-013, SC-007).
 *
 * Single presentational component shared by every error surface: reserve errors
 * from the inventory grid and confirm/release errors from the reservation panel
 * both render through here, so error feedback looks identical everywhere and
 * always appears centered at the top of the viewport (matches design mock).
 */

export interface ToastData {
  title: string
  message: string
}

interface ToastProps {
  toast: ToastData
}

export function Toast({ toast }: ToastProps) {
  return (
    <div className="pointer-events-none fixed inset-x-0 top-6 z-50 flex justify-center px-4">
      <div
        role="alert"
        className="pointer-events-auto flex w-full max-w-sm items-start gap-3 rounded-lg bg-red-500 px-4 py-3 text-white shadow-lg"
      >
        <svg
          aria-hidden="true"
          viewBox="0 0 20 20"
          fill="currentColor"
          className="mt-0.5 h-5 w-5 shrink-0 text-white/90"
        >
          <path
            fillRule="evenodd"
            d="M10 18a8 8 0 100-16 8 8 0 000 16zM9 9a1 1 0 012 0v4a1 1 0 11-2 0V9zm1-4a1 1 0 100 2 1 1 0 000-2z"
            clipRule="evenodd"
          />
        </svg>
        <div className="text-sm leading-snug">
          <p className="font-bold">{toast.title}</p>
          <p className="text-white/90">{toast.message}</p>
        </div>
      </div>
    </div>
  )
}
