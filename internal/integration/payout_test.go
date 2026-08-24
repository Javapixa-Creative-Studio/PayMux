package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/crypto"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/gateway"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/payout"
)

// Payouts against a real database.
//
// The properties worth testing here are the ones a unit test cannot reach:
// they live in constraints and in the WHERE clause of an UPDATE, and the
// interesting cases are two callers racing. A mistake in any of them moves
// somebody else's money.

// enablePayouts turns disbursement on for an application and gives the
// gateway account the credentials it needs.
func (h *harness) enablePayouts(app *testApplication, accountID string, limits payout.Limits) {
	h.t.Helper()
	ctx := context.Background()

	creator := crypto.Secret("iris-creator")
	approver := crypto.Secret("iris-approver")
	if _, err := h.container.GatewayAccounts.Update(ctx, accountID, gateway.AccountUpdate{
		DisbursementCreatorKey:  &creator,
		DisbursementApproverKey: &approver,
	}); err != nil {
		h.t.Fatalf("set disbursement keys: %v", err)
	}
	if err := h.container.PayoutRepo.SetLimits(ctx, app.ID, limits); err != nil {
		h.t.Fatalf("set payout limits: %v", err)
	}
}

func (h *harness) addBeneficiary(app *testApplication, alias string) *payout.Beneficiary {
	h.t.Helper()
	b := &payout.Beneficiary{
		ApplicationID: app.ID,
		Alias:         alias,
		Name:          "Jon Snow",
		Account:       "1172993826",
		Bank:          "bni",
	}
	if err := h.container.PayoutRepo.CreateBeneficiary(context.Background(), b); err != nil {
		h.t.Fatalf("create beneficiary: %v", err)
	}
	return b
}

func TestPayoutRequiresPermissionEvenWithAValidKey(t *testing.T) {
	// Holding an API key is not the same as being allowed to move money out,
	// and the default has to be the safe one.
	h := newHarness(t)
	accountID := h.setupGatewayAccount()
	app := h.setupApplication("Product A", "product-a")
	h.addBeneficiary(app, "supplier")

	// Credentials present, but nobody enabled the application.
	creator := crypto.Secret("iris-creator")
	if _, err := h.container.GatewayAccounts.Update(context.Background(), accountID,
		gateway.AccountUpdate{DisbursementCreatorKey: &creator}); err != nil {
		t.Fatalf("set key: %v", err)
	}

	resp, body := h.request(http.MethodPost, "/api/v1/payouts", map[string]any{
		"application_payout_id": "PO-1",
		"beneficiary_alias":     "supplier",
		"amount":                1000,
	}, withKey(app.Key))

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body %s", resp.StatusCode, body)
	}
}

func TestConcurrentPayoutRequestsProduceOnePayout(t *testing.T) {
	// A client that retries a request it never saw the answer to must not end
	// up having sent the money twice. The uniqueness constraint is what
	// guarantees it, so this races real requests at a real database.
	h := newHarness(t)
	accountID := h.setupGatewayAccount()
	app := h.setupApplication("Product A", "product-a")
	h.addBeneficiary(app, "supplier")
	h.enablePayouts(app, accountID, payout.Limits{
		Enabled: true, RequiresApproval: true, MaxAmount: ptr(int64(100_000)),
	})

	const attempts = 8
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		created = map[string]int{}
	)
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, body := h.request(http.MethodPost, "/api/v1/payouts", map[string]any{
				"application_payout_id": "SAME-REFERENCE",
				"beneficiary_alias":     "supplier",
				"amount":                50_000,
			}, withKey(app.Key))
			if resp.StatusCode >= 500 {
				t.Errorf("status = %d, body %s", resp.StatusCode, body)
				return
			}
			var p struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(body, &p); err == nil && p.ID != "" {
				mu.Lock()
				created[p.ID]++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if len(created) != 1 {
		t.Fatalf("got %d distinct payouts, want exactly 1: %v", len(created), created)
	}

	var count int
	if err := h.db.Pool().QueryRow(context.Background(),
		`SELECT count(*) FROM payouts WHERE application_id = $1`, app.ID).Scan(&count); err != nil {
		t.Fatalf("count payouts: %v", err)
	}
	if count != 1 {
		t.Fatalf("%d payout rows, want 1 — a retry became a second transfer", count)
	}
}

func TestDailyLimitCountsPayoutsStillInFlight(t *testing.T) {
	// Money the gateway already has cannot be spent again, so a limit that
	// only counted completed payouts would let an application spend the same
	// headroom twice while transfers were in flight.
	h := newHarness(t)
	accountID := h.setupGatewayAccount()
	app := h.setupApplication("Product A", "product-a")
	h.addBeneficiary(app, "supplier")
	h.enablePayouts(app, accountID, payout.Limits{
		Enabled: true, RequiresApproval: true, DailyLimit: ptr(int64(100_000)),
	})

	for i, amount := range []int{60_000, 50_000} {
		resp, body := h.request(http.MethodPost, "/api/v1/payouts", map[string]any{
			"application_payout_id": []string{"PO-1", "PO-2"}[i],
			"beneficiary_alias":     "supplier",
			"amount":                amount,
		}, withKey(app.Key))

		if i == 0 && resp.StatusCode != http.StatusAccepted {
			t.Fatalf("first payout: status = %d, body %s", resp.StatusCode, body)
		}
		// The first is still REQUESTED — nothing has been sent, let alone
		// completed — and it must still consume the day's headroom.
		if i == 1 && resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("second payout: status = %d, want 422; body %s", resp.StatusCode, body)
		}
	}
}

func TestApproverCannotApproveTheirOwnRequest(t *testing.T) {
	// The constraint is in the schema, so this asserts the database refuses it
	// rather than trusting a handler to remember.
	h := newHarness(t)
	accountID := h.setupGatewayAccount()
	app := h.setupApplication("Product A", "product-a")
	beneficiary := h.addBeneficiary(app, "supplier")
	h.enablePayouts(app, accountID, payout.Limits{Enabled: true, RequiresApproval: true})

	ctx := context.Background()
	admin := "adm_01ARZ3NDEKTSV4RRFFQ69G5FAV"
	if _, err := h.db.Pool().Exec(ctx,
		`INSERT INTO admins (id, email, password_hash) VALUES ($1, $2, 'x')`,
		admin, "approver@paymux.test"); err != nil {
		t.Fatalf("seed admin: %v", err)
	}

	_, err := h.db.Pool().Exec(ctx, `
		INSERT INTO payouts (
			id, application_id, gateway_account_id, gateway, application_payout_id,
			idempotency_key, beneficiary_id, beneficiary_name, beneficiary_account,
			beneficiary_bank, amount, currency, normalized_status,
			requested_by, approved_by, approved_at
		) VALUES ('pyo_self', $1, $2, 'midtrans', 'PO-SELF', 'k-self', $3,
		          'Jon Snow', '1172993826', 'bni', 1000, 'IDR', 'APPROVED', $4, $4, now())`,
		app.ID, accountID, beneficiary.ID, admin)
	if err == nil {
		t.Fatal("the database allowed an approver to approve their own request")
	}
}

func TestOnlyOneWorkerClaimsAnApprovedPayout(t *testing.T) {
	// Two workers both deciding to submit the same approved payout is how one
	// transfer becomes two. SKIP LOCKED is what prevents it, and it only means
	// anything against a real database.
	h := newHarness(t)
	accountID := h.setupGatewayAccount()
	app := h.setupApplication("Product A", "product-a")
	beneficiary := h.addBeneficiary(app, "supplier")
	h.enablePayouts(app, accountID, payout.Limits{Enabled: true, RequiresApproval: false,
		MaxAmount: ptr(int64(100_000))})

	ctx := context.Background()
	if _, err := h.db.Pool().Exec(ctx, `
		INSERT INTO payouts (
			id, application_id, gateway_account_id, gateway, application_payout_id,
			idempotency_key, beneficiary_id, beneficiary_name, beneficiary_account,
			beneficiary_bank, amount, currency, normalized_status, status_rank
		) VALUES ('pyo_race', $1, $2, 'midtrans', 'PO-RACE', 'k-race', $3,
		          'Jon Snow', '1172993826', 'bni', 1000, 'IDR', 'APPROVED', 20)`,
		app.ID, accountID, beneficiary.ID); err != nil {
		t.Fatalf("seed payout: %v", err)
	}

	// One transaction claims the row and holds it. A second, running at the
	// same time, must find nothing rather than the same payout.
	tx, err := h.db.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var held string
	if err := tx.QueryRow(ctx, `
		SELECT id FROM payouts WHERE normalized_status = 'APPROVED'
		LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&held); err != nil {
		t.Fatalf("first claim found nothing: %v", err)
	}

	other, err := h.db.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin second: %v", err)
	}
	defer func() { _ = other.Rollback(ctx) }()

	var second string
	if err := other.QueryRow(ctx, `
		SELECT id FROM payouts WHERE normalized_status = 'APPROVED'
		LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&second); err == nil {
		t.Fatalf("a second worker also claimed %s — it would be sent twice", second)
	}
}

func TestPayoutStateCannotMoveBackwards(t *testing.T) {
	// The allowed predecessors go into the UPDATE's WHERE clause, so a stale
	// reconciliation loses rather than overwriting a later state.
	h := newHarness(t)
	accountID := h.setupGatewayAccount()
	app := h.setupApplication("Product A", "product-a")
	beneficiary := h.addBeneficiary(app, "supplier")
	h.enablePayouts(app, accountID, payout.Limits{Enabled: true, RequiresApproval: true})

	ctx := context.Background()
	if _, err := h.db.Pool().Exec(ctx, `
		INSERT INTO payouts (
			id, application_id, gateway_account_id, gateway, application_payout_id,
			idempotency_key, beneficiary_id, beneficiary_name, beneficiary_account,
			beneficiary_bank, amount, currency, normalized_status, status_rank, reference_no
		) VALUES ('pyo_done', $1, $2, 'midtrans', 'PO-DONE', 'k-done', $3,
		          'Jon Snow', '1172993826', 'bni', 1000, 'IDR', 'COMPLETED', 40, 'ref-1')`,
		app.ID, accountID, beneficiary.ID); err != nil {
		t.Fatalf("seed payout: %v", err)
	}

	_, err := h.container.PayoutRepo.ApplyState(ctx, "pyo_done", payout.StateUpdate{
		Status: gateway.PayoutSubmitted,
	})
	if err == nil {
		t.Fatal("a completed payout was walked back to SUBMITTED")
	}

	var status string
	if err := h.db.Pool().QueryRow(ctx,
		`SELECT normalized_status FROM payouts WHERE id = 'pyo_done'`).Scan(&status); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if status != "COMPLETED" {
		t.Fatalf("status = %s, want COMPLETED", status)
	}
}

func ptr[T any](v T) *T { return &v }
