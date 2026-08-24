/** Wire types for PayMux's admin API. These mirror `internal/api`. */

export interface ListEnvelope<T> {
  data: T[];
  has_more: boolean;
  limit: number;
}

export type PaymentStatus =
  | 'PENDING'
  | 'AUTHORIZED'
  | 'PAID'
  | 'FAILED'
  | 'CANCELED'
  | 'EXPIRED'
  | 'REFUNDED'
  | 'PARTIALLY_REFUNDED';

export type DeliveryState =
  | 'pending'
  | 'delivering'
  | 'succeeded'
  | 'failed'
  | 'dead'
  | 'canceled';

export type RoutingStatus = 'routed' | 'duplicate' | 'unrouted' | 'rejected' | 'ignored';

export interface Admin {
  id: string;
  email: string;
  name: string;
  status: 'active' | 'disabled';
  last_login_at: string | null;
  created_at: string;
}

export interface Application {
  id: string;
  name: string;
  slug: string;
  description: string;
  gateway_account_id?: string;
  status: 'active' | 'disabled';
  metadata: Record<string, unknown>;
  created_at: string;
  updated_at: string;
}

export interface ApiKey {
  id: string;
  application_id: string;
  name: string;
  mode: 'live' | 'test';
  display_prefix: string;
  status: 'active' | 'revoked' | 'expired';
  last_used_at: string | null;
  expires_at: string | null;
  revoked_at: string | null;
  created_at: string;
  /** Present only in the response that created the key. */
  key?: string;
}

export interface Destination {
  id: string;
  application_id: string;
  url: string;
  description: string;
  event_types: string[];
  enabled: boolean;
  created_at: string;
  updated_at: string;
  /** Present only when the secret is created or rotated. */
  secret?: string;
}

export interface GatewayAccount {
  id: string;
  gateway: string;
  name: string;
  environment: 'sandbox' | 'production';
  merchant_id: string;
  client_key: string;
  server_key_set: boolean;
  disbursement_creator_key_set: boolean;
  disbursement_approver_key_set: boolean;
  enabled: boolean;
  is_default: boolean;
  capabilities: {
    checkout: boolean;
    refund: boolean;
    partial_refund: boolean;
    subscriptions: boolean;
    cancel: boolean;
    expire: boolean;
    // False until the account holds disbursement credentials, whatever the
    // adapter is capable of.
    disbursement: boolean;
  };
  last_checked_at: string | null;
  last_check_ok: boolean | null;
  last_check_error?: string;
  created_at: string;
  updated_at: string;
}

export interface Customer {
  first_name?: string;
  last_name?: string;
  email?: string;
  phone?: string;
}

export interface PaymentItem {
  id?: string;
  name: string;
  price: number;
  quantity: number;
  category?: string;
}

export interface Payment {
  id: string;
  object: 'payment';
  application_id: string;
  gateway: string;
  application_order_id: string;
  gateway_order_id: string;
  gateway_transaction_id?: string;
  amount: number;
  currency: string;
  status: PaymentStatus;
  gateway_status?: string;
  fraud_status?: string;
  payment_type?: string;
  payment_method?: string;
  snap_token?: string;
  redirect_url?: string;
  refunded_amount: number;
  refundable_amount: number;
  metadata: Record<string, unknown>;
  customer?: Customer;
  items?: PaymentItem[];
  expires_at: string | null;
  paid_at?: string;
  canceled_at?: string;
  expired_at?: string;
  created_at: string;
  updated_at: string;
}

export interface Refund {
  id: string;
  object: 'refund';
  payment_id: string;
  application_id?: string;
  gateway_refund_id?: string;
  amount: number;
  currency: string;
  reason?: string;
  status: 'PENDING' | 'SUCCEEDED' | 'FAILED';
  gateway_status?: string;
  failure_reason?: string;
  created_at: string;
  updated_at: string;
}

export interface EventPayload {
  id: string;
  type: string;
  gateway: string;
  application_id: string;
  payment_id?: string;
  refund_id?: string;
  status?: PaymentStatus;
  gateway_status?: string;
  amount?: number;
  currency?: string;
  created_at: string;
  gateway_data?: Record<string, unknown>;
  [key: string]: unknown;
}

export interface PayMuxEvent {
  id: string;
  object: 'event';
  type: string;
  gateway: string;
  application_id: string;
  payment_id?: string;
  refund_id?: string;
  gateway_event_id?: string;
  data: EventPayload;
  created_at: string;
}

export interface Delivery {
  id: string;
  object: 'delivery';
  event_id: string;
  application_id: string;
  destination_id: string;
  url: string;
  state: DeliveryState;
  attempt_count: number;
  max_attempts: number;
  next_attempt_at?: string;
  last_attempt_at?: string;
  last_status_code?: number;
  last_error?: string;
  last_duration_ms?: number;
  succeeded_at?: string;
  created_at: string;
}

export interface DeliveryAttempt {
  id: string;
  attempt_number: number;
  status_code?: number;
  error?: string;
  duration_ms: number;
  response_body?: string;
  created_at: string;
}

export interface GatewayEvent {
  id: string;
  object: 'gateway_event';
  gateway: string;
  application_id?: string;
  payment_id?: string;
  gateway_order_id?: string;
  gateway_transaction_id?: string;
  gateway_status?: string;
  fraud_status?: string;
  signature_verified: boolean;
  routing_status: RoutingStatus;
  routing_error?: string;
  payload?: Record<string, unknown>;
  received_at: string;
  processed_at?: string;
}

export interface Subscription {
  id: string;
  object: 'subscription';
  application_id: string;
  gateway: string;
  gateway_subscription_id?: string;
  name: string;
  amount: number;
  currency: string;
  status: 'ACTIVE' | 'INACTIVE' | 'CANCELED';
  gateway_status?: string;
  interval_unit: string;
  interval_count: number;
  max_interval?: number;
  start_time?: string;
  payment_type?: string;
  metadata: Record<string, unknown>;
  created_at: string;
  updated_at: string;
}

export interface PaymentDetail {
  payment: Payment;
  refunds: Refund[] | null;
  events: PayMuxEvent[] | null;
  deliveries: Delivery[] | null;
  gateway_events: GatewayEvent[] | null;
}

export interface DeliveryDetail {
  delivery: Delivery;
  attempts: DeliveryAttempt[] | null;
}

export interface Overview {
  window: string;
  payments: {
    created: number;
    paid: number;
    pending: number;
    failed: number;
  };
  deliveries: {
    pending: number;
    failed: number;
    succeeded: number;
    dead: number;
  };
  unrouted_notifications: number;
  currency_totals: Array<{
    currency: string;
    paid_total: number;
    count: number;
  }> | null;
  /** Per-application activity, which the routing schematic draws. */
  applications: Array<{
    application_id: string;
    name: string;
    payments: number;
    paid: number;
    pending: number;
    deliveries_ok: number;
    deliveries_failed: number;
    deliveries_dead: number;
  }> | null;
}

/** PayMux's normalized state for money leaving the merchant balance. */
export type PayoutStatus =
  | 'REQUESTED'
  | 'APPROVED'
  | 'SUBMITTED'
  | 'UNRESOLVED'
  | 'COMPLETED'
  | 'FAILED'
  | 'REJECTED';

export interface Beneficiary {
  id: string;
  object: 'beneficiary';
  application_id: string;
  alias: string;
  name: string;
  account: string;
  bank: string;
  email?: string;
  verified_at?: string | null;
  verified_name?: string;
  disabled_at?: string | null;
  created_at: string;
  updated_at: string;
}

export interface Payout {
  id: string;
  object: 'payout';
  application_id: string;
  gateway: string;
  application_payout_id: string;
  reference_no?: string;
  beneficiary_id?: string;
  beneficiary_name: string;
  beneficiary_account: string;
  beneficiary_bank: string;
  beneficiary_email?: string;
  amount: number;
  currency: string;
  notes?: string;
  status: PayoutStatus;
  gateway_status?: string;
  failure_code?: string;
  failure_reason?: string;
  requested_by?: string;
  approved_by?: string;
  approved_at?: string;
  rejected_by?: string;
  rejected_at?: string;
  reject_reason?: string;
  submitted_at?: string;
  completed_at?: string;
  failed_at?: string;
  created_at: string;
  updated_at: string;
}

/** One recorded change of state, with who caused it. */
export interface PayoutTransition {
  id: string;
  payout_id: string;
  from_status?: PayoutStatus | '';
  to_status: PayoutStatus;
  actor_kind: 'application' | 'admin' | 'gateway' | 'worker';
  actor_id?: string;
  reason?: string;
  created_at: string;
}

export interface PayoutDetail {
  payout: Payout;
  transitions: PayoutTransition[] | null;
}

/** What an application is permitted to disburse. null means no ceiling. */
export interface PayoutLimits {
  enabled: boolean;
  requires_approval: boolean;
  max_amount: number | null;
  daily_limit: number | null;
}
