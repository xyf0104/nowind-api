package admin

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type pixlabSMSCardKeysRequest struct {
	CardKeys string `json:"card_keys" binding:"required,max=1048576"`
}

func (h *SettingHandler) pixlabSMSServiceOrError(c *gin.Context) *service.PixlabSMSService {
	if h.pixlabSMSService == nil {
		response.Error(c, 503, "接码服务尚未初始化")
		return nil
	}
	return h.pixlabSMSService
}

func pixlabSMSOwnerID(c *gin.Context) (int64, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	return subject.UserID, ok && subject.UserID > 0
}

// GetPixlabSMSStatus returns only server-side queue counters.
func (h *SettingHandler) GetPixlabSMSStatus(c *gin.Context) {
	svc := h.pixlabSMSServiceOrError(c)
	if svc == nil {
		return
	}
	ownerID, _ := pixlabSMSOwnerID(c)
	status, err := svc.QueueStatus(c.Request.Context(), ownerID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, status)
}

// AddPixlabSMSCardKeys accepts raw keys once, encrypts them at rest, and never
// returns them to the caller. The route is excluded from request-body auditing.
func (h *SettingHandler) AddPixlabSMSCardKeys(c *gin.Context) {
	svc := h.pixlabSMSServiceOrError(c)
	if svc == nil {
		return
	}
	var req pixlabSMSCardKeysRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	added, status, err := svc.AddCardKeys(c.Request.Context(), req.CardKeys)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{
		"added_count":  added,
		"queued_count": status.QueuedCount,
	})
}

func (h *SettingHandler) ClearPixlabSMSCardKeys(c *gin.Context) {
	svc := h.pixlabSMSServiceOrError(c)
	if svc == nil {
		return
	}
	deleted, status, err := svc.ClearQueuedCardKeys(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{
		"deleted_count": deleted,
		"queued_count":  status.QueuedCount,
	})
}

func (h *SettingHandler) RedeemPixlabSMSNumber(c *gin.Context) {
	svc := h.pixlabSMSServiceOrError(c)
	if svc == nil {
		return
	}
	ownerID, ok := pixlabSMSOwnerID(c)
	if !ok {
		response.Error(c, 401, "无法识别当前管理员")
		return
	}
	result, err := svc.Redeem(c.Request.Context(), ownerID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *SettingHandler) ResumePixlabSMSNumber(c *gin.Context) {
	h.withPixlabSMSAction(c, "resume")
}

func (h *SettingHandler) CheckPixlabSMSNumber(c *gin.Context) {
	h.withPixlabSMSAction(c, "check")
}

func (h *SettingHandler) ChangePixlabSMSNumber(c *gin.Context) {
	h.withPixlabSMSAction(c, "change")
}

func (h *SettingHandler) CancelPixlabSMSNumber(c *gin.Context) {
	h.withPixlabSMSAction(c, "cancel")
}

func (h *SettingHandler) withPixlabSMSAction(c *gin.Context, action string) {
	svc := h.pixlabSMSServiceOrError(c)
	if svc == nil {
		return
	}
	ownerID, ok := pixlabSMSOwnerID(c)
	if !ok {
		response.Error(c, 401, "无法识别当前管理员")
		return
	}
	sessionID := strings.TrimSpace(c.Param("session_id"))
	var (
		result *service.PixlabSMSResult
		err    error
	)
	switch action {
	case "resume":
		result, err = svc.Resume(c.Request.Context(), ownerID, sessionID)
	case "check":
		result, err = svc.Check(c.Request.Context(), ownerID, sessionID)
	case "change":
		result, err = svc.ChangeNumber(c.Request.Context(), ownerID, sessionID)
	case "cancel":
		result, err = svc.Cancel(c.Request.Context(), ownerID, sessionID)
	}
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}
