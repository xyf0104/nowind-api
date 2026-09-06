package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type announcementEmailRepoStub struct {
	AnnouncementRepository
	announcement *Announcement
}

func (r *announcementEmailRepoStub) GetByID(_ context.Context, id int64) (*Announcement, error) {
	if r.announcement == nil || r.announcement.ID != id {
		return nil, ErrAnnouncementNotFound
	}
	return r.announcement, nil
}

type announcementEmailUserRepoStub struct {
	UserRepository
	users      map[int64]User
	listParams pagination.PaginationParams
	listFilter UserListFilters
}

func (r *announcementEmailUserRepoStub) ListWithFilters(_ context.Context, params pagination.PaginationParams, filters UserListFilters) ([]User, *pagination.PaginationResult, error) {
	items := make([]User, 0, len(r.users))
	for _, user := range r.users {
		if filters.Status != "" && user.Status != filters.Status {
			continue
		}
		if filters.Role != "" && user.Role != filters.Role {
			continue
		}
		if filters.RequireEmail && strings.TrimSpace(user.Email) == "" {
			continue
		}
		items = append(items, user)
	}
	r.listParams = params
	r.listFilter = filters
	if params.SortBy == "last_activity_at" {
		sort.Slice(items, func(i, j int) bool {
			left := announcementEmailUserActivity(items[i])
			right := announcementEmailUserActivity(items[j])
			if left.Equal(right) {
				return items[i].ID > items[j].ID
			}
			return left.After(right)
		})
	}
	total := len(items)
	start := params.Offset()
	if start > len(items) {
		start = len(items)
	}
	end := start + params.Limit()
	if end > len(items) {
		end = len(items)
	}
	items = items[start:end]
	return items, &pagination.PaginationResult{
		Total:    int64(total),
		Page:     params.Page,
		PageSize: params.PageSize,
		Pages:    (total + params.Limit() - 1) / params.Limit(),
	}, nil
}

func announcementEmailUserActivity(user User) time.Time {
	var latest time.Time
	if user.LastLoginAt != nil {
		latest = *user.LastLoginAt
	}
	if user.LastActiveAt != nil && user.LastActiveAt.After(latest) {
		latest = *user.LastActiveAt
	}
	if user.LastUsedAt != nil && user.LastUsedAt.After(latest) {
		latest = *user.LastUsedAt
	}
	return latest
}

func (r *announcementEmailUserRepoStub) GetByID(_ context.Context, id int64) (*User, error) {
	user, ok := r.users[id]
	if !ok {
		return nil, ErrUserNotFound
	}
	return &user, nil
}

func (r *announcementEmailUserRepoStub) GetByIDs(_ context.Context, ids []int64) ([]*User, error) {
	users := make([]*User, 0, len(ids))
	for _, id := range ids {
		user, ok := r.users[id]
		if !ok {
			continue
		}
		copy := user
		users = append(users, &copy)
	}
	return users, nil
}

type announcementEmailSenderStub struct {
	mu       sync.Mutex
	checkErr error
	sendErr  error
	loginURL string
	sent     []NotificationEmailSendInput
}

func (s *announcementEmailSenderStub) CheckDelivery(context.Context) error {
	return s.checkErr
}

func (s *announcementEmailSenderStub) LoginURL(context.Context) string {
	return s.loginURL
}

func (s *announcementEmailSenderStub) Send(_ context.Context, input NotificationEmailSendInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, input)
	return s.sendErr
}

func (s *announcementEmailSenderStub) sentInputs() []NotificationEmailSendInput {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]NotificationEmailSendInput(nil), s.sent...)
}

type announcementEmailDeliveryKey struct {
	announcementID int64
	userID         int64
}

type announcementEmailDeliveryRecord struct {
	status string
}

type announcementEmailDeliveryMemoryRepo struct {
	mu      sync.Mutex
	records map[announcementEmailDeliveryKey]announcementEmailDeliveryRecord
}

func newAnnouncementEmailDeliveryMemoryRepo() *announcementEmailDeliveryMemoryRepo {
	return &announcementEmailDeliveryMemoryRepo{
		records: make(map[announcementEmailDeliveryKey]announcementEmailDeliveryRecord),
	}
}

func (r *announcementEmailDeliveryMemoryRepo) Claim(_ context.Context, delivery AnnouncementEmailDelivery) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := announcementEmailDeliveryKey{announcementID: delivery.AnnouncementID, userID: delivery.UserID}
	if _, exists := r.records[key]; exists {
		return false, nil
	}
	r.records[key] = announcementEmailDeliveryRecord{status: AnnouncementEmailDeliveryStatusClaimed}
	return true, nil
}

func (r *announcementEmailDeliveryMemoryRepo) MarkSent(_ context.Context, announcementID, userID int64, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := announcementEmailDeliveryKey{announcementID: announcementID, userID: userID}
	if _, exists := r.records[key]; !exists {
		return errors.New("delivery record not found")
	}
	r.records[key] = announcementEmailDeliveryRecord{status: AnnouncementEmailDeliveryStatusSent}
	return nil
}

func (r *announcementEmailDeliveryMemoryRepo) MarkFailed(_ context.Context, announcementID, userID int64, _ time.Time, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := announcementEmailDeliveryKey{announcementID: announcementID, userID: userID}
	if _, exists := r.records[key]; !exists {
		return errors.New("delivery record not found")
	}
	r.records[key] = announcementEmailDeliveryRecord{status: AnnouncementEmailDeliveryStatusFailed}
	return nil
}

func (r *announcementEmailDeliveryMemoryRepo) Summary(_ context.Context, announcementID int64) (AnnouncementEmailDeliverySummary, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var summary AnnouncementEmailDeliverySummary
	for key, record := range r.records {
		if key.announcementID != announcementID {
			continue
		}
		summary.Total++
		switch record.status {
		case AnnouncementEmailDeliveryStatusClaimed:
			summary.Claimed++
		case AnnouncementEmailDeliveryStatusSent:
			summary.Sent++
		case AnnouncementEmailDeliveryStatusFailed:
			summary.Failed++
		}
	}
	return summary, nil
}

func newAnnouncementEmailTestService(users ...User) (*AnnouncementService, *announcementEmailSenderStub, *announcementEmailDeliveryMemoryRepo) {
	userRepo := &announcementEmailUserRepoStub{users: make(map[int64]User, len(users))}
	for _, user := range users {
		if user.Role == "" {
			user.Role = RoleUser
		}
		userRepo.users[user.ID] = user
	}
	announcementRepo := &announcementEmailRepoStub{announcement: &Announcement{
		ID:         41,
		Title:      "Weekend promotion",
		Content:    "## Limited offer\n\n**GPT Pro 20x** is available now.",
		Status:     AnnouncementStatusActive,
		NotifyMode: AnnouncementNotifyModeEmail,
	}}
	sender := &announcementEmailSenderStub{loginURL: "https://xiass.example/login"}
	deliveryRepo := newAnnouncementEmailDeliveryMemoryRepo()
	svc := NewAnnouncementService(announcementRepo, nil, userRepo, nil)
	svc.SetEmailNotificationDependencies(sender, deliveryRepo)
	return svc, sender, deliveryRepo
}

func TestAnnouncementEmailDispatchDeduplicatesConcurrentRequests(t *testing.T) {
	svc, sender, deliveryRepo := newAnnouncementEmailTestService(User{
		ID:       7,
		Email:    "member@example.com",
		Username: "member",
		Status:   StatusActive,
	})

	start := make(chan struct{})
	results := make(chan AnnouncementEmailDispatchResult, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := svc.DispatchEmailNotifications(context.Background(), 41, DispatchAnnouncementEmailInput{
				Scope:   AnnouncementEmailScopeSelected,
				UserIDs: []int64{7},
			})
			results <- result
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	var sent, alreadySent int
	for result := range results {
		sent += result.Sent
		alreadySent += result.AlreadySent
	}
	require.Equal(t, 1, sent)
	require.Equal(t, 1, alreadySent)

	sentInputs := sender.sentInputs()
	require.Len(t, sentInputs, 1)
	require.Equal(t, "https://xiass.example/login", sentInputs[0].Variables["login_url"])
	require.Contains(t, sentInputs[0].RawHTMLVariables["announcement_content_html"], "<strong>GPT Pro 20x</strong>")

	summary, err := deliveryRepo.Summary(context.Background(), 41)
	require.NoError(t, err)
	require.Equal(t, int64(1), summary.Total)
	require.Equal(t, int64(1), summary.Sent)
}

func TestAnnouncementEmailDispatchSkipsActiveUsersWithoutEmail(t *testing.T) {
	svc, sender, _ := newAnnouncementEmailTestService(
		User{ID: 1, Email: "deliver@example.com", Status: StatusActive},
		User{ID: 2, Status: StatusActive},
		User{ID: 3, Email: "disabled@example.com", Status: StatusDisabled},
	)

	result, err := svc.DispatchEmailNotifications(context.Background(), 41, DispatchAnnouncementEmailInput{
		Scope: AnnouncementEmailScopeAll,
	})

	require.NoError(t, err)
	require.Equal(t, 1, result.Targeted)
	require.Equal(t, 1, result.Sent)
	require.Equal(t, 0, result.Skipped)
	require.Len(t, sender.sentInputs(), 1)
}

func TestAnnouncementEmailDispatchAllTargetsThe70MostRecentlyActiveEligibleUsers(t *testing.T) {
	base := time.Now().UTC().Add(-10 * time.Hour)
	users := make([]User, 0, 75)
	for id := int64(1); id <= 75; id++ {
		lastUsed := base.Add(time.Duration(id) * time.Minute)
		user := User{
			ID:         id,
			Email:      fmt.Sprintf("user-%d@example.com", id),
			Role:       RoleUser,
			Status:     StatusActive,
			LastUsedAt: &lastUsed,
		}
		if id == 1 {
			lastLogin := base.Add(30 * time.Hour)
			user.LastLoginAt = &lastLogin
		}
		users = append(users, user)
	}
	adminLogin := base.Add(24 * time.Hour)
	users = append(users, User{ID: 100, Email: "admin@example.com", Role: RoleAdmin, Status: StatusActive, LastLoginAt: &adminLogin})
	disabledLogin := base.Add(25 * time.Hour)
	users = append(users, User{ID: 101, Email: "disabled@example.com", Role: RoleUser, Status: StatusDisabled, LastLoginAt: &disabledLogin})
	users = append(users, User{ID: 102, Email: "", Role: RoleUser, Status: StatusActive, LastLoginAt: &adminLogin})

	svc, sender, _ := newAnnouncementEmailTestService(users...)
	result, err := svc.DispatchEmailNotifications(context.Background(), 41, DispatchAnnouncementEmailInput{Scope: AnnouncementEmailScopeAll})

	require.NoError(t, err)
	require.Equal(t, 70, result.Targeted)
	require.Equal(t, 70, result.Sent)
	repo, ok := svc.userRepo.(*announcementEmailUserRepoStub)
	require.True(t, ok)
	require.Equal(t, "last_activity_at", repo.listParams.SortBy)
	require.Equal(t, 70, repo.listParams.PageSize)
	require.Equal(t, RoleUser, repo.listFilter.Role)
	require.Equal(t, StatusActive, repo.listFilter.Status)
	require.True(t, repo.listFilter.RequireEmail)
	require.Len(t, sender.sentInputs(), 70)
	require.Contains(t, recipientEmails(sender.sentInputs()), "user-1@example.com")
	require.NotContains(t, recipientEmails(sender.sentInputs()), "user-6@example.com")
	for _, input := range sender.sentInputs() {
		require.NotContains(t, input.RecipientEmail, "user-5@example.com")
	}
}

func recipientEmails(inputs []NotificationEmailSendInput) []string {
	emails := make([]string, 0, len(inputs))
	for _, input := range inputs {
		emails = append(emails, input.RecipientEmail)
	}
	return emails
}

func TestAnnouncementEmailDispatchNeverRetriesAFailedRecipient(t *testing.T) {
	svc, sender, deliveryRepo := newAnnouncementEmailTestService(User{
		ID:     9,
		Email:  "member@example.com",
		Status: StatusActive,
	})
	sender.sendErr = errors.New("smtp temporarily unavailable")

	first, err := svc.DispatchEmailNotifications(context.Background(), 41, DispatchAnnouncementEmailInput{
		Scope:   AnnouncementEmailScopeSelected,
		UserIDs: []int64{9},
	})
	require.NoError(t, err)
	require.Equal(t, 1, first.Failed)

	second, err := svc.DispatchEmailNotifications(context.Background(), 41, DispatchAnnouncementEmailInput{
		Scope:   AnnouncementEmailScopeSelected,
		UserIDs: []int64{9},
	})
	require.NoError(t, err)
	require.Equal(t, 1, second.AlreadySent)
	require.Len(t, sender.sentInputs(), 1)

	summary, err := deliveryRepo.Summary(context.Background(), 41)
	require.NoError(t, err)
	require.Equal(t, int64(1), summary.Failed)
}

func TestRenderAnnouncementEmailContentEscapesRawHTML(t *testing.T) {
	html := renderAnnouncementEmailContent("## Notice\n\n<span style=\"color: #e05a47;\"><strong>Important</strong></span> <script>alert('no')</script>\n\nUse `XIASS API`.")

	require.Contains(t, html, "<h2")
	require.Contains(t, html, "<strong>Important</strong>")
	require.Contains(t, html, `<span style="color: #e05a47"><strong>Important</strong></span>`)
	require.Contains(t, html, "&lt;script&gt;alert(&#39;no&#39;)&lt;/script&gt;")
	require.NotContains(t, html, "<script>")
	require.Contains(t, html, "<code")
}

func TestRenderAnnouncementEmailContentRejectsUnsafeInlineAttributes(t *testing.T) {
	html := renderAnnouncementEmailContent(`<span style="color: #e05a47; background: url(https://example.test/track)">Offer</span><a href="https://example.test">open</a>`)

	require.Contains(t, html, `&lt;span style=&#34;color: #e05a47; background: url(https://example.test/track)&#34;&gt;Offer&lt;/span&gt;`)
	require.Contains(t, html, `&lt;a href=&#34;https://example.test&#34;&gt;open&lt;/a&gt;`)
	require.NotContains(t, html, `<a href=`)
}
