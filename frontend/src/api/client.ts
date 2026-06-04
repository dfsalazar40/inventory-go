import { getUserId } from '../lib/identity'

const BASE_URL = import.meta.env.VITE_API_URL ?? 'http://localhost:8080'

export interface ApiError {
  error: string
  message: string
}

export class ApiRequestError extends Error {
  constructor(
    public readonly status: number,
    public readonly body: ApiError,
  ) {
    super(body.message)
    this.name = 'ApiRequestError'
  }
}

/**
 * Base fetch wrapper that automatically attaches X-User-Id on every request.
 * Pass idempotencyKey for state-mutating requests (POST /reservations).
 */
export async function apiFetch<T>(
  path: string,
  options: RequestInit & { idempotencyKey?: string } = {},
): Promise<T> {
  const { idempotencyKey, ...fetchOptions } = options

  const headers = new Headers(fetchOptions.headers)
  headers.set('Content-Type', 'application/json')
  headers.set('X-User-Id', getUserId())

  if (idempotencyKey) {
    headers.set('Idempotency-Key', idempotencyKey)
  }

  const response = await fetch(`${BASE_URL}${path}`, {
    ...fetchOptions,
    headers,
  })

  if (!response.ok) {
    const body = (await response.json().catch(() => ({
      error: 'unknown',
      message: response.statusText,
    }))) as ApiError
    throw new ApiRequestError(response.status, body)
  }

  // 204 No Content — return empty object cast to T.
  if (response.status === 204) {
    return {} as T
  }

  return response.json() as Promise<T>
}
