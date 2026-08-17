package routes

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	basemiddleware "github.com/Wei-Shaw/sub2api/internal/middleware"
	ippkg "github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// RegisterToolRoutes registers public utility endpoints. The OpenAI OAuth
// helper is intentionally public and free, but bounded by fail-closed Redis
// rate limits because it performs outbound token exchanges.
func RegisterToolRoutes(
	v1 *gin.RouterGroup,
	h *handler.Handlers,
	redisClient *redis.Client,
	panelRateLimiter *servermiddleware.PanelRateLimiter,
) {
	h.Admin.OpenAIOAuth.ConfigurePublicToolSessionStore(repository.NewRedisPublicOpenAIOAuthSessionStore(redisClient))
	tools := v1.Group("/tools")

	rateLimiter := basemiddleware.NewRateLimiter(redisClient)
	strict := basemiddleware.RateLimitOptions{FailureMode: basemiddleware.RateLimitFailClose}
	openaiOAuth := tools.Group("/openai-oauth")
	openaiOAuth.Use(publicOpenAIOAuthSecurity())
	openaiOAuth.Use(panelRateLimiter.PublicIP())
	{
		openaiOAuth.POST(
			"/authorize",
			rateLimiter.LimitWithOptions("public-openai-oauth-authorize", 5, time.Minute, strict),
			rateLimiter.LimitWithOptions("public-openai-oauth-authorize-hourly", 30, time.Hour, strict),
			h.Admin.OpenAIOAuth.GeneratePublicToolAuthURL,
		)
		openaiOAuth.POST(
			"/exchange",
			rateLimiter.LimitWithOptions("public-openai-oauth-exchange", 10, time.Minute, strict),
			h.Admin.OpenAIOAuth.ExchangePublicToolCode,
		)
	}
}

// publicOpenAIOAuthSecurity runs before every public OAuth limiter and handler.
// It keeps early errors private and forces security-sensitive IP consumers onto
// Gin's configured trusted-proxy chain instead of the legacy raw-header mode.
func publicOpenAIOAuthSecurity() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "private, no-store, max-age=0")
		c.Header("Pragma", "no-cache")
		c.Header("Expires", "0")
		c.Header("Referrer-Policy", "no-referrer")

		trustedClientIP := ippkg.GetTrustedClientIP(c)
		ippkg.SetForwardedIPSettings(c, false, nil)
		binding := &service.SessionBinding{IP: trustedClientIP, UserAgent: c.Request.UserAgent()}
		if existing := service.SessionBindingFromContext(c.Request.Context()); existing != nil {
			binding.UserAgent = existing.UserAgent
		}
		c.Request = c.Request.WithContext(service.WithSessionBinding(c.Request.Context(), binding))
		c.Next()
	}
}
