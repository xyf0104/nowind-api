package handler

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// SMSReceiverHandler exposes the paid receiving flow to authenticated members.
// It intentionally has no card-key management endpoint: operator keys remain
// encrypted and administratively managed under /admin/settings/sms-receiver.
type SMSReceiverHandler struct {
	service *service.PixlabSMSService
}

func NewSMSReceiverHandler(svc *service.PixlabSMSService) *SMSReceiverHandler {
	return &SMSReceiverHandler{service: svc}
}

func (h *SMSReceiverHandler) serviceOrError(c *gin.Context) *service.PixlabSMSService {
	if h == nil || h.service == nil {
		response.Error(c, 503, "接码服务尚未初始化")
		return nil
	}
	return h.service
}

func smsMemberID(c *gin.Context) (int64, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	return subject.UserID, ok && subject.UserID > 0
}

func (h *SMSReceiverHandler) GetStatus(c *gin.Context) {
	svc := h.serviceOrError(c)
	if svc == nil {
		return
	}
	userID, ok := smsMemberID(c)
	if !ok {
		response.Error(c, 401, "无法识别当前用户")
		return
	}
	result, err := svc.MemberStatus(c.Request.Context(), userID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *SMSReceiverHandler) Redeem(c *gin.Context) {
	h.withAction(c, "redeem")
}

func (h *SMSReceiverHandler) Resume(c *gin.Context) {
	h.withAction(c, "resume")
}

func (h *SMSReceiverHandler) Check(c *gin.Context) {
	h.withAction(c, "check")
}

func (h *SMSReceiverHandler) Change(c *gin.Context) {
	h.withAction(c, "change")
}

func (h *SMSReceiverHandler) Cancel(c *gin.Context) {
	h.withAction(c, "cancel")
}

func (h *SMSReceiverHandler) withAction(c *gin.Context, action string) {
	svc := h.serviceOrError(c)
	if svc == nil {
		return
	}
	userID, ok := smsMemberID(c)
	if !ok {
		response.Error(c, 401, "无法识别当前用户")
		return
	}

	var (
		result *service.PixlabSMSMemberResult
		err    error
	)
	sessionID := strings.TrimSpace(c.Param("session_id"))
	switch action {
	case "redeem":
		result, err = svc.RedeemForMember(c.Request.Context(), userID)
	case "resume":
		result, err = svc.ResumeForMember(c.Request.Context(), userID, sessionID)
	case "check":
		result, err = svc.CheckForMember(c.Request.Context(), userID, sessionID)
	case "change":
		result, err = svc.ChangeNumberForMember(c.Request.Context(), userID, sessionID)
	case "cancel":
		result, err = svc.CancelForMember(c.Request.Context(), userID, sessionID)
	}
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}
