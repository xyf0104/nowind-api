package admin

import (
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/gin-gonic/gin"
)

// GetBillingBreakdown returns account billing grouped by user or, when user_id is set, model.
// GET /api/v1/admin/accounts/:id/billing-breakdown
func (h *AccountHandler) GetBillingBreakdown(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	var userID *int64
	if rawUserID := strings.TrimSpace(c.Query("user_id")); rawUserID != "" {
		parsed, parseErr := strconv.ParseInt(rawUserID, 10, 64)
		if parseErr != nil || parsed <= 0 {
			response.BadRequest(c, "Invalid user_id")
			return
		}
		userID = &parsed
	}

	startTime, endTime, rangeTimezone, err := parseAccountBillingRange(c)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	breakdown, err := h.accountUsageService.GetAccountBillingBreakdown(
		c.Request.Context(),
		accountID,
		startTime,
		endTime,
		userID,
		rangeTimezone,
	)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, breakdown)
}

func parseAccountBillingRange(c *gin.Context) (time.Time, time.Time, string, error) {
	startTimeRaw := strings.TrimSpace(c.Query("start_time"))
	endTimeRaw := strings.TrimSpace(c.Query("end_time"))
	if startTimeRaw != "" || endTimeRaw != "" {
		if startTimeRaw == "" || endTimeRaw == "" {
			return time.Time{}, time.Time{}, "", billingRangeError("start_time and end_time must be provided together")
		}
		startTime, err := time.Parse(time.RFC3339, startTimeRaw)
		if err != nil {
			return time.Time{}, time.Time{}, "", billingRangeError("Invalid start_time format, use RFC3339")
		}
		endTime, err := time.Parse(time.RFC3339, endTimeRaw)
		if err != nil {
			return time.Time{}, time.Time{}, "", billingRangeError("Invalid end_time format, use RFC3339")
		}
		if !startTime.Before(endTime) {
			return time.Time{}, time.Time{}, "", billingRangeError("start_time must be before end_time")
		}
		return startTime, endTime, accountBillingTimezone(c.Query("timezone")), nil
	}

	startDate := strings.TrimSpace(c.Query("start_date"))
	endDate := strings.TrimSpace(c.Query("end_date"))
	if startDate == "" || endDate == "" {
		return time.Time{}, time.Time{}, "", billingRangeError("start_time/end_time or start_date/end_date are required")
	}
	userTimezone := strings.TrimSpace(c.Query("timezone"))
	startTime, err := timezone.ParseInUserLocation("2006-01-02", startDate, userTimezone)
	if err != nil {
		return time.Time{}, time.Time{}, "", billingRangeError("Invalid start_date format, use YYYY-MM-DD")
	}
	endTime, err := timezone.ParseInUserLocation("2006-01-02", endDate, userTimezone)
	if err != nil {
		return time.Time{}, time.Time{}, "", billingRangeError("Invalid end_date format, use YYYY-MM-DD")
	}
	endTime = endTime.AddDate(0, 0, 1)
	if !startTime.Before(endTime) {
		return time.Time{}, time.Time{}, "", billingRangeError("start_date must not be after end_date")
	}
	return startTime, endTime, accountBillingTimezone(userTimezone), nil
}

type billingRangeError string

func (e billingRangeError) Error() string { return string(e) }

func accountBillingTimezone(userTimezone string) string {
	userTimezone = strings.TrimSpace(userTimezone)
	if userTimezone != "" {
		if _, err := time.LoadLocation(userTimezone); err == nil {
			return userTimezone
		}
	}
	return timezone.Name()
}
