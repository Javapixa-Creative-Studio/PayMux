// Package audit records who changed what through PayMux's management APIs.
package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/Javapixa-Creative-Studio/PayMux/internal/ids"
	"github.com/Javapixa-Creative-Studio/PayMux/internal/storage"
)

// Entry is one recorded action.
type Entry struct {
	ActorType  string
	ActorID    string
	Action     string
	TargetType string
	TargetID   string
	RequestID  string
	IPAddress  string
	Metadata   map[string]any
}

// Recorder appends audit entries.
type Recorder struct {
	db     *storage.DB
	logger *slog.Logger
}

// NewRecorder builds a Recorder.
func NewRecorder(db *storage.DB, logger *slog.Logger) *Recorder {
	if logger == nil {
		logger = slog.Default()
	}
	return &Recorder{db: db, logger: logger}
}

// Record writes an entry, joining the caller's transaction when there is one
// so an audited change and its record commit together.
func (r *Recorder) Record(ctx context.Context, entry Entry) error {
	metadata := []byte("{}")
	if entry.Metadata != nil {
		encoded, err := json.Marshal(entry.Metadata)
		if err != nil {
			return fmt.Errorf("audit: encode metadata: %w", err)
		}
		metadata = encoded
	}
	_, err := r.db.FromContext(ctx).Exec(ctx, `
		INSERT INTO audit_logs
			(id, actor_type, actor_id, action, target_type, target_id, request_id, ip_address, metadata)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		ids.New(ids.AuditLog), entry.ActorType, entry.ActorID, entry.Action,
		entry.TargetType, entry.TargetID, entry.RequestID, entry.IPAddress, metadata,
	)
	if err != nil {
		return fmt.Errorf("audit: record %s: %w", entry.Action, err)
	}
	return nil
}

// TryRecord writes an entry and logs, rather than returns, a failure.
//
// Audit writes must not fail the operation they describe: losing the record of
// a successful change is bad, but rolling back a completed payment change
// because its log line could not be written would be worse.
func (r *Recorder) TryRecord(ctx context.Context, entry Entry) {
	if err := r.Record(ctx, entry); err != nil {
		r.logger.Error("could not write audit entry",
			"action", entry.Action,
			"target_type", entry.TargetType,
			"target_id", entry.TargetID,
			"error", err,
		)
	}
}
