package reports

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct{ pool *pgxpool.Pool }

type Summary struct {
	Open      int64 `json:"open"`
	Actioned  int64 `json:"actioned"`
	Dismissed int64 `json:"dismissed"`
}

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

type Record struct {
	ID                  string          `json:"report_id"`
	ReporterUserID      string          `json:"reporter_user_id"`
	TargetUserID        string          `json:"target_user_id"`
	ConversationID      *string         `json:"conversation_id"`
	MessageID           *string         `json:"message_id"`
	MessageSeq          *int64          `json:"message_seq"`
	Reason              string          `json:"reason"`
	Details             string          `json:"details"`
	MessageSnapshot     json.RawMessage `json:"message_snapshot"`
	Status              string          `json:"status"`
	ResolutionNotes     string          `json:"resolution_notes"`
	NotificationStatus  string          `json:"notification_status"`
	NotificationError   *string         `json:"notification_error"`
	CreatedAt           *time.Time      `json:"created_at"`
	ReviewedAt          *time.Time      `json:"reviewed_at"`
	ReviewedBy          *string         `json:"reviewed_by"`
	ReviewedByModerator *string         `json:"reviewed_by_moderator"`
}

type CreateInput struct {
	ReporterUserID, TargetUserID string
	ConversationID, MessageID    *string
	MessageSeq                   *int64
	Reason, Details              string
	MessageSnapshot              json.RawMessage
}

func (r *Repository) Create(ctx context.Context, in CreateInput) (string, error) {
	var id string
	err := r.pool.QueryRow(ctx, `INSERT INTO moderation_reports
		(reporter_user_id,target_user_id,conversation_id,message_id,message_seq,reason,details,message_snapshot)
		VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''),NULLIF($8,'')::jsonb) RETURNING id::text`,
		in.ReporterUserID, in.TargetUserID, in.ConversationID, in.MessageID, in.MessageSeq,
		in.Reason, in.Details, string(in.MessageSnapshot)).Scan(&id)
	return id, err
}

func (r *Repository) SetNotificationResult(ctx context.Context, id, status string, notificationErr error) error {
	var message any
	if notificationErr != nil {
		message = notificationErr.Error()
	}
	_, err := r.pool.Exec(ctx, `UPDATE moderation_reports SET notification_status=$2,notification_error=$3 WHERE id=$1`, id, status, message)
	return err
}

func (r *Repository) List(ctx context.Context, status string, limit, offset int) ([]Record, error) {
	rows, err := r.pool.Query(ctx, `SELECT id::text,reporter_user_id::text,target_user_id::text,conversation_id,message_id,message_seq,
		reason,COALESCE(details,''),COALESCE(message_snapshot,'null'::jsonb),status,COALESCE(resolution_notes,''),
		notification_status,notification_error,created_at,reviewed_at,reviewed_by::text,reviewed_by_moderator
		FROM moderation_reports WHERE ($1='' OR status=$1) ORDER BY created_at ASC,id ASC LIMIT $2 OFFSET $3`, status, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := make([]Record, 0)
	for rows.Next() {
		var record Record
		if err = rows.Scan(&record.ID, &record.ReporterUserID, &record.TargetUserID, &record.ConversationID, &record.MessageID,
			&record.MessageSeq, &record.Reason, &record.Details, &record.MessageSnapshot, &record.Status, &record.ResolutionNotes,
			&record.NotificationStatus, &record.NotificationError, &record.CreatedAt, &record.ReviewedAt, &record.ReviewedBy, &record.ReviewedByModerator); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (r *Repository) Summary(ctx context.Context) (Summary, error) {
	var summary Summary
	err := r.pool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE status='open'),
		count(*) FILTER (WHERE status='actioned'),
		count(*) FILTER (WHERE status='dismissed')
		FROM moderation_reports`).Scan(&summary.Open, &summary.Actioned, &summary.Dismissed)
	return summary, err
}

func (r *Repository) UpdateStatus(ctx context.Context, id, adminID, moderatorUsername, status, notes string) error {
	result, err := r.pool.Exec(ctx, `UPDATE moderation_reports SET status=$4,resolution_notes=NULLIF($5,''),reviewed_at=now(),reviewed_by=NULLIF($2,'')::uuid,reviewed_by_moderator=NULLIF($3,'')
		WHERE id=$1`, id, adminID, moderatorUsername, status, notes)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("report not found")
	}
	return nil
}
