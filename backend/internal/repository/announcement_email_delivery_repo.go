package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type announcementEmailDeliveryRepository struct {
	db *sql.DB
}

func NewAnnouncementEmailDeliveryRepository(db *sql.DB) service.AnnouncementEmailDeliveryRepository {
	return &announcementEmailDeliveryRepository{db: db}
}

func (r *announcementEmailDeliveryRepository) Claim(ctx context.Context, delivery service.AnnouncementEmailDelivery) (bool, error) {
	if r == nil || r.db == nil {
		return false, fmt.Errorf("announcement email delivery database is unavailable")
	}
	if delivery.AnnouncementID <= 0 || delivery.UserID <= 0 || strings.TrimSpace(delivery.RecipientEmail) == "" {
		return false, fmt.Errorf("invalid announcement email delivery claim")
	}
	attemptedAt := delivery.AttemptedAt.UTC()
	if attemptedAt.IsZero() {
		attemptedAt = time.Now().UTC()
	}
	status := strings.TrimSpace(delivery.Status)
	if status == "" {
		status = service.AnnouncementEmailDeliveryStatusClaimed
	}

	result, err := r.db.ExecContext(ctx, `
		INSERT INTO announcement_email_deliveries (
			announcement_id, user_id, recipient_email, status, attempted_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $5, $5)
		ON CONFLICT (announcement_id, user_id) DO NOTHING
	`, delivery.AnnouncementID, delivery.UserID, strings.TrimSpace(delivery.RecipientEmail), status, attemptedAt)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}

func (r *announcementEmailDeliveryRepository) MarkSent(ctx context.Context, announcementID, userID int64, sentAt time.Time) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("announcement email delivery database is unavailable")
	}
	if announcementID <= 0 || userID <= 0 {
		return fmt.Errorf("invalid announcement email delivery identity")
	}
	if sentAt.IsZero() {
		sentAt = time.Now().UTC()
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE announcement_email_deliveries
		SET status = $3, sent_at = $4, error_message = NULL, updated_at = $4
		WHERE announcement_id = $1 AND user_id = $2
	`, announcementID, userID, service.AnnouncementEmailDeliveryStatusSent, sentAt.UTC())
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("announcement email delivery record not found")
	}
	return nil
}

func (r *announcementEmailDeliveryRepository) MarkFailed(ctx context.Context, announcementID, userID int64, attemptedAt time.Time, failure string) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("announcement email delivery database is unavailable")
	}
	if announcementID <= 0 || userID <= 0 {
		return fmt.Errorf("invalid announcement email delivery identity")
	}
	if attemptedAt.IsZero() {
		attemptedAt = time.Now().UTC()
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE announcement_email_deliveries
		SET status = $3, attempted_at = $4, error_message = $5, updated_at = $4
		WHERE announcement_id = $1 AND user_id = $2
	`, announcementID, userID, service.AnnouncementEmailDeliveryStatusFailed, attemptedAt.UTC(), strings.TrimSpace(failure))
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("announcement email delivery record not found")
	}
	return nil
}

func (r *announcementEmailDeliveryRepository) Summary(ctx context.Context, announcementID int64) (service.AnnouncementEmailDeliverySummary, error) {
	if r == nil || r.db == nil {
		return service.AnnouncementEmailDeliverySummary{}, fmt.Errorf("announcement email delivery database is unavailable")
	}
	if announcementID <= 0 {
		return service.AnnouncementEmailDeliverySummary{}, fmt.Errorf("invalid announcement ID")
	}

	var summary service.AnnouncementEmailDeliverySummary
	err := r.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*) AS total,
			COUNT(*) FILTER (WHERE status = $2) AS claimed,
			COUNT(*) FILTER (WHERE status = $3) AS sent,
			COUNT(*) FILTER (WHERE status = $4) AS failed
		FROM announcement_email_deliveries
		WHERE announcement_id = $1
	`, announcementID,
		service.AnnouncementEmailDeliveryStatusClaimed,
		service.AnnouncementEmailDeliveryStatusSent,
		service.AnnouncementEmailDeliveryStatusFailed,
	).Scan(&summary.Total, &summary.Claimed, &summary.Sent, &summary.Failed)
	if err != nil {
		return service.AnnouncementEmailDeliverySummary{}, err
	}
	return summary, nil
}
