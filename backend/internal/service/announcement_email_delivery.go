package service

import (
	"context"
	"errors"
	"fmt"
	"html"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

const (
	AnnouncementEmailScopeAll      = "all"
	AnnouncementEmailScopeSelected = "selected"

	announcementEmailRecipientPageSize = 500
	announcementEmailSelectedUserLimit = 1000
	announcementEmailWorkerCount       = 4
	announcementEmailContentLimit      = 40000
)

var (
	announcementEmailStrongMarkdown = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
	announcementEmailEmMarkdown     = regexp.MustCompile(`(?m)(?:\*|_)([^*_\n]+)(?:\*|_)`)
	announcementEmailCodeMarkdown   = regexp.MustCompile("`([^`\\n]+)`")
	announcementEmailHTMLTag        = regexp.MustCompile(`(?is)^<\s*(/?)\s*([a-z][a-z0-9]*)\s*([^>]*)>$`)
	announcementEmailStyleAttribute = regexp.MustCompile(`(?is)^style\s*=\s*(?:"([^"]*)"|'([^']*)')\s*$`)
	announcementEmailHexColor       = regexp.MustCompile(`(?i)^#[0-9a-f]{3}(?:[0-9a-f]{3})?(?:[0-9a-f]{2})?$`)
)

type DispatchAnnouncementEmailInput struct {
	Scope   string
	UserIDs []int64
}

type AnnouncementEmailDispatchResult struct {
	Targeted    int `json:"targeted"`
	Claimed     int `json:"claimed"`
	Sent        int `json:"sent"`
	Failed      int `json:"failed"`
	AlreadySent int `json:"already_sent"`
	Skipped     int `json:"skipped"`
}

type announcementEmailDispatchOutcome struct {
	state string
	err   error
}

type announcementEmailUserBatchRepository interface {
	GetByIDs(ctx context.Context, ids []int64) ([]*User, error)
}

// DispatchEmailNotifications sends a manually-confirmed announcement email to
// active recipients. A recipient is first atomically claimed in PostgreSQL;
// the actual SMTP operation only happens after the claim succeeds.
func (s *AnnouncementService) DispatchEmailNotifications(
	ctx context.Context,
	announcementID int64,
	input DispatchAnnouncementEmailInput,
) (AnnouncementEmailDispatchResult, error) {
	var result AnnouncementEmailDispatchResult
	if s == nil || s.announcementRepo == nil || s.userRepo == nil || s.emailSender == nil || s.emailDeliveryRepo == nil {
		return result, ErrAnnouncementEmailUnavailable
	}

	announcement, err := s.announcementRepo.GetByID(ctx, announcementID)
	if err != nil {
		return result, err
	}
	if announcement.NotifyMode != AnnouncementNotifyModeEmail {
		return result, ErrAnnouncementEmailNotEnabled
	}
	if err := validateAnnouncementEmailDispatchInput(input); err != nil {
		return result, err
	}
	if err := s.emailSender.CheckDelivery(ctx); err != nil {
		return result, ErrAnnouncementEmailUnavailable.WithCause(err)
	}

	recipients, skipped, err := s.listAnnouncementEmailRecipients(ctx, input)
	if err != nil {
		return result, err
	}
	result.Skipped = skipped
	result.Targeted = len(recipients)
	if len(recipients) == 0 {
		return result, nil
	}

	loginURL := s.emailSender.LoginURL(ctx)
	contentHTML := renderAnnouncementEmailContent(announcement.Content)
	jobs := make(chan User)
	outcomes := make(chan announcementEmailDispatchOutcome, len(recipients))
	workerCount := announcementEmailWorkerCount
	if len(recipients) < workerCount {
		workerCount = len(recipients)
	}

	var workers sync.WaitGroup
	for worker := 0; worker < workerCount; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for recipient := range jobs {
				outcomes <- s.dispatchAnnouncementEmailToRecipient(ctx, announcement, recipient, loginURL, contentHTML)
			}
		}()
	}
	for _, recipient := range recipients {
		jobs <- recipient
	}
	close(jobs)
	workers.Wait()
	close(outcomes)

	var infrastructureErr error
	for outcome := range outcomes {
		switch outcome.state {
		case AnnouncementEmailDeliveryStatusSent:
			result.Claimed++
			result.Sent++
		case AnnouncementEmailDeliveryStatusFailed:
			result.Claimed++
			result.Failed++
		case "already_sent":
			result.AlreadySent++
		case "claim_error", "finalize_error":
			if infrastructureErr == nil {
				infrastructureErr = outcome.err
			}
		}
	}
	if infrastructureErr != nil {
		return result, fmt.Errorf("persist announcement email delivery: %w", infrastructureErr)
	}
	return result, nil
}

func (s *AnnouncementService) AnnouncementEmailDeliverySummary(
	ctx context.Context,
	announcementID int64,
) (AnnouncementEmailDeliverySummary, error) {
	if s == nil || s.announcementRepo == nil || s.emailDeliveryRepo == nil {
		return AnnouncementEmailDeliverySummary{}, ErrAnnouncementEmailUnavailable
	}
	announcement, err := s.announcementRepo.GetByID(ctx, announcementID)
	if err != nil {
		return AnnouncementEmailDeliverySummary{}, err
	}
	if announcement.NotifyMode != AnnouncementNotifyModeEmail {
		return AnnouncementEmailDeliverySummary{}, ErrAnnouncementEmailNotEnabled
	}
	return s.emailDeliveryRepo.Summary(ctx, announcementID)
}

func validateAnnouncementEmailDispatchInput(input DispatchAnnouncementEmailInput) error {
	switch strings.ToLower(strings.TrimSpace(input.Scope)) {
	case AnnouncementEmailScopeAll:
		return nil
	case AnnouncementEmailScopeSelected:
		if len(input.UserIDs) == 0 {
			return ErrAnnouncementEmailSelectionRequired
		}
		if len(input.UserIDs) > announcementEmailSelectedUserLimit {
			return ErrAnnouncementEmailTooManyUsers
		}
		return nil
	default:
		return ErrAnnouncementEmailInvalidScope
	}
}

func (s *AnnouncementService) listAnnouncementEmailRecipients(
	ctx context.Context,
	input DispatchAnnouncementEmailInput,
) ([]User, int, error) {
	switch strings.ToLower(strings.TrimSpace(input.Scope)) {
	case AnnouncementEmailScopeAll:
		return s.listAllActiveAnnouncementEmailRecipients(ctx)
	case AnnouncementEmailScopeSelected:
		return s.listSelectedAnnouncementEmailRecipients(ctx, input.UserIDs)
	default:
		return nil, 0, ErrAnnouncementEmailInvalidScope
	}
}

func (s *AnnouncementService) listAllActiveAnnouncementEmailRecipients(ctx context.Context) ([]User, int, error) {
	includeSubscriptions := false
	filters := UserListFilters{
		Status:               StatusActive,
		IncludeSubscriptions: &includeSubscriptions,
	}
	recipients := make([]User, 0)
	skipped := 0
	page := 1
	for {
		items, result, err := s.userRepo.ListWithFilters(ctx, pagination.PaginationParams{
			Page:      page,
			PageSize:  announcementEmailRecipientPageSize,
			SortBy:    "id",
			SortOrder: pagination.SortOrderAsc,
		}, filters)
		if err != nil {
			return nil, 0, fmt.Errorf("list active users for announcement email: %w", err)
		}
		for _, user := range items {
			if strings.TrimSpace(user.Email) != "" {
				recipients = append(recipients, user)
			} else {
				skipped++
			}
		}
		if result == nil || page >= result.Pages || len(items) == 0 {
			break
		}
		page++
	}
	return recipients, skipped, nil
}

func (s *AnnouncementService) listSelectedAnnouncementEmailRecipients(ctx context.Context, userIDs []int64) ([]User, int, error) {
	uniqueIDs := uniquePositiveAnnouncementEmailUserIDs(userIDs)
	if len(uniqueIDs) == 0 {
		return nil, len(userIDs), ErrAnnouncementEmailSelectionRequired
	}
	if len(uniqueIDs) > announcementEmailSelectedUserLimit {
		return nil, 0, ErrAnnouncementEmailTooManyUsers
	}

	byID := make(map[int64]User, len(uniqueIDs))
	if batchRepo, ok := s.userRepo.(announcementEmailUserBatchRepository); ok {
		users, err := batchRepo.GetByIDs(ctx, uniqueIDs)
		if err != nil {
			return nil, 0, fmt.Errorf("load selected users for announcement email: %w", err)
		}
		for _, user := range users {
			if user != nil {
				byID[user.ID] = *user
			}
		}
	} else {
		for _, userID := range uniqueIDs {
			user, err := s.userRepo.GetByID(ctx, userID)
			if err == nil && user != nil {
				byID[user.ID] = *user
				continue
			}
			if err != nil && !errors.Is(err, ErrUserNotFound) {
				return nil, 0, fmt.Errorf("load selected user for announcement email: %w", err)
			}
		}
	}

	recipients := make([]User, 0, len(uniqueIDs))
	skipped := len(userIDs) - len(uniqueIDs)
	for _, userID := range uniqueIDs {
		user, ok := byID[userID]
		if !ok || !user.IsActive() || strings.TrimSpace(user.Email) == "" {
			skipped++
			continue
		}
		recipients = append(recipients, user)
	}
	return recipients, skipped, nil
}

func uniquePositiveAnnouncementEmailUserIDs(userIDs []int64) []int64 {
	unique := make([]int64, 0, len(userIDs))
	seen := make(map[int64]struct{}, len(userIDs))
	for _, userID := range userIDs {
		if userID <= 0 {
			continue
		}
		if _, exists := seen[userID]; exists {
			continue
		}
		seen[userID] = struct{}{}
		unique = append(unique, userID)
	}
	return unique
}

func (s *AnnouncementService) dispatchAnnouncementEmailToRecipient(
	ctx context.Context,
	announcement *Announcement,
	recipient User,
	loginURL string,
	contentHTML string,
) announcementEmailDispatchOutcome {
	now := time.Now().UTC()
	claimed, err := s.emailDeliveryRepo.Claim(ctx, AnnouncementEmailDelivery{
		AnnouncementID: announcement.ID,
		UserID:         recipient.ID,
		RecipientEmail: strings.TrimSpace(recipient.Email),
		Status:         AnnouncementEmailDeliveryStatusClaimed,
		AttemptedAt:    now,
	})
	if err != nil {
		return announcementEmailDispatchOutcome{state: "claim_error", err: err}
	}
	if !claimed {
		return announcementEmailDispatchOutcome{state: "already_sent"}
	}

	err = s.emailSender.Send(ctx, NotificationEmailSendInput{
		Event:          NotificationEmailEventAnnouncementPublished,
		RecipientEmail: recipient.Email,
		RecipientName:  announcementEmailRecipientName(recipient),
		UserID:         recipient.ID,
		SourceType:     "announcement",
		SourceID:       fmt.Sprintf("%d:%d", announcement.ID, recipient.ID),
		Variables: map[string]string{
			"announcement_title": announcement.Title,
			"login_url":          loginURL,
		},
		RawHTMLVariables: map[string]string{
			"announcement_content_html": contentHTML,
		},
	})
	if err != nil {
		if markErr := s.emailDeliveryRepo.MarkFailed(ctx, announcement.ID, recipient.ID, now, announcementEmailFailure(err)); markErr != nil {
			return announcementEmailDispatchOutcome{state: "finalize_error", err: markErr}
		}
		return announcementEmailDispatchOutcome{state: AnnouncementEmailDeliveryStatusFailed}
	}
	if err := s.emailDeliveryRepo.MarkSent(ctx, announcement.ID, recipient.ID, time.Now().UTC()); err != nil {
		return announcementEmailDispatchOutcome{state: "finalize_error", err: err}
	}
	return announcementEmailDispatchOutcome{state: AnnouncementEmailDeliveryStatusSent}
}

func announcementEmailFailure(err error) string {
	if err == nil {
		return ""
	}
	value := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(err.Error(), "\r", " "), "\n", " "))
	if len(value) > 500 {
		return value[:500]
	}
	return value
}

func announcementEmailRecipientName(user User) string {
	if name := strings.TrimSpace(user.Username); name != "" {
		return name
	}
	email := strings.TrimSpace(user.Email)
	if at := strings.Index(email, "@"); at > 0 {
		return email[:at]
	}
	return email
}

func renderAnnouncementEmailContent(content string) string {
	content = strings.TrimSpace(content)
	if len(content) > announcementEmailContentLimit {
		content = content[:announcementEmailContentLimit] + "\n\n..."
	}
	escaped := escapeAnnouncementEmailInlineHTML(strings.ReplaceAll(content, "\r\n", "\n"))
	sections := strings.Split(escaped, "\n\n")
	parts := make([]string, 0, len(sections))
	for _, rawSection := range sections {
		section := strings.TrimSpace(rawSection)
		if section == "" {
			continue
		}
		if strings.HasPrefix(section, "### ") {
			parts = append(parts, `<h3 style="margin: 22px 0 8px; font-size: 17px; line-height: 1.45;">`+renderAnnouncementEmailInlineMarkdown(strings.TrimSpace(section[4:]))+`</h3>`)
			continue
		}
		if strings.HasPrefix(section, "## ") {
			parts = append(parts, `<h2 style="margin: 24px 0 10px; font-size: 20px; line-height: 1.35;">`+renderAnnouncementEmailInlineMarkdown(strings.TrimSpace(section[3:]))+`</h2>`)
			continue
		}
		if strings.HasPrefix(section, "# ") {
			parts = append(parts, `<h2 style="margin: 24px 0 10px; font-size: 22px; line-height: 1.3;">`+renderAnnouncementEmailInlineMarkdown(strings.TrimSpace(section[2:]))+`</h2>`)
			continue
		}
		parts = append(parts, `<p style="margin: 0 0 16px;">`+renderAnnouncementEmailInlineMarkdown(strings.ReplaceAll(section, "\n", "<br>"))+`</p>`)
	}
	if len(parts) == 0 {
		return `<p style="margin: 0;">` + html.EscapeString(content) + `</p>`
	}
	return strings.Join(parts, "")
}

func renderAnnouncementEmailInlineMarkdown(value string) string {
	value = announcementEmailCodeMarkdown.ReplaceAllString(value, `<code style="border-radius: 4px; background: #e2e8f0; padding: 2px 5px; font-size: 0.92em;">$1</code>`)
	value = announcementEmailStrongMarkdown.ReplaceAllString(value, `<strong>$1</strong>`)
	return announcementEmailEmMarkdown.ReplaceAllString(value, `<em>$1</em>`)
}

type announcementEmailInlineTag struct {
	value   string
	name    string
	closing bool
	void    bool
}

// escapeAnnouncementEmailInlineHTML retains only the small inline subset the
// announcement editor historically uses for emphasis. Everything else stays
// escaped so a stored announcement cannot add links, remote images, scripts,
// event handlers, or arbitrary email-client markup.
func escapeAnnouncementEmailInlineHTML(content string) string {
	content = strings.ReplaceAll(content, "\x00", "")
	var (
		out      strings.Builder
		allowed  = make(map[string]string)
		tagID    int
		openTags []string
	)
	for cursor := 0; cursor < len(content); {
		open := strings.IndexByte(content[cursor:], '<')
		if open < 0 {
			_, _ = out.WriteString(content[cursor:])
			break
		}
		open += cursor
		_, _ = out.WriteString(content[cursor:open])
		close := strings.IndexByte(content[open:], '>')
		if close < 0 {
			_, _ = out.WriteString(content[open:])
			break
		}
		close += open
		candidate := content[open : close+1]
		if sanitized, ok := sanitizeAnnouncementEmailInlineTag(candidate); ok {
			if sanitized.closing {
				if len(openTags) == 0 || openTags[len(openTags)-1] != sanitized.name {
					out.WriteString(candidate)
					cursor = close + 1
					continue
				}
				openTags = openTags[:len(openTags)-1]
			} else if !sanitized.void {
				openTags = append(openTags, sanitized.name)
			}
			marker := fmt.Sprintf("\x00ANNOUNCEMENTTAG%d\x00", tagID)
			tagID++
			allowed[marker] = sanitized.value
			out.WriteString(marker)
		} else {
			out.WriteString(candidate)
		}
		cursor = close + 1
	}

	escaped := html.EscapeString(out.String())
	for marker, sanitized := range allowed {
		escaped = strings.ReplaceAll(escaped, marker, sanitized)
	}
	return escaped
}

func sanitizeAnnouncementEmailInlineTag(raw string) (announcementEmailInlineTag, bool) {
	match := announcementEmailHTMLTag.FindStringSubmatch(strings.TrimSpace(raw))
	if len(match) != 4 {
		return announcementEmailInlineTag{}, false
	}
	closing := match[1] == "/"
	name := strings.ToLower(strings.TrimSpace(match[2]))
	attributes := strings.TrimSpace(match[3])
	selfClosing := strings.HasSuffix(attributes, "/")
	if selfClosing {
		attributes = strings.TrimSpace(strings.TrimSuffix(attributes, "/"))
	}

	switch name {
	case "strong", "b", "em", "i":
		if attributes != "" || selfClosing {
			return announcementEmailInlineTag{}, false
		}
		if closing {
			return announcementEmailInlineTag{value: "</" + name + ">", name: name, closing: true}, true
		}
		return announcementEmailInlineTag{value: "<" + name + ">", name: name}, true
	case "br":
		if closing || attributes != "" {
			return announcementEmailInlineTag{}, false
		}
		return announcementEmailInlineTag{value: "<br>", name: name, void: true}, true
	case "span":
		if closing {
			if attributes != "" || selfClosing {
				return announcementEmailInlineTag{}, false
			}
			return announcementEmailInlineTag{value: "</span>", name: name, closing: true}, true
		}
		if selfClosing {
			return announcementEmailInlineTag{}, false
		}
		if attributes == "" {
			return announcementEmailInlineTag{value: "<span>", name: name}, true
		}
		color, ok := sanitizeAnnouncementEmailColorStyle(attributes)
		if !ok {
			return announcementEmailInlineTag{}, false
		}
		if color == "" {
			return announcementEmailInlineTag{value: "<span>", name: name}, true
		}
		return announcementEmailInlineTag{value: `<span style="color: ` + color + `">`, name: name}, true
	default:
		return announcementEmailInlineTag{}, false
	}
}

func sanitizeAnnouncementEmailColorStyle(attributes string) (string, bool) {
	match := announcementEmailStyleAttribute.FindStringSubmatch(strings.TrimSpace(attributes))
	if len(match) != 3 {
		return "", false
	}
	style := match[1]
	if style == "" {
		style = match[2]
	}
	color := ""
	for _, declaration := range strings.Split(style, ";") {
		declaration = strings.TrimSpace(declaration)
		if declaration == "" {
			continue
		}
		key, value, ok := strings.Cut(declaration, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "color") || color != "" {
			return "", false
		}
		value = strings.ToLower(strings.TrimSpace(value))
		if !announcementEmailHexColor.MatchString(value) {
			return "", false
		}
		color = value
	}
	return color, true
}
