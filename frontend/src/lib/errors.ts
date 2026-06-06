/**
 * Centralized error handling (US7, FR-013, SC-007).
 *
 * Single source of truth that maps any thrown error — typed API errors or raw
 * network failures — to a stable ApiErrorCode and to user-readable copy. Every
 * surface (reserve from the grid, confirm/release from the panel, inline card
 * alerts) routes through here so error messaging stays consistent and is defined
 * in exactly one place.
 */

import { ApiRequestError } from '../api/client'
import type { ToastData } from '../components/Toast'

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

/** The codes that originate from a typed API Error body (everything else → fallback). */
const TYPED_CODES: ApiErrorCode[] = [
  'conflict',
  'insufficient_stock',
  'validation_error',
  'idempotency_key_conflict',
  'idempotency_key_required',
  'not_found',
  'not_pending',
  'internal_error',
]

/** Maps each typed error code to a distinct, user-readable message (SC-007). */
export const ERROR_MESSAGES: Record<ApiErrorCode, string> = {
  conflict: 'Item Taken — reserved by another user. Try again shortly.',
  insufficient_stock: 'Not enough stock available. Another user may have taken the last unit.',
  validation_error: 'Invalid request. Please check your quantity and try again.',
  idempotency_key_conflict:
    'A conflicting request was already made. Please refresh and try again.',
  idempotency_key_required: 'Request error. Please refresh the page and try again.',
  not_found: 'Item not found. It may have expired or been released already.',
  not_pending: 'This action is no longer valid. Please refresh the page.',
  network_error: 'Network error. Please check your connection and try again.',
  internal_error: 'Something went wrong on our end. Please try again in a moment.',
}

/** Short, human-readable toast title per code. */
const ERROR_TITLES: Record<ApiErrorCode, string> = {
  conflict: 'Item Taken',
  insufficient_stock: 'Out of Stock',
  validation_error: 'Invalid Request',
  idempotency_key_conflict: 'Conflicting Request',
  idempotency_key_required: 'Request Error',
  not_found: 'Not Found',
  not_pending: 'No Longer Pending',
  network_error: 'Network Error',
  internal_error: 'Something Went Wrong',
}

/** Extract a typed ApiErrorCode from an unknown thrown error. */
export function toErrorCode(err: unknown): ApiErrorCode {
  if (err instanceof ApiRequestError) {
    const code = err.body.error
    return TYPED_CODES.includes(code as ApiErrorCode) ? (code as ApiErrorCode) : 'internal_error'
  }
  return 'network_error'
}

/**
 * Build a {title, message} toast for any thrown error. When an itemName is given,
 * the conflict message is personalized to match the design mock
 * ("Sorry, the Vintage Camera was just reserved by another user.").
 */
export function errorToToast(err: unknown, opts?: { itemName?: string }): ToastData {
  const code = toErrorCode(err)
  const message =
    code === 'conflict' && opts?.itemName
      ? `Sorry, the ${opts.itemName} was just reserved by another user.`
      : ERROR_MESSAGES[code]
  return { title: ERROR_TITLES[code], message }
}
