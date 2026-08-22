/**
 * Typed fetch wrapper around the PayMux admin API.
 *
 * Every failure is normalised into an ApiError carrying PayMux's error code
 * and request id, so screens can branch on the code and operators can quote
 * the request id when reporting a problem.
 */

const RAW_BASE_URL = import.meta.env.VITE_API_BASE_URL ?? '';
export const API_BASE_URL = RAW_BASE_URL.replace(/\/$/, '');

export interface ApiFieldError {
  field: string;
  message: string;
}

export interface ApiErrorBody {
  error: {
    code: string;
    message: string;
    fields?: ApiFieldError[];
    request_id?: string;
  };
}

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly fields: ApiFieldError[];
  readonly requestId?: string;

  constructor(status: number, body: ApiErrorBody | undefined, fallback: string) {
    super(body?.error?.message ?? fallback);
    this.name = 'ApiError';
    this.status = status;
    this.code = body?.error?.code ?? 'UNKNOWN';
    this.fields = body?.error?.fields ?? [];
    this.requestId = body?.error?.request_id;
  }

  /** True when the session has expired or was never established. */
  get isUnauthorized(): boolean {
    return this.status === 401;
  }
}

export interface RequestOptions extends Omit<RequestInit, 'body'> {
  body?: unknown;
  /** Query string parameters; undefined and null values are dropped. */
  query?: Record<string, string | number | boolean | undefined | null>;
}

export async function apiFetch<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const { body, query, headers, ...rest } = options;

  const url = new URL(`${API_BASE_URL}${path}`, window.location.origin);
  if (query) {
    for (const [key, value] of Object.entries(query)) {
      if (value !== undefined && value !== null && value !== '') {
        url.searchParams.set(key, String(value));
      }
    }
  }

  const response = await fetch(url.toString(), {
    ...rest,
    // The dashboard authenticates with a session cookie, so credentials must
    // ride along even when the API lives on another origin.
    credentials: 'include',
    headers: {
      Accept: 'application/json',
      ...(body === undefined ? {} : { 'Content-Type': 'application/json' }),
      ...headers,
    },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  });

  if (response.status === 204) {
    return undefined as T;
  }

  const text = await response.text();
  const parsed: unknown = text ? safeParse(text) : undefined;

  if (!response.ok) {
    throw new ApiError(response.status, parsed as ApiErrorBody | undefined, response.statusText);
  }
  return parsed as T;
}

function safeParse(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return undefined;
  }
}
