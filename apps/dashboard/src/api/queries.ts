/**
 * Query and mutation hooks for the admin API.
 *
 * Query keys are structured `[resource, ...scope]` so a mutation can
 * invalidate exactly the lists a change affects rather than everything.
 */

import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseQueryOptions,
} from '@tanstack/react-query';

import { apiFetch, type RequestOptions } from './client';
import type {
  Admin,
  ApiKey,
  Application,
  Delivery,
  DeliveryDetail,
  Destination,
  GatewayAccount,
  GatewayEvent,
  ListEnvelope,
  Overview,
  Payment,
  PaymentDetail,
  PayMuxEvent,
  Refund,
  Subscription,
} from './types';

const ADMIN = '/admin/api';

/** Query parameters accepted by the list endpoints. */
export type ListParams = RequestOptions['query'];

// ---------------------------------------------------------------------------
// Session
// ---------------------------------------------------------------------------

export const sessionKey = ['session'] as const;

export function useSession(options?: Partial<UseQueryOptions<Admin>>) {
  return useQuery({
    queryKey: sessionKey,
    queryFn: () => apiFetch<Admin>(`${ADMIN}/auth/me`),
    // A 401 here is the answer, not a failure: it means "not signed in".
    retry: false,
    staleTime: 60_000,
    ...options,
  });
}

export function useLogin() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: { email: string; password: string }) =>
      apiFetch<{ admin: Admin; expires_at: string }>(`${ADMIN}/auth/login`, {
        method: 'POST',
        body,
      }),
    onSuccess: (result) => {
      queryClient.setQueryData(sessionKey, result.admin);
    },
  });
}

export function useLogout() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => apiFetch<void>(`${ADMIN}/auth/logout`, { method: 'POST' }),
    onSuccess: () => {
      queryClient.clear();
    },
  });
}

export function useChangePassword() {
  return useMutation({
    mutationFn: (body: { current_password: string; new_password: string }) =>
      apiFetch<void>(`${ADMIN}/auth/password`, { method: 'POST', body }),
  });
}

export function useAdmins() {
  return useQuery({
    queryKey: ['admins'],
    queryFn: () => apiFetch<ListEnvelope<Admin>>(`${ADMIN}/admins`),
  });
}

export function useCreateAdmin() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: { email: string; name: string; password: string }) =>
      apiFetch<Admin>(`${ADMIN}/admins`, { method: 'POST', body }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['admins'] }),
  });
}

// ---------------------------------------------------------------------------
// Overview
// ---------------------------------------------------------------------------

export function useOverview(window = '24h') {
  return useQuery({
    queryKey: ['overview', window],
    queryFn: () => apiFetch<Overview>(`${ADMIN}/overview`, { query: { window } }),
    // The strip is a live readout, so it refreshes on its own.
    refetchInterval: 30_000,
  });
}

// ---------------------------------------------------------------------------
// Applications
// ---------------------------------------------------------------------------

export function useApplications(params?: ListParams) {
  return useQuery({
    queryKey: ['applications', params],
    queryFn: () => apiFetch<ListEnvelope<Application>>(`${ADMIN}/applications`, { query: params }),
  });
}

export function useApplication(id: string | undefined) {
  return useQuery({
    queryKey: ['application', id],
    queryFn: () => apiFetch<Application>(`${ADMIN}/applications/${id}`),
    enabled: Boolean(id),
  });
}

export function useCreateApplication() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: { name: string; slug?: string; description?: string }) =>
      apiFetch<Application>(`${ADMIN}/applications`, { method: 'POST', body }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['applications'] }),
  });
}

export function useUpdateApplication(id: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: Record<string, unknown>) =>
      apiFetch<Application>(`${ADMIN}/applications/${id}`, { method: 'PATCH', body }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['application', id] });
      queryClient.invalidateQueries({ queryKey: ['applications'] });
    },
  });
}

export function useApiKeys(applicationId: string | undefined) {
  return useQuery({
    queryKey: ['api-keys', applicationId],
    queryFn: () => apiFetch<ListEnvelope<ApiKey>>(`${ADMIN}/applications/${applicationId}/keys`),
    enabled: Boolean(applicationId),
  });
}

export function useCreateApiKey(applicationId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: { name: string; mode: 'live' | 'test' }) =>
      apiFetch<ApiKey>(`${ADMIN}/applications/${applicationId}/keys`, { method: 'POST', body }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['api-keys', applicationId] }),
  });
}

export function useRevokeApiKey(applicationId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (keyId: string) =>
      apiFetch<ApiKey>(`${ADMIN}/applications/${applicationId}/keys/${keyId}/revoke`, {
        method: 'POST',
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['api-keys', applicationId] }),
  });
}

export function useDestinations(applicationId: string | undefined) {
  return useQuery({
    queryKey: ['destinations', applicationId],
    queryFn: () =>
      apiFetch<ListEnvelope<Destination>>(`${ADMIN}/applications/${applicationId}/destinations`),
    enabled: Boolean(applicationId),
  });
}

export function useCreateDestination(applicationId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: { url: string; description?: string }) =>
      apiFetch<Destination>(`${ADMIN}/applications/${applicationId}/destinations`, {
        method: 'POST',
        body,
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['destinations', applicationId] }),
  });
}

export function useUpdateDestination(applicationId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: { id: string } & Record<string, unknown>) =>
      apiFetch<Destination>(`${ADMIN}/applications/${applicationId}/destinations/${id}`, {
        method: 'PATCH',
        body,
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['destinations', applicationId] }),
  });
}

export function useRotateDestinationSecret(applicationId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiFetch<Destination>(
        `${ADMIN}/applications/${applicationId}/destinations/${id}/rotate-secret`,
        { method: 'POST' },
      ),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['destinations', applicationId] }),
  });
}

export function useDeleteDestination(applicationId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiFetch<void>(`${ADMIN}/applications/${applicationId}/destinations/${id}`, {
        method: 'DELETE',
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['destinations', applicationId] }),
  });
}

// ---------------------------------------------------------------------------
// Payments
// ---------------------------------------------------------------------------

export function usePayments(params?: ListParams) {
  return useQuery({
    queryKey: ['payments', params],
    queryFn: () => apiFetch<ListEnvelope<Payment>>(`${ADMIN}/payments`, { query: params }),
  });
}

export function usePaymentDetail(id: string | undefined) {
  return useQuery({
    queryKey: ['payment', id],
    queryFn: () => apiFetch<PaymentDetail>(`${ADMIN}/payments/${id}`),
    enabled: Boolean(id),
  });
}

/** Payment actions that reach the gateway: sync, cancel and expire. */
export function usePaymentAction(id: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (action: 'sync' | 'cancel' | 'expire') =>
      apiFetch<Payment>(`${ADMIN}/payments/${id}/${action}`, { method: 'POST' }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['payment', id] });
      queryClient.invalidateQueries({ queryKey: ['payments'] });
    },
  });
}

export function useRefundPayment(id: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: { amount?: number; reason?: string }) =>
      apiFetch<Refund>(`${ADMIN}/payments/${id}/refunds`, { method: 'POST', body }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['payment', id] });
      queryClient.invalidateQueries({ queryKey: ['payments'] });
    },
  });
}

// ---------------------------------------------------------------------------
// Events, deliveries and gateway notifications
// ---------------------------------------------------------------------------

export function useRefunds(params?: ListParams) {
  return useQuery({
    queryKey: ['refunds', params],
    queryFn: () => apiFetch<ListEnvelope<Refund>>(`${ADMIN}/refunds`, { query: params }),
  });
}

export function useEvents(params?: ListParams) {
  return useQuery({
    queryKey: ['events', params],
    queryFn: () => apiFetch<ListEnvelope<PayMuxEvent>>(`${ADMIN}/events`, { query: params }),
  });
}

export function useDeliveries(params?: ListParams) {
  return useQuery({
    queryKey: ['deliveries', params],
    queryFn: () => apiFetch<ListEnvelope<Delivery>>(`${ADMIN}/deliveries`, { query: params }),
  });
}

export function useDeliveryDetail(id: string | undefined) {
  return useQuery({
    queryKey: ['delivery', id],
    queryFn: () => apiFetch<DeliveryDetail>(`${ADMIN}/deliveries/${id}`),
    enabled: Boolean(id),
  });
}

export function useRetryDelivery() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiFetch<Delivery>(`${ADMIN}/deliveries/${id}/retry`, { method: 'POST' }),
    onSuccess: (delivery) => {
      queryClient.invalidateQueries({ queryKey: ['deliveries'] });
      queryClient.invalidateQueries({ queryKey: ['delivery', delivery.id] });
      queryClient.invalidateQueries({ queryKey: ['payment'] });
    },
  });
}

export function useGatewayEvents(params?: ListParams) {
  return useQuery({
    queryKey: ['gateway-events', params],
    queryFn: () =>
      apiFetch<ListEnvelope<GatewayEvent>>(`${ADMIN}/gateway-events`, { query: params }),
  });
}

// ---------------------------------------------------------------------------
// Subscriptions
// ---------------------------------------------------------------------------

export function useSubscriptions(params?: ListParams) {
  return useQuery({
    queryKey: ['subscriptions', params],
    queryFn: () =>
      apiFetch<ListEnvelope<Subscription>>(`${ADMIN}/subscriptions`, { query: params }),
  });
}

export function useSubscriptionAction(id: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (action: 'sync' | 'enable' | 'disable' | 'cancel') =>
      apiFetch<Subscription>(`${ADMIN}/subscriptions/${id}/${action}`, { method: 'POST' }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['subscriptions'] }),
  });
}

// ---------------------------------------------------------------------------
// Gateways
// ---------------------------------------------------------------------------

export function useGatewayAccounts() {
  return useQuery({
    queryKey: ['gateway-accounts'],
    queryFn: () => apiFetch<ListEnvelope<GatewayAccount>>(`${ADMIN}/gateways/accounts`),
  });
}

export function useSupportedGateways() {
  return useQuery({
    queryKey: ['gateways'],
    queryFn: () => apiFetch<ListEnvelope<{ name: string }>>(`${ADMIN}/gateways`),
    staleTime: Infinity,
  });
}

export function useCreateGatewayAccount() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: Record<string, unknown>) =>
      apiFetch<GatewayAccount>(`${ADMIN}/gateways/accounts`, { method: 'POST', body }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['gateway-accounts'] }),
  });
}

export function useUpdateGatewayAccount() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: { id: string } & Record<string, unknown>) =>
      apiFetch<GatewayAccount>(`${ADMIN}/gateways/accounts/${id}`, { method: 'PATCH', body }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['gateway-accounts'] }),
  });
}

/**
 * Checks an account's credentials against the live gateway.
 *
 * The result is stored server-side, so the outcome stays visible after the
 * page is reloaded rather than living only in this session.
 */
export function useTestGatewayAccount() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiFetch<GatewayAccount>(`${ADMIN}/gateways/accounts/${id}/test`, { method: 'POST' }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['gateway-accounts'] }),
  });
}

export function useDeleteGatewayAccount() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiFetch<void>(`${ADMIN}/gateways/accounts/${id}`, { method: 'DELETE' }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['gateway-accounts'] }),
  });
}
