/**
 * Test harness for the dashboard.
 *
 * Screens are rendered against a stubbed `fetch` rather than a mocked query
 * layer, so the tests exercise the request the page actually makes, including
 * the query string, which is where a filter bug would hide.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, type RenderResult } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { vi } from 'vitest';
import type { ReactElement } from 'react';

/** One stubbed endpoint: a matcher and what it answers with. */
export interface Stub {
  method?: string;
  /** Matched against the path and query string. */
  path: string | RegExp;
  status?: number;
  /**
   * The response. A function is called per request, so a stub can reflect
   * state a previous call changed, which is what a refetch after a mutation
   * needs in order to be meaningful.
   */
  body?: unknown | (() => unknown);
}

/** A request the component under test made. */
export interface RecordedRequest {
  method: string;
  url: string;
  body: unknown;
}

export interface Api {
  requests: RecordedRequest[];
  /** Requests made to a path, for asserting on what a page asked for. */
  to(path: string): RecordedRequest[];
}

/** Installs a fetch stub for the given routes and records every call. */
export function stubApi(routes: Stub[]): Api {
  const requests: RecordedRequest[] = [];

  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const url = typeof input === 'string' ? input : input.toString();
      const method = (init?.method ?? 'GET').toUpperCase();
      const path = url.replace(/^https?:\/\/[^/]+/, '');

      requests.push({
        method,
        url: path,
        body: init?.body ? JSON.parse(String(init.body)) : undefined,
      });

      const route = routes.find((candidate) => {
        if (candidate.method && candidate.method.toUpperCase() !== method) return false;
        return typeof candidate.path === 'string'
          ? path.startsWith(candidate.path)
          : candidate.path.test(path);
      });

      if (!route) {
        // An unstubbed call is a test bug, not a 404 to be silently rendered.
        throw new Error(`no stub for ${method} ${path}`);
      }

      const status = route.status ?? 200;
      const body = typeof route.body === 'function' ? (route.body as () => unknown)() : route.body;
      return new Response(body === undefined ? '' : JSON.stringify(body), {
        status,
        headers: { 'Content-Type': 'application/json' },
      });
    }),
  );

  return {
    requests,
    to(path: string) {
      return requests.filter((request) => request.url.startsWith(path));
    },
  };
}

/**
 * Renders a screen with the providers it expects.
 *
 * routePath matters for screens that read a URL parameter: without a matching
 * route, useParams returns nothing and the page renders as though the record
 * were missing.
 */
export function renderScreen(ui: ReactElement, initialPath = '/', routePath?: string): RenderResult {
  const queryClient = new QueryClient({
    defaultOptions: {
      // Retries would make a failing-path test wait for backoff.
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  });

  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[initialPath]}>
        {routePath ? (
          <Routes>
            <Route path={routePath} element={ui} />
          </Routes>
        ) : (
          ui
        )}
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

/** A page of results in the API's envelope. */
export function page<T>(data: T[]) {
  return { data, has_more: false, limit: 25 };
}
