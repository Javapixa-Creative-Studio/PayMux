package payout

import (
	"encoding/json"
	"time"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/event"
)

// BuildPayload renders a payout as the body an application receives.
//
// The beneficiary travels with it because the receiving application has to
// reconcile the transfer against its own records, and it is their own
// beneficiary. PayMux's internals do not: the idempotency key and the gateway
// account stay here.
func BuildPayload(p *Payout, t event.Type) event.Payload {
	payload := event.Payload{
		Type:                t,
		Gateway:             p.Gateway,
		ApplicationID:       p.ApplicationID,
		PayoutID:            p.ID,
		ApplicationPayoutID: p.ApplicationPayoutID,
		Status:              string(p.Status),
		GatewayStatus:       p.GatewayStatus,
		Amount:              p.Amount,
		Currency:            p.Currency,
		BeneficiaryName:     p.BeneficiaryName,
		BeneficiaryAccount:  p.BeneficiaryAccount,
		BeneficiaryBank:     p.BeneficiaryBank,
		Notes:               p.Notes,
		FailureCode:         p.FailureCode,
		FailureReason:       p.FailureReason,
		RejectReason:        p.RejectReason,
		CompletedAt:         p.CompletedAt,
		CreatedAt:           time.Now().UTC(),
	}
	if p.ReferenceNo != nil {
		payload.ReferenceNo = *p.ReferenceNo
	}
	if len(p.Metadata) > 0 {
		var metadata map[string]any
		if err := json.Unmarshal(p.Metadata, &metadata); err == nil {
			payload.Metadata = metadata
		}
	}
	return payload
}
