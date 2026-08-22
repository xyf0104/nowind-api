package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"

	"github.com/gin-gonic/gin"
)

// RegisterTeamChildBrowserRoutes mounts the browser iframe proxy outside the
// admin-header middleware. Access is instead established by a short-lived,
// administrator-minted HttpOnly cookie in the handler itself.
func RegisterTeamChildBrowserRoutes(v1 *gin.RouterGroup, h *handler.Handlers) {
	browser := v1.Group("/team-child-browser")
	browser.GET("/*path", h.Admin.OpenAIOAuth.ServeTeamChildBrowser)
}
