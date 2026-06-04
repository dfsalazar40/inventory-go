const USER_ID_KEY = 'inventory_user_id'
const USER_ID_TIMESTAMP_KEY = 'inventory_user_id_ts'

// TTL for the browser-generated user ID (~1 day in milliseconds).
const USER_ID_TTL_MS = 24 * 60 * 60 * 1000

/**
 * Returns the browser-scoped anonymous user ID.
 * Generates a new UUID on first load or after ~1-day TTL expiry, then persists it.
 * Used in the X-User-Id header on every API request.
 */
export function getUserId(): string {
  const stored = localStorage.getItem(USER_ID_KEY)
  const ts = localStorage.getItem(USER_ID_TIMESTAMP_KEY)

  if (stored && ts) {
    const age = Date.now() - Number(ts)
    if (age < USER_ID_TTL_MS) {
      return stored
    }
  }

  // Generate a fresh ID and persist it with a timestamp.
  const id = crypto.randomUUID()
  localStorage.setItem(USER_ID_KEY, id)
  localStorage.setItem(USER_ID_TIMESTAMP_KEY, String(Date.now()))
  return id
}

/**
 * Generates a one-shot idempotency key for a single "Reserve Item" action.
 * The caller is responsible for persisting this key long enough to handle retries.
 */
export function generateIdempotencyKey(): string {
  return crypto.randomUUID()
}
