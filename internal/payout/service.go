package payout

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/crypto"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/delivery"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/event"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/gateway"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/storage"
)

// idempotencyWindow is how long a gateway will still answer questions about a
// request under its original key. Midtrans discards keys after 24 hours; this
// is deliberately shorter, so PayMux stops retrying while there is still time
// for a person to intervene rather than at the moment it becomes impossible.
const idempotencyWindow = 20 * time.Hour

// MetricsRecorder observes payouts changing state. The domain depends on this
// narrow interface rather than the metrics package, so instrumentation stays
// optional and testable.
type MetricsRecorder interface {
	RecordPayout(status, gateway string)
}

// Disburser builds the disbursement client for an account. It is a function
// rather than a registry lookup because disbursement credentials live on the
// account and have to be unsealed per call.
type Disburser func(ctx context.Context, acc *gateway.Account) (gateway.DisbursementGateway, error)

// Service implements PayMux's payout operations.
type Service struct {
	db        *storage.DB
	repo      *Repository
	accounts  *gateway.Repository
	disburser Disburser
	publisher *delivery.Publisher
	logger    *slog.Logger
	metrics   MetricsRecorder
}

// SetMetrics attaches a recorder. A nil recorder simply disables the counters.
func (s *Service) SetMetrics(recorder MetricsRecorder) { s.metrics = recorder }

// NewService builds a Service.
func NewService(
	db *storage.DB,
	repo *Repository,
	accounts *gateway.Repository,
	disburser Disburser,
	publisher *delivery.Publisher,
	logger *slog.Logger,
) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{db: db, repo: repo, accounts: accounts,
		disburser: disburser, publisher: publisher, logger: logger}
}

// RequestInput describes a payout somebody wants to make.
type RequestInput struct {
	ApplicationID string
	// GatewayAccountID is resolved by the caller from the application.
	GatewayAccountID string
	Gateway          string

	// ApplicationPayoutID is the caller's own reference. Reusing one returns
	// the original payout rather than making a second.
	ApplicationPayoutID string

	// Exactly one of BeneficiaryID or BeneficiaryAlias identifies where the
	// money goes. PayMux does not accept a raw account number on a payout:
	// a destination has to be a record somebody created first.
	BeneficiaryID    string
	BeneficiaryAlias string

	Amount   int64
	Currency string
	Notes    string
	Metadata json.RawMessage

	// RequestedBy is the administrator who asked, when the request came from
	// the dashboard. Empty when an application asked through the API.
	RequestedBy string
	// KeyMode is the API key's mode, checked against the account environment.
	KeyMode crypto.KeyMode
}

// Request records a payout, subject to every rule that decides whether it may
// happen at all.
//
// The checks run in this order deliberately: permission before limits, limits
// before destination, destination before anything is written. An application
// that was never allowed to disburse should not be told whether a beneficiary
// exists, and nothing at all should be persisted for a request that cannot
// proceed.
func (s *Service) Request(ctx context.Context, in RequestInput) (*Payout, error) {
	if in.Amount <= 0 {
		return nil, fmt.Errorf("payout: amount must be positive")
	}
	if in.ApplicationPayoutID == "" {
		return nil, fmt.Errorf("payout: a payout reference is required")
	}

	limits, err := s.repo.LimitsFor(ctx, in.ApplicationID)
	if err != nil {
		return nil, err
	}
	if !limits.Enabled {
		return nil, ErrPayoutsDisabled
	}
	if limits.MaxAmount != nil && in.Amount > *limits.MaxAmount {
		return nil, fmt.Errorf("%w: the limit is %d", ErrExceedsMaxAmount, *limits.MaxAmount)
	}

	// A retried request must find its original rather than consume headroom
	// twice, so this runs before the daily limit is computed.
	if existing, err := s.repo.GetByApplicationReference(ctx, in.ApplicationID, in.ApplicationPayoutID); err == nil {
		return existing, nil
	} else if !errors.Is(err, ErrPayoutNotFound) {
		return nil, err
	}

	if limits.DailyLimit != nil {
		sent, err := s.repo.SentToday(ctx, in.ApplicationID)
		if err != nil {
			return nil, err
		}
		if sent+in.Amount > *limits.DailyLimit {
			return nil, fmt.Errorf("%w: %d of %d already committed today",
				ErrExceedsDailyLimit, sent, *limits.DailyLimit)
		}
	}

	beneficiary, err := s.resolveBeneficiary(ctx, in)
	if err != nil {
		return nil, err
	}

	status := gateway.PayoutRequested
	if !limits.RequiresApproval {
		status = gateway.PayoutApproved
	}

	p := &Payout{
		ApplicationID:       in.ApplicationID,
		GatewayAccountID:    in.GatewayAccountID,
		Gateway:             in.Gateway,
		ApplicationPayoutID: in.ApplicationPayoutID,
		BeneficiaryID:       &beneficiary.ID,
		// Copied, not referenced: this is where the money went, whatever the
		// address book says later.
		BeneficiaryName:    beneficiary.Name,
		BeneficiaryAccount: beneficiary.Account,
		BeneficiaryBank:    beneficiary.Bank,
		BeneficiaryEmail:   beneficiary.Email,
		Amount:             in.Amount,
		Currency:           strings.ToUpper(in.Currency),
		Notes:              in.Notes,
		Status:             status,
		Metadata:           in.Metadata,
	}
	if in.RequestedBy != "" {
		p.RequestedBy = &in.RequestedBy
	}

	err = s.db.InTx(ctx, func(ctx context.Context, _ storage.Querier) error {
		if err := s.repo.Create(ctx, p); err != nil {
			return err
		}
		actor, actorID := ActorApplication, ""
		if in.RequestedBy != "" {
			actor, actorID = ActorAdmin, in.RequestedBy
		}
		return s.repo.RecordTransition(ctx, &Transition{
			PayoutID: p.ID, ToStatus: status, ActorKind: actor, ActorID: actorID,
		})
	})
	if err != nil {
		return nil, err
	}

	s.publish(ctx, p, event.PayoutRequested)
	if status == gateway.PayoutApproved {
		s.publish(ctx, p, event.PayoutApproved)
	}
	return p, nil
}

func (s *Service) resolveBeneficiary(ctx context.Context, in RequestInput) (*Beneficiary, error) {
	var (
		b   *Beneficiary
		err error
	)
	switch {
	case in.BeneficiaryID != "":
		b, err = s.repo.GetBeneficiary(ctx, in.ApplicationID, in.BeneficiaryID)
	case in.BeneficiaryAlias != "":
		b, err = s.repo.GetBeneficiaryByAlias(ctx, in.ApplicationID, in.BeneficiaryAlias)
	default:
		return nil, fmt.Errorf("%w: name one with beneficiary_id or beneficiary_alias", ErrBeneficiaryNotFound)
	}
	if err != nil {
		return nil, err
	}
	if !b.Usable() {
		return nil, ErrBeneficiaryDisabled
	}
	return b, nil
}

// VerifyBeneficiary asks the bank who owns an account, and records the answer.
//
// This is the last point at which a wrong account number is still cheap. Once
// a payout is submitted the money is gone to whoever that number belongs to,
// and a digit transposed in an address book entry is indistinguishable from a
// correct one until somebody notices they were not paid.
//
// The bank's answer is stored rather than compared automatically. Names differ
// legitimately, a trading name, an initial, a spouse's account, so PayMux
// records what the bank said and lets a person decide whether it is the right
// person.
func (s *Service) VerifyBeneficiary(ctx context.Context, applicationID, beneficiaryID, accountID string) (*Beneficiary, error) {
	b, err := s.repo.GetBeneficiary(ctx, applicationID, beneficiaryID)
	if err != nil {
		return nil, err
	}

	acc, err := s.accounts.Get(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if s.disburser == nil {
		return nil, ErrNotSupported
	}
	client, err := s.disburser(ctx, acc)
	if err != nil {
		return nil, err
	}
	validator, ok := client.(gateway.AccountValidator)
	if !ok {
		return nil, fmt.Errorf("%w: this gateway cannot check accounts", ErrNotSupported)
	}

	result, err := validator.ValidateAccount(ctx, b.Account, b.Bank)
	if err != nil {
		return nil, err
	}
	if err := s.repo.MarkBeneficiaryVerified(ctx, applicationID, beneficiaryID,
		result.AccountName, time.Now().UTC()); err != nil {
		return nil, err
	}
	return s.repo.GetBeneficiary(ctx, applicationID, beneficiaryID)
}

// Balance reports what an account has available to pay out.
//
// PayMux does not gate payouts on this. The per-application limits are what
// bound spending, and a balance read is a snapshot that can be stale by the
// time a transfer executes: refusing a payout because a number looked low a
// moment ago would be its own kind of wrong. This is for an operator to look
// at, not for the system to act on.
func (s *Service) Balance(ctx context.Context, accountID string) (*gateway.Balance, error) {
	client, err := s.clientForAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	reporter, ok := client.(gateway.BalanceReporter)
	if !ok {
		return nil, fmt.Errorf("%w: this gateway cannot report a balance", ErrNotSupported)
	}
	return reporter.GetBalance(ctx)
}

// clientForAccount builds the disbursement client for a gateway account.
func (s *Service) clientForAccount(ctx context.Context, accountID string) (gateway.DisbursementGateway, error) {
	acc, err := s.accounts.Get(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if s.disburser == nil {
		return nil, ErrNotSupported
	}
	return s.disburser(ctx, acc)
}

// Banks lists the destinations this account can pay out to.
func (s *Service) Banks(ctx context.Context, accountID string) ([]gateway.Bank, error) {
	client, err := s.clientForAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	lister, ok := client.(gateway.BankLister)
	if !ok {
		return nil, fmt.Errorf("%w: this gateway cannot list banks", ErrNotSupported)
	}
	return lister.ListBanks(ctx)
}

// Approve releases a payout for submission.
func (s *Service) Approve(ctx context.Context, payoutID, adminID string) (*Payout, error) {
	p, err := s.repo.Get(ctx, payoutID)
	if err != nil {
		return nil, err
	}
	if !p.NeedsApproval() {
		return nil, fmt.Errorf("%w: it is %s", ErrNotPending, p.Status)
	}
	// The database refuses this too. Checking here means the caller gets an
	// explanation rather than a constraint violation.
	if p.RequestedBy != nil && *p.RequestedBy == adminID {
		return nil, ErrSelfApproval
	}

	updated, err := s.transition(ctx, p, StateUpdate{
		Status:     gateway.PayoutApproved,
		ApprovedBy: adminID,
	}, ActorAdmin, adminID, "")
	if err != nil {
		return nil, err
	}
	s.publish(ctx, updated, event.PayoutApproved)
	return updated, nil
}

// Reject refuses a payout. Nothing is sent to the gateway.
func (s *Service) Reject(ctx context.Context, payoutID, adminID, reason string) (*Payout, error) {
	p, err := s.repo.Get(ctx, payoutID)
	if err != nil {
		return nil, err
	}
	if !p.NeedsApproval() {
		return nil, fmt.Errorf("%w: it is %s", ErrNotPending, p.Status)
	}

	updated, err := s.transition(ctx, p, StateUpdate{
		Status:       gateway.PayoutRejected,
		RejectedBy:   adminID,
		RejectReason: reason,
	}, ActorAdmin, adminID, reason)
	if err != nil {
		return nil, err
	}
	s.publish(ctx, updated, event.PayoutRejected)
	return updated, nil
}

// Submit hands an approved payout to the gateway.
//
// This is the only place in PayMux that causes money to leave, and the only
// place where an error's meaning changes the next step rather than just the
// message. Three outcomes, three different states:
//
//   - accepted: SUBMITTED, with the gateway's reference recorded;
//   - refused: FAILED, because the gateway understood and declined;
//   - unknown: UNRESOLVED, because it may have gone out and only a retry
//     under the original idempotency key can tell us.
func (s *Service) Submit(ctx context.Context, payoutID string) (*Payout, error) {
	p, err := s.repo.Get(ctx, payoutID)
	if err != nil {
		return nil, err
	}
	if !p.Submittable() {
		return nil, fmt.Errorf("payout: %s is not awaiting submission", payoutID)
	}

	client, err := s.clientFor(ctx, p)
	if err != nil {
		return nil, err
	}

	result, err := client.CreatePayout(ctx, gateway.CreatePayoutRequest{
		IdempotencyKey:     p.IdempotencyKey,
		Amount:             p.Amount,
		Currency:           p.Currency,
		BeneficiaryName:    p.BeneficiaryName,
		BeneficiaryAccount: p.BeneficiaryAccount,
		BeneficiaryBank:    p.BeneficiaryBank,
		BeneficiaryEmail:   p.BeneficiaryEmail,
		Notes:              p.Notes,
	})
	if err != nil {
		return s.recordSubmissionFailure(ctx, p, err)
	}
	return s.applyGatewayResult(ctx, p, result, ActorGateway, "")
}

// recordSubmissionFailure decides which kind of failure just happened.
func (s *Service) recordSubmissionFailure(ctx context.Context, p *Payout, cause error) (*Payout, error) {
	if errors.Is(cause, gateway.ErrOutcomeUnknown) {
		expires := p.CreatedAt.Add(idempotencyWindow)
		s.logger.Error("payout outcome is unknown; it may have been executed",
			"payout_id", p.ID, "application_id", p.ApplicationID,
			"amount", p.Amount, "retry_until", expires, "error", cause)

		updated, err := s.transition(ctx, p, StateUpdate{
			Status:               gateway.PayoutUnresolved,
			FailureReason:        cause.Error(),
			IdempotencyExpiresAt: &expires,
		}, ActorGateway, "", "outcome unknown")
		if err != nil {
			return nil, err
		}
		// Reported to the caller as well as recorded: a submission whose
		// outcome is unknown is not a success, and treating it as one is how
		// an operator finds out too late.
		return updated, cause
	}

	updated, err := s.transition(ctx, p, StateUpdate{
		Status:        gateway.PayoutFailed,
		FailureReason: cause.Error(),
	}, ActorGateway, "", "rejected by the gateway")
	if err != nil {
		return nil, err
	}
	s.publish(ctx, updated, event.PayoutFailed)
	return updated, nil
}

// applyGatewayResult records what the gateway said about a payout.
func (s *Service) applyGatewayResult(ctx context.Context, p *Payout, result *gateway.PayoutResult, actor, reason string) (*Payout, error) {
	if !p.Status.CanTransitionTo(result.Status) {
		// The gateway is describing a state PayMux already has, or an earlier
		// one. Record that we asked and leave the payout alone.
		if err := s.repo.TouchSynced(ctx, p.ID); err != nil {
			return nil, err
		}
		return p, nil
	}

	updated, err := s.transition(ctx, p, StateUpdate{
		Status:        result.Status,
		GatewayStatus: result.GatewayStatus,
		ReferenceNo:   result.ReferenceNo,
		FailureCode:   result.FailureCode,
		FailureReason: result.FailureReason,
		GatewayData:   result.Raw,
		MarkSynced:    true,
	}, actor, "", reason)
	if err != nil {
		return nil, err
	}
	if t, ok := event.TypeForPayoutStatus(updated.Status); ok {
		s.publish(ctx, updated, t)
	}
	return updated, nil
}

// transition applies a state change and records it in one transaction, so the
// payout and its history can never disagree.
func (s *Service) transition(ctx context.Context, p *Payout, update StateUpdate, actor, actorID, reason string) (*Payout, error) {
	var updated *Payout
	err := s.db.InTx(ctx, func(ctx context.Context, _ storage.Querier) error {
		var err error
		updated, err = s.repo.ApplyState(ctx, p.ID, update)
		if err != nil {
			return err
		}
		return s.repo.RecordTransition(ctx, &Transition{
			PayoutID: p.ID, FromStatus: p.Status, ToStatus: update.Status,
			ActorKind: actor, ActorID: actorID, Reason: reason,
			GatewayData: update.GatewayData,
		})
	})
	if err != nil {
		return nil, err
	}
	if s.metrics != nil {
		s.metrics.RecordPayout(string(update.Status), p.Gateway)
	}
	return updated, nil
}

// Reconcile brings one payout up to date with the gateway.
//
// It handles the three in-flight shapes differently:
//
//   - APPROVED: never sent. Submit it.
//   - SUBMITTED: sent and acknowledged. Ask what happened.
//   - UNRESOLVED: sent, outcome unknown. Re-send under the original
//     idempotency key, which the gateway answers with the original result
//     rather than performing a second transfer, but only while it still
//     remembers the key.
func (s *Service) Reconcile(ctx context.Context, p *Payout) (*Payout, error) {
	switch p.Status {
	case gateway.PayoutApproved:
		return s.Submit(ctx, p.ID)

	case gateway.PayoutSubmitted:
		if p.ReferenceNo == nil {
			// Submitted without a reference should be impossible, but if it
			// happens there is nothing to ask about and guessing is worse.
			return p, s.repo.TouchSynced(ctx, p.ID)
		}
		client, err := s.clientFor(ctx, p)
		if err != nil {
			return nil, err
		}
		result, err := client.GetPayout(ctx, *p.ReferenceNo)
		if err != nil {
			if errors.Is(err, gateway.ErrPayoutNotFound) {
				return p, s.repo.TouchSynced(ctx, p.ID)
			}
			return nil, err
		}
		return s.applyGatewayResult(ctx, p, result, ActorGateway, "reconciled")

	case gateway.PayoutUnresolved:
		return s.resolveUnknown(ctx, p)

	default:
		return p, nil
	}
}

// resolveUnknown settles a payout PayMux is not sure about.
func (s *Service) resolveUnknown(ctx context.Context, p *Payout) (*Payout, error) {
	now := time.Now()
	if !p.Recoverable(now) {
		// The gateway no longer remembers the key, so a retry would be a new
		// request rather than a question about the old one. PayMux stops
		// here rather than risk sending the money twice.
		s.logger.Error("payout outcome can no longer be resolved automatically",
			"payout_id", p.ID, "application_id", p.ApplicationID,
			"amount", p.Amount, "beneficiary", p.BeneficiaryAccount)
		return p, s.repo.TouchSynced(ctx, p.ID)
	}

	client, err := s.clientFor(ctx, p)
	if err != nil {
		return nil, err
	}
	// The same key as the first attempt. If the gateway executed that request
	// it returns the original result; if it did not, this creates it once.
	result, err := client.CreatePayout(ctx, gateway.CreatePayoutRequest{
		IdempotencyKey:     p.IdempotencyKey,
		Amount:             p.Amount,
		Currency:           p.Currency,
		BeneficiaryName:    p.BeneficiaryName,
		BeneficiaryAccount: p.BeneficiaryAccount,
		BeneficiaryBank:    p.BeneficiaryBank,
		BeneficiaryEmail:   p.BeneficiaryEmail,
		Notes:              p.Notes,
	})
	if err != nil {
		if errors.Is(err, gateway.ErrOutcomeUnknown) {
			// Still unknown. Leave it where it is and try again later, while
			// the window lasts.
			return p, s.repo.TouchSynced(ctx, p.ID)
		}
		return s.recordSubmissionFailure(ctx, p, err)
	}
	return s.applyGatewayResult(ctx, p, result, ActorGateway, "resolved under the original idempotency key")
}

// ReconcileBatch settles up to limit in-flight payouts.
func (s *Service) ReconcileBatch(ctx context.Context, limit int, minAge time.Duration) (int, error) {
	var claimed []*Payout
	err := s.db.InTx(ctx, func(ctx context.Context, _ storage.Querier) error {
		var err error
		claimed, err = s.repo.ClaimUnsettled(ctx, limit, minAge)
		return err
	})
	if err != nil {
		return 0, err
	}

	done := 0
	for _, p := range claimed {
		if _, err := s.Reconcile(ctx, p); err != nil && !errors.Is(err, gateway.ErrOutcomeUnknown) {
			s.logger.Warn("could not reconcile a payout",
				"payout_id", p.ID, "status", p.Status, "error", err)
			continue
		}
		done++
	}
	return done, nil
}

// clientFor builds the disbursement client for a payout's account.
func (s *Service) clientFor(ctx context.Context, p *Payout) (gateway.DisbursementGateway, error) {
	acc, err := s.accounts.Get(ctx, p.GatewayAccountID)
	if err != nil {
		return nil, err
	}
	if s.disburser == nil {
		return nil, ErrNotSupported
	}
	client, err := s.disburser(ctx, acc)
	if err != nil {
		return nil, err
	}
	return client, nil
}

// publish emits a payout event, which the delivery pipeline then signs,
// queues and retries exactly as it does a payment's.
func (s *Service) publish(ctx context.Context, p *Payout, t event.Type) {
	if s.publisher == nil {
		return
	}
	e := &event.Event{
		ApplicationID: p.ApplicationID,
		Type:          t,
		Gateway:       p.Gateway,
		PayoutID:      p.ID,
		DedupeKey:     event.PayoutDedupeKey(p.ID, t),
		Payload:       BuildPayload(p, t),
	}
	if _, err := s.publisher.Publish(ctx, e); err != nil {
		s.logger.Error("could not publish a payout event",
			"payout_id", p.ID, "type", t, "error", err)
	}
}

// Get reads a payout.
func (s *Service) Get(ctx context.Context, applicationID, payoutID string) (*Payout, error) {
	if applicationID == "" {
		return s.repo.Get(ctx, payoutID)
	}
	return s.repo.GetForApplication(ctx, applicationID, payoutID)
}

// List returns a page of payouts.
func (s *Service) List(ctx context.Context, filter Filter, page storage.Page) (storage.List[*Payout], error) {
	return s.repo.List(ctx, filter, page)
}

// Transitions reads a payout's history.
func (s *Service) Transitions(ctx context.Context, payoutID string) ([]*Transition, error) {
	return s.repo.Transitions(ctx, payoutID)
}

// Repo exposes the repository for handlers that only read.
func (s *Service) Repo() *Repository { return s.repo }
