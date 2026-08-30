package service

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

const (
	AnnouncementStatusDraft    = domain.AnnouncementStatusDraft
	AnnouncementStatusActive   = domain.AnnouncementStatusActive
	AnnouncementStatusArchived = domain.AnnouncementStatusArchived
)

const (
	AnnouncementNotifyModeSilent = domain.AnnouncementNotifyModeSilent
	AnnouncementNotifyModePopup  = domain.AnnouncementNotifyModePopup
	AnnouncementNotifyModeEmail  = domain.AnnouncementNotifyModeEmail
)

const (
	AnnouncementConditionTypeSubscription = domain.AnnouncementConditionTypeSubscription
	AnnouncementConditionTypeBalance      = domain.AnnouncementConditionTypeBalance
)

const (
	AnnouncementOperatorIn  = domain.AnnouncementOperatorIn
	AnnouncementOperatorGT  = domain.AnnouncementOperatorGT
	AnnouncementOperatorGTE = domain.AnnouncementOperatorGTE
	AnnouncementOperatorLT  = domain.AnnouncementOperatorLT
	AnnouncementOperatorLTE = domain.AnnouncementOperatorLTE
	AnnouncementOperatorEQ  = domain.AnnouncementOperatorEQ
)

var (
	ErrAnnouncementNotFound        = domain.ErrAnnouncementNotFound
	ErrAnnouncementInvalidTarget   = domain.ErrAnnouncementInvalidTarget
	ErrAnnouncementNilInput        = infraerrors.BadRequest("ANNOUNCEMENT_INPUT_REQUIRED", "announcement input is required")
	ErrAnnouncementInvalidTitle    = infraerrors.BadRequest("ANNOUNCEMENT_TITLE_INVALID", "announcement title is invalid")
	ErrAnnouncementContentRequired = infraerrors.BadRequest(
		"ANNOUNCEMENT_CONTENT_REQUIRED",
		"announcement content is required",
	)
	ErrAnnouncementInvalidStatus     = infraerrors.BadRequest("ANNOUNCEMENT_STATUS_INVALID", "announcement status is invalid")
	ErrAnnouncementInvalidNotifyMode = infraerrors.BadRequest(
		"ANNOUNCEMENT_NOTIFY_MODE_INVALID",
		"announcement notify_mode is invalid",
	)
	ErrAnnouncementEmailNotEnabled = infraerrors.BadRequest(
		"ANNOUNCEMENT_EMAIL_NOT_ENABLED",
		"email notification is not enabled for this announcement",
	)
	ErrAnnouncementEmailInvalidScope = infraerrors.BadRequest(
		"ANNOUNCEMENT_EMAIL_SCOPE_INVALID",
		"email notification scope must be all or selected",
	)
	ErrAnnouncementEmailSelectionRequired = infraerrors.BadRequest(
		"ANNOUNCEMENT_EMAIL_SELECTION_REQUIRED",
		"select at least one active user before sending email notifications",
	)
	ErrAnnouncementEmailTooManyUsers = infraerrors.BadRequest(
		"ANNOUNCEMENT_EMAIL_TOO_MANY_USERS",
		"too many selected users for one email notification request",
	)
	ErrAnnouncementEmailUnavailable = infraerrors.ServiceUnavailable(
		"ANNOUNCEMENT_EMAIL_UNAVAILABLE",
		"email notification service is not configured",
	)
	ErrAnnouncementInvalidSchedule = infraerrors.BadRequest(
		"ANNOUNCEMENT_TIME_RANGE_INVALID",
		"starts_at must be before ends_at",
	)
)

type AnnouncementTargeting = domain.AnnouncementTargeting

type AnnouncementConditionGroup = domain.AnnouncementConditionGroup

type AnnouncementCondition = domain.AnnouncementCondition

type Announcement = domain.Announcement

type AnnouncementListFilters struct {
	Status string
	Search string
}

type AnnouncementRepository interface {
	Create(ctx context.Context, a *Announcement) error
	GetByID(ctx context.Context, id int64) (*Announcement, error)
	Update(ctx context.Context, a *Announcement) error
	Delete(ctx context.Context, id int64) error

	List(ctx context.Context, params pagination.PaginationParams, filters AnnouncementListFilters) ([]Announcement, *pagination.PaginationResult, error)
	ListActive(ctx context.Context, now time.Time) ([]Announcement, error)
}

type AnnouncementReadRepository interface {
	MarkRead(ctx context.Context, announcementID, userID int64, readAt time.Time) error
	GetReadMapByUser(ctx context.Context, userID int64, announcementIDs []int64) (map[int64]time.Time, error)
	GetReadMapByUsers(ctx context.Context, announcementID int64, userIDs []int64) (map[int64]time.Time, error)
	CountByAnnouncementID(ctx context.Context, announcementID int64) (int64, error)
}

const (
	AnnouncementEmailDeliveryStatusClaimed = "claimed"
	AnnouncementEmailDeliveryStatusSent    = "sent"
	AnnouncementEmailDeliveryStatusFailed  = "failed"
)

type AnnouncementEmailDelivery struct {
	AnnouncementID int64
	UserID         int64
	RecipientEmail string
	Status         string
	AttemptedAt    time.Time
}

type AnnouncementEmailDeliverySummary struct {
	Total   int64 `json:"total"`
	Claimed int64 `json:"claimed"`
	Sent    int64 `json:"sent"`
	Failed  int64 `json:"failed"`
}

// AnnouncementEmailDeliveryRepository owns the durable, atomic delivery claim.
// The unique announcement/user key is intentionally independent from SMTP so a
// retrying HTTP request can never issue the same announcement twice.
type AnnouncementEmailDeliveryRepository interface {
	Claim(ctx context.Context, delivery AnnouncementEmailDelivery) (bool, error)
	MarkSent(ctx context.Context, announcementID, userID int64, sentAt time.Time) error
	MarkFailed(ctx context.Context, announcementID, userID int64, attemptedAt time.Time, failure string) error
	Summary(ctx context.Context, announcementID int64) (AnnouncementEmailDeliverySummary, error)
}
