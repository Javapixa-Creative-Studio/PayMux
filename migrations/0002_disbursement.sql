-- Disbursement: paying money out to a named account.
--
-- Everything before this migration moves money in one direction — a customer
-- pays, and the worst case of a mistake is a payment that has to be refunded
-- through the gateway that took it. A payout has no such backstop. It leaves
-- the merchant balance for an account the caller names, and nothing outside
-- PayMux constrains the amount or the destination.
--
-- So the guardrails live here, in the schema, rather than in handler code
-- where a later refactor could route around them:
--
--   * an application cannot disburse at all until someone turns it on;
--   * every payout is bounded by a per-payout and a per-day ceiling;
--   * an approver may not approve their own request;
--   * the same logical payout cannot be submitted twice, however many times
--     the caller retries;
--   * the beneficiary is copied onto the payout, so editing an address book
--     entry can never rewrite where money already went.

-- ---------------------------------------------------------------------------
-- Disbursement credentials, per gateway account.
--
-- Midtrans issues separate creator and approver keys for exactly the reason
-- PayMux wants them: whoever can request a payout must not be able to release
-- it. They are held apart here so that a leak of one is not a leak of both.
-- Both are empty until an operator configures disbursement, and an empty
-- creator key is what "this account cannot pay out" means.
-- ---------------------------------------------------------------------------

ALTER TABLE gateway_accounts
    ADD COLUMN disbursement_creator_key_encrypted  TEXT NOT NULL DEFAULT '',
    ADD COLUMN disbursement_approver_key_encrypted TEXT NOT NULL DEFAULT '';

-- ---------------------------------------------------------------------------
-- Per-application permission and ceilings.
--
-- Off by default. PayMux's whole premise is that many applications share one
-- merchant account, which is harmless while money only flows in and is the
-- entire risk once it can flow out: without this, any application holding an
-- API key could drain a balance it does not own.
-- ---------------------------------------------------------------------------

ALTER TABLE applications
    ADD COLUMN payout_enabled       BOOLEAN NOT NULL DEFAULT false,
    -- NULL means "no ceiling", which is only reachable by an explicit choice.
    ADD COLUMN payout_max_amount    BIGINT,
    ADD COLUMN payout_daily_limit   BIGINT,
    -- Whether a human must release each payout. On by default: the safe
    -- setting is the one you get by not thinking about it.
    ADD COLUMN payout_requires_approval BOOLEAN NOT NULL DEFAULT true,
    ADD CONSTRAINT applications_payout_max_positive
        CHECK (payout_max_amount IS NULL OR payout_max_amount > 0),
    ADD CONSTRAINT applications_payout_daily_positive
        CHECK (payout_daily_limit IS NULL OR payout_daily_limit > 0);

-- ---------------------------------------------------------------------------
-- Beneficiaries: an application's address book of payout destinations.
--
-- Keeping them as records rather than accepting a raw account number on every
-- request means a destination can be reviewed once and reused, and that
-- changing one is an auditable act rather than a different string in a
-- payload.
-- ---------------------------------------------------------------------------

CREATE TABLE beneficiaries (
    id             TEXT PRIMARY KEY,
    application_id TEXT        NOT NULL REFERENCES applications (id) ON DELETE RESTRICT,

    -- The application's own handle for this destination, unique to it.
    alias          TEXT        NOT NULL,
    name           TEXT        NOT NULL,
    -- A bank account number, or a phone number for an e-wallet destination.
    account        TEXT        NOT NULL,
    -- A bank code, or 'gopay' / 'ovo' for e-wallets.
    bank           TEXT        NOT NULL,
    email          TEXT        NOT NULL DEFAULT '',

    -- Set once the gateway has confirmed the account exists and the name
    -- matches. A payout to an unverified beneficiary is allowed but flagged.
    verified_at    TIMESTAMPTZ,
    verified_name  TEXT        NOT NULL DEFAULT '',

    disabled_at    TIMESTAMPTZ,
    metadata       JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT beneficiaries_alias_present   CHECK (alias <> ''),
    CONSTRAINT beneficiaries_account_present CHECK (account <> ''),
    CONSTRAINT beneficiaries_bank_present    CHECK (bank <> '')
);

CREATE UNIQUE INDEX beneficiaries_alias_key
    ON beneficiaries (application_id, lower(alias));

CREATE INDEX beneficiaries_application_idx
    ON beneficiaries (application_id, created_at DESC);

-- ---------------------------------------------------------------------------
-- Payouts.
-- ---------------------------------------------------------------------------

CREATE TABLE payouts (
    id                    TEXT PRIMARY KEY,
    application_id        TEXT        NOT NULL REFERENCES applications (id) ON DELETE RESTRICT,
    gateway_account_id    TEXT        NOT NULL REFERENCES gateway_accounts (id) ON DELETE RESTRICT,
    gateway               TEXT        NOT NULL,

    -- The caller's own reference. Unique within the application, which is what
    -- makes a retried request the same payout rather than a second one.
    application_payout_id TEXT        NOT NULL,

    -- What PayMux sends as X-Idempotency-Key. Chosen before the first call and
    -- never regenerated, so a retry after a timeout asks the gateway about the
    -- original request instead of starting a new one.
    idempotency_key       TEXT        NOT NULL,

    -- The gateway's identifier. NULL while a submission's outcome is unknown,
    -- which is the state that matters most: money may or may not be moving.
    reference_no          TEXT,

    -- The address book entry this came from, kept for provenance only.
    beneficiary_id        TEXT        REFERENCES beneficiaries (id) ON DELETE SET NULL,
    -- ...and a copy of it as it was at the moment of request. A payout is a
    -- historical fact; editing a beneficiary tomorrow must not appear to
    -- change where yesterday's money went.
    beneficiary_name      TEXT        NOT NULL,
    beneficiary_account   TEXT        NOT NULL,
    beneficiary_bank      TEXT        NOT NULL,
    beneficiary_email     TEXT        NOT NULL DEFAULT '',

    amount                BIGINT      NOT NULL CHECK (amount > 0),
    currency              TEXT        NOT NULL,
    notes                 TEXT        NOT NULL DEFAULT '',

    normalized_status     TEXT        NOT NULL,
    -- Monotonic, so a stale poll cannot walk a completed payout backwards.
    status_rank           SMALLINT    NOT NULL DEFAULT 0,
    gateway_status        TEXT        NOT NULL DEFAULT '',
    failure_code          TEXT        NOT NULL DEFAULT '',
    failure_reason        TEXT        NOT NULL DEFAULT '',

    -- Who asked. NULL when an application asked through the API.
    requested_by          TEXT        REFERENCES admins (id) ON DELETE SET NULL,
    approved_by           TEXT        REFERENCES admins (id) ON DELETE SET NULL,
    approved_at           TIMESTAMPTZ,
    rejected_by           TEXT        REFERENCES admins (id) ON DELETE SET NULL,
    rejected_at           TIMESTAMPTZ,
    reject_reason         TEXT        NOT NULL DEFAULT '',

    submitted_at          TIMESTAMPTZ,
    completed_at          TIMESTAMPTZ,
    failed_at             TIMESTAMPTZ,
    last_synced_at        TIMESTAMPTZ,
    -- When the gateway will stop honouring the idempotency key. After this
    -- passes with no known outcome, retrying is no longer safe and a human
    -- has to reconcile against the gateway's own records.
    idempotency_expires_at TIMESTAMPTZ,

    gateway_data          JSONB       NOT NULL DEFAULT '{}'::jsonb,
    metadata              JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Dual control. An approver who is also the requester is one person moving
    -- money unwitnessed, which is the thing approval exists to prevent.
    CONSTRAINT payouts_dual_control
        CHECK (approved_by IS NULL OR requested_by IS NULL OR approved_by <> requested_by),

    -- A payout is released or refused, never both.
    CONSTRAINT payouts_not_both_outcomes
        CHECK (approved_at IS NULL OR rejected_at IS NULL)
);

-- The constraint that stops a retry becoming a second transfer.
CREATE UNIQUE INDEX payouts_application_reference_key
    ON payouts (application_id, application_payout_id);

CREATE UNIQUE INDEX payouts_idempotency_key
    ON payouts (idempotency_key);

-- Gateway references are unique per account, and absent while in flight.
CREATE UNIQUE INDEX payouts_gateway_reference_key
    ON payouts (gateway_account_id, reference_no)
    WHERE reference_no IS NOT NULL;

CREATE INDEX payouts_application_idx
    ON payouts (application_id, created_at DESC);

CREATE INDEX payouts_status_idx
    ON payouts (normalized_status, created_at DESC);

CREATE INDEX payouts_beneficiary_idx
    ON payouts (beneficiary_id, created_at DESC);

-- The worker's queue: everything whose outcome PayMux does not yet know.
-- Partial, because settled payouts are the overwhelming majority and none of
-- them ever needs polling again.
CREATE INDEX payouts_unsettled_idx
    ON payouts (last_synced_at NULLS FIRST)
    WHERE normalized_status IN ('APPROVED', 'SUBMITTED', 'UNRESOLVED');

-- ---------------------------------------------------------------------------
-- Payout events reuse the existing pipeline.
--
-- events already carries one nullable reference per subject, and deliveries
-- are keyed on events rather than on payments, so a payout event is signed,
-- queued, retried and replayed by exactly the machinery that already carries
-- payment events. Nothing about delivery needed to learn what a payout is.
-- ---------------------------------------------------------------------------

ALTER TABLE events
    ADD COLUMN payout_id TEXT REFERENCES payouts (id) ON DELETE SET NULL;

CREATE INDEX events_payout_idx ON events (payout_id, created_at DESC);

-- ---------------------------------------------------------------------------
-- Every state change, kept forever.
--
-- A payment can be re-derived from the gateway; a payout that went to the
-- wrong account is a question about who did what and when, so the answer has
-- to survive independently of the gateway's own retention.
-- ---------------------------------------------------------------------------

CREATE TABLE payout_transitions (
    id            TEXT PRIMARY KEY,
    payout_id     TEXT        NOT NULL REFERENCES payouts (id) ON DELETE CASCADE,
    from_status   TEXT        NOT NULL DEFAULT '',
    to_status     TEXT        NOT NULL,
    -- 'application', 'admin', 'gateway' or 'worker'.
    actor_kind    TEXT        NOT NULL,
    actor_id      TEXT        NOT NULL DEFAULT '',
    reason        TEXT        NOT NULL DEFAULT '',
    gateway_data  JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX payout_transitions_payout_idx
    ON payout_transitions (payout_id, created_at);
