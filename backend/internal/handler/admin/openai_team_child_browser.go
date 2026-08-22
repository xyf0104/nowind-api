package admin

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const (
	teamChildBrowserProxyPrefix = "/api/v1/team-child-browser"
	teamChildBrowserCookieName  = "xiass_team_child_browser"
	teamChildBrowserDefaultTTL  = 12 * time.Hour
	teamChildBrowserTicketTTL   = 3 * time.Minute

	teamChildBrowserSessionRedisPrefix = "xiass:team-child:browser:session:"
	teamChildBrowserTicketRedisPrefix  = "xiass:team-child:browser:ticket:"
)

type openAITeamBrowserStore struct {
	mu       sync.Mutex
	sessions map[string]openAITeamBrowserSession
	tickets  map[string]openAITeamBrowserTicket
	redis    *redis.Client
	now      func() time.Time
}

type openAITeamBrowserSession struct {
	adminUserID int64
	upstreamURL *url.URL
	expiresAt   time.Time
}

type openAITeamBrowserTicket struct {
	sessionToken string
	expiresAt    time.Time
}

type persistedOpenAITeamBrowserSession struct {
	AdminUserID int64     `json:"admin_user_id"`
	UpstreamURL string    `json:"upstream_url"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type teamChildBrowserConfig struct {
	upstreamURL *url.URL
	ttl         time.Duration
	ticketTTL   time.Duration
}

type teamChildBrowserSessionResponse struct {
	EmbedURL        string `json:"embed_url"`
	ExpiresAt       string `json:"expires_at"`
	TicketExpiresAt string `json:"ticket_expires_at"`
}

func newOpenAITeamBrowserStore() *openAITeamBrowserStore {
	return &openAITeamBrowserStore{
		sessions: make(map[string]openAITeamBrowserSession),
		tickets:  make(map[string]openAITeamBrowserTicket),
		now:      time.Now,
	}
}

// ConfigureTeamChildSessionStore upgrades the short-lived Team workflow state
// to Redis in real deployments. The in-memory implementation remains only for
// direct unit tests and isolated handler construction.
func (h *OpenAIOAuthHandler) ConfigureTeamChildSessionStore(redisClient *redis.Client) {
	if h == nil {
		return
	}
	if h.teamMailboxStore == nil {
		h.teamMailboxStore = newOpenAITeamMailboxStore()
	}
	if h.teamBrowserStore == nil {
		h.teamBrowserStore = newOpenAITeamBrowserStore()
	}
	h.teamMailboxStore.configureRedis(redisClient)
	h.teamBrowserStore.configureRedis(redisClient)
}

func (s *openAITeamBrowserStore) configureRedis(redisClient *redis.Client) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.redis = redisClient
	s.mu.Unlock()
}

// CreateTeamChildBrowserSession mints a one-time iframe bootstrap URL for the
// configured internal Chromium workspace. The bootstrap ticket is consumed by
// the browser proxy and replaced by an HttpOnly cookie before any browser UI is
// served, so the administrator JWT never needs to appear in an iframe request.
// POST /api/v1/admin/openai/team-child/browser-sessions
func (h *OpenAIOAuthHandler) CreateTeamChildBrowserSession(c *gin.Context) {
	if h == nil || h.teamBrowserStore == nil {
		response.InternalError(c, "team child browser service is unavailable")
		return
	}
	config, err := loadTeamChildBrowserConfig()
	if err != nil {
		response.BadRequest(c, "Team child browser is not configured")
		return
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "administrator session is required")
		return
	}

	cookieToken, err := newTeamChildBrowserToken()
	if err != nil {
		response.InternalError(c, "browser workspace session could not be created")
		return
	}
	ticket, err := newTeamChildBrowserToken()
	if err != nil {
		response.InternalError(c, "browser workspace session could not be created")
		return
	}
	now := h.teamBrowserStore.now()
	expiresAt := now.Add(config.ttl)
	ticketExpiresAt := now.Add(config.ticketTTL)
	session := openAITeamBrowserSession{
		adminUserID: subject.UserID,
		upstreamURL: config.upstreamURL,
		expiresAt:   expiresAt,
	}
	if err := h.teamBrowserStore.create(c.Request.Context(), cookieToken, ticket, session, config.ticketTTL); err != nil {
		response.InternalError(c, "browser workspace session could not be stored")
		return
	}

	response.Success(c, teamChildBrowserSessionResponse{
		EmbedURL:        teamChildBrowserProxyPrefix + "/?ticket=" + url.QueryEscape(ticket),
		ExpiresAt:       expiresAt.UTC().Format(time.RFC3339),
		TicketExpiresAt: ticketExpiresAt.UTC().Format(time.RFC3339),
	})
}

// ServeTeamChildBrowser is intentionally outside the normal admin group: a
// browser iframe cannot attach XIASS's Authorization header. It accepts only a
// short-lived, administrator-minted bootstrap ticket or its HttpOnly session
// cookie and proxies exclusively to the configured internal Chromium service.
// GET /api/v1/team-child-browser/*path
func (h *OpenAIOAuthHandler) ServeTeamChildBrowser(c *gin.Context) {
	if h == nil || h.teamBrowserStore == nil {
		http.Error(c.Writer, "Browser workspace is unavailable", http.StatusServiceUnavailable)
		return
	}
	if ticket := strings.TrimSpace(c.Query("ticket")); ticket != "" {
		sessionToken, session, ok, err := h.consumeTeamChildBrowserTicket(c.Request.Context(), ticket)
		if err != nil {
			http.Error(c.Writer, "Browser workspace is temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		if !ok {
			http.Error(c.Writer, "Browser workspace link has expired", http.StatusUnauthorized)
			return
		}
		if allowed, verifyErr := h.canUseTeamChildBrowserSession(c.Request.Context(), session); verifyErr != nil {
			_ = h.teamBrowserStore.delete(c.Request.Context(), sessionToken)
			http.Error(c.Writer, "Browser workspace is temporarily unavailable", http.StatusServiceUnavailable)
			return
		} else if !allowed {
			_ = h.teamBrowserStore.delete(c.Request.Context(), sessionToken)
			clearTeamChildBrowserCookie(c)
			http.Error(c.Writer, "Browser workspace authorization has expired", http.StatusUnauthorized)
			return
		}
		setTeamChildBrowserCookie(c, sessionToken, session.expiresAt)
		c.Header("Cache-Control", "private, no-store, max-age=0")
		c.Header("Pragma", "no-cache")
		c.Header("Referrer-Policy", "no-referrer")
		c.Redirect(http.StatusFound, teamChildBrowserProxyPrefix+"/")
		return
	}

	sessionToken, err := c.Cookie(teamChildBrowserCookieName)
	if err != nil || strings.TrimSpace(sessionToken) == "" {
		http.Error(c.Writer, "Browser workspace login is required", http.StatusUnauthorized)
		return
	}
	session, ok, lookupErr := h.lookupTeamChildBrowserSession(c.Request.Context(), sessionToken)
	if lookupErr != nil {
		http.Error(c.Writer, "Browser workspace is temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	if !ok {
		clearTeamChildBrowserCookie(c)
		http.Error(c.Writer, "Browser workspace session has expired", http.StatusUnauthorized)
		return
	}
	if allowed, verifyErr := h.canUseTeamChildBrowserSession(c.Request.Context(), session); verifyErr != nil {
		http.Error(c.Writer, "Browser workspace is temporarily unavailable", http.StatusServiceUnavailable)
		return
	} else if !allowed {
		_ = h.teamBrowserStore.delete(c.Request.Context(), sessionToken)
		clearTeamChildBrowserCookie(c)
		http.Error(c.Writer, "Browser workspace authorization has expired", http.StatusUnauthorized)
		return
	}

	c.Header("Referrer-Policy", "no-referrer")
	newTeamChildBrowserReverseProxy(session.upstreamURL, c).ServeHTTP(c.Writer, c.Request)
}

func (h *OpenAIOAuthHandler) canUseTeamChildBrowserSession(ctx context.Context, session openAITeamBrowserSession) (bool, error) {
	if h == nil || session.adminUserID <= 0 {
		return false, nil
	}
	// Direct handler unit tests do not construct the full admin service. In the
	// actual application this capability is always injected by Wire and the
	// route has already passed the admin middleware.
	if h.adminService == nil {
		return true, nil
	}
	user, err := h.adminService.GetUser(ctx, session.adminUserID)
	if err != nil {
		return false, err
	}
	return user != nil && user.IsAdmin() && user.IsActive(), nil
}

func loadTeamChildBrowserConfig() (teamChildBrowserConfig, error) {
	enabled, err := strconv.ParseBool(strings.TrimSpace(os.Getenv("TEAM_CHILD_BROWSER_ENABLED")))
	if err != nil && strings.TrimSpace(os.Getenv("TEAM_CHILD_BROWSER_ENABLED")) != "" {
		return teamChildBrowserConfig{}, fmt.Errorf("TEAM_CHILD_BROWSER_ENABLED is invalid")
	}
	if !enabled {
		return teamChildBrowserConfig{}, fmt.Errorf("TEAM_CHILD_BROWSER_ENABLED is disabled")
	}

	upstreamRaw := strings.TrimSpace(os.Getenv("TEAM_CHILD_BROWSER_UPSTREAM_URL"))
	if upstreamRaw == "" {
		return teamChildBrowserConfig{}, fmt.Errorf("TEAM_CHILD_BROWSER_UPSTREAM_URL is required")
	}
	upstreamURL, err := url.Parse(upstreamRaw)
	if err != nil || upstreamURL.Scheme == "" || upstreamURL.Host == "" ||
		(upstreamURL.Scheme != "http" && upstreamURL.Scheme != "https") ||
		(upstreamURL.Path != "" && upstreamURL.Path != "/") ||
		upstreamURL.RawQuery != "" || upstreamURL.Fragment != "" {
		return teamChildBrowserConfig{}, fmt.Errorf("TEAM_CHILD_BROWSER_UPSTREAM_URL is invalid")
	}

	ttl := teamChildBrowserDefaultTTL
	if rawTTL := strings.TrimSpace(os.Getenv("TEAM_CHILD_BROWSER_SESSION_TTL_MINUTES")); rawTTL != "" {
		minutes, parseErr := strconv.Atoi(rawTTL)
		if parseErr != nil || minutes < 5 || minutes > 1440 {
			return teamChildBrowserConfig{}, fmt.Errorf("TEAM_CHILD_BROWSER_SESSION_TTL_MINUTES must be between 5 and 1440")
		}
		ttl = time.Duration(minutes) * time.Minute
	}
	ticketTTL := teamChildBrowserTicketTTL
	if rawTicketTTL := strings.TrimSpace(os.Getenv("TEAM_CHILD_BROWSER_TICKET_TTL_SECONDS")); rawTicketTTL != "" {
		seconds, parseErr := strconv.Atoi(rawTicketTTL)
		if parseErr != nil || seconds < 60 || seconds > 600 {
			return teamChildBrowserConfig{}, fmt.Errorf("TEAM_CHILD_BROWSER_TICKET_TTL_SECONDS must be between 60 and 600")
		}
		ticketTTL = time.Duration(seconds) * time.Second
	}
	return teamChildBrowserConfig{upstreamURL: upstreamURL, ttl: ttl, ticketTTL: ticketTTL}, nil
}

func newTeamChildBrowserToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func (s *openAITeamBrowserStore) pruneLocked(now time.Time) {
	for token, session := range s.sessions {
		if !session.expiresAt.After(now) {
			delete(s.sessions, token)
		}
	}
	for ticket, ticketSession := range s.tickets {
		if !ticketSession.expiresAt.After(now) {
			delete(s.tickets, ticket)
			continue
		}
		if _, ok := s.sessions[ticketSession.sessionToken]; !ok {
			delete(s.tickets, ticket)
		}
	}
}

func (s *openAITeamBrowserStore) create(ctx context.Context, sessionToken, ticket string, session openAITeamBrowserSession, ticketTTL time.Duration) error {
	if s == nil || strings.TrimSpace(sessionToken) == "" || strings.TrimSpace(ticket) == "" || session.upstreamURL == nil || session.expiresAt.IsZero() || ticketTTL <= 0 {
		return errors.New("browser workspace session is invalid")
	}
	if redisClient := s.redisClient(); redisClient != nil {
		payload, err := encodePersistedOpenAITeamBrowserSession(session)
		if err != nil {
			return err
		}
		sessionTTL := time.Until(session.expiresAt)
		if sessionTTL <= 0 {
			return errors.New("browser workspace session has expired")
		}
		_, err = redisClient.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, teamChildBrowserSessionRedisPrefix+sessionToken, payload, sessionTTL)
			pipe.Set(ctx, teamChildBrowserTicketRedisPrefix+ticket, sessionToken, ticketTTL)
			return nil
		})
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.pruneLocked(now)
	s.sessions[sessionToken] = session
	s.tickets[ticket] = openAITeamBrowserTicket{sessionToken: sessionToken, expiresAt: now.Add(ticketTTL)}
	return nil
}

func (h *OpenAIOAuthHandler) consumeTeamChildBrowserTicket(ctx context.Context, ticket string) (string, openAITeamBrowserSession, bool, error) {
	if h == nil || h.teamBrowserStore == nil {
		return "", openAITeamBrowserSession{}, false, errors.New("browser workspace store is unavailable")
	}
	return h.teamBrowserStore.consumeTicket(ctx, ticket)
}

func (s *openAITeamBrowserStore) consumeTicket(ctx context.Context, ticket string) (string, openAITeamBrowserSession, bool, error) {
	ticket = strings.TrimSpace(ticket)
	if s == nil || ticket == "" || len(ticket) > 256 {
		return "", openAITeamBrowserSession{}, false, nil
	}
	if redisClient := s.redisClient(); redisClient != nil {
		sessionToken, err := redisClient.GetDel(ctx, teamChildBrowserTicketRedisPrefix+ticket).Result()
		if errors.Is(err, redis.Nil) {
			return "", openAITeamBrowserSession{}, false, nil
		}
		if err != nil {
			return "", openAITeamBrowserSession{}, false, err
		}
		session, ok, err := s.lookup(ctx, sessionToken)
		return sessionToken, session, ok, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.pruneLocked(now)
	ticketSession, ok := s.tickets[ticket]
	if !ok {
		return "", openAITeamBrowserSession{}, false, nil
	}
	delete(s.tickets, ticket)
	session, ok := s.sessions[ticketSession.sessionToken]
	return ticketSession.sessionToken, session, ok && session.expiresAt.After(now), nil
}

func (h *OpenAIOAuthHandler) lookupTeamChildBrowserSession(ctx context.Context, token string) (openAITeamBrowserSession, bool, error) {
	if h == nil || h.teamBrowserStore == nil {
		return openAITeamBrowserSession{}, false, errors.New("browser workspace store is unavailable")
	}
	return h.teamBrowserStore.lookup(ctx, token)
}

func (s *openAITeamBrowserStore) lookup(ctx context.Context, token string) (openAITeamBrowserSession, bool, error) {
	token = strings.TrimSpace(token)
	if s == nil || token == "" || len(token) > 256 {
		return openAITeamBrowserSession{}, false, nil
	}
	if redisClient := s.redisClient(); redisClient != nil {
		payload, err := redisClient.Get(ctx, teamChildBrowserSessionRedisPrefix+token).Bytes()
		if errors.Is(err, redis.Nil) {
			return openAITeamBrowserSession{}, false, nil
		}
		if err != nil {
			return openAITeamBrowserSession{}, false, err
		}
		session, err := decodePersistedOpenAITeamBrowserSession(payload)
		if err != nil {
			_ = redisClient.Del(ctx, teamChildBrowserSessionRedisPrefix+token).Err()
			return openAITeamBrowserSession{}, false, err
		}
		if !session.expiresAt.After(s.now()) {
			_ = redisClient.Del(ctx, teamChildBrowserSessionRedisPrefix+token).Err()
			return openAITeamBrowserSession{}, false, nil
		}
		return session, true, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.pruneLocked(now)
	session, ok := s.sessions[token]
	return session, ok && session.expiresAt.After(now), nil
}

func (s *openAITeamBrowserStore) delete(ctx context.Context, token string) error {
	token = strings.TrimSpace(token)
	if s == nil || token == "" || len(token) > 256 {
		return nil
	}
	if redisClient := s.redisClient(); redisClient != nil {
		return redisClient.Del(ctx, teamChildBrowserSessionRedisPrefix+token).Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, token)
	s.pruneLocked(s.now())
	return nil
}

func (s *openAITeamBrowserStore) redisClient() *redis.Client {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.redis
}

func encodePersistedOpenAITeamBrowserSession(session openAITeamBrowserSession) ([]byte, error) {
	if session.upstreamURL == nil || session.adminUserID <= 0 || session.expiresAt.IsZero() {
		return nil, errors.New("browser workspace session is invalid")
	}
	return json.Marshal(persistedOpenAITeamBrowserSession{
		AdminUserID: session.adminUserID,
		UpstreamURL: session.upstreamURL.String(),
		ExpiresAt:   session.expiresAt.UTC(),
	})
}

func decodePersistedOpenAITeamBrowserSession(payload []byte) (openAITeamBrowserSession, error) {
	var persisted persistedOpenAITeamBrowserSession
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return openAITeamBrowserSession{}, fmt.Errorf("decode browser workspace session: %w", err)
	}
	upstreamURL, err := url.Parse(persisted.UpstreamURL)
	if err != nil || persisted.AdminUserID <= 0 || upstreamURL.Scheme == "" || upstreamURL.Host == "" || persisted.ExpiresAt.IsZero() {
		return openAITeamBrowserSession{}, errors.New("browser workspace session is invalid")
	}
	return openAITeamBrowserSession{
		adminUserID: persisted.AdminUserID,
		upstreamURL: upstreamURL,
		expiresAt:   persisted.ExpiresAt,
	}, nil
}

func setTeamChildBrowserCookie(c *gin.Context, token string, expiresAt time.Time) {
	maxAge := int(time.Until(expiresAt).Seconds())
	if maxAge < 1 {
		maxAge = 1
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     teamChildBrowserCookieName,
		Value:    token,
		Path:     teamChildBrowserProxyPrefix,
		MaxAge:   maxAge,
		Expires:  expiresAt.UTC(),
		HttpOnly: true,
		Secure:   teamChildBrowserRequestIsSecure(c),
		SameSite: http.SameSiteStrictMode,
	})
}

func clearTeamChildBrowserCookie(c *gin.Context) {
	if c == nil {
		return
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     teamChildBrowserCookieName,
		Value:    "",
		Path:     teamChildBrowserProxyPrefix,
		MaxAge:   -1,
		Expires:  time.Unix(1, 0).UTC(),
		HttpOnly: true,
		Secure:   teamChildBrowserRequestIsSecure(c),
		SameSite: http.SameSiteStrictMode,
	})
}

func teamChildBrowserRequestIsSecure(c *gin.Context) bool {
	if c == nil || c.Request == nil {
		return false
	}
	if c.Request.TLS != nil {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(c.GetHeader("X-Forwarded-Proto")), "https")
}

func newTeamChildBrowserReverseProxy(upstreamURL *url.URL, c *gin.Context) *httputil.ReverseProxy {
	proxy := httputil.NewSingleHostReverseProxy(upstreamURL)
	if upstreamURL.Scheme == "https" && strings.EqualFold(upstreamURL.Hostname(), "team-child-browser") {
		// The internal LinuxServer Chromium service presents its own self-signed
		// certificate. This exception is deliberately scoped to the fixed Docker
		// service name; all other configured HTTPS upstreams keep normal checks.
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.TLSClientConfig = &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: true, // #nosec G402 -- Docker-internal self-signed Chromium only.
		}
		proxy.Transport = transport
	}
	director := proxy.Director
	proxy.Director = func(request *http.Request) {
		director(request)
		request.Host = upstreamURL.Host
		// XIASS tokens and browser-workspace cookies must never be forwarded to
		// the desktop container. The Chromium profile has its own stored cookies.
		request.Header.Del("Authorization")
		request.Header.Del("X-API-Key")
		request.Header.Del("Cookie")
		request.Header.Set("X-Forwarded-Prefix", teamChildBrowserProxyPrefix)
		request.Header.Set("X-Forwarded-Host", c.Request.Host)
		if teamChildBrowserRequestIsSecure(c) {
			request.Header.Set("X-Forwarded-Proto", "https")
		} else {
			request.Header.Set("X-Forwarded-Proto", "http")
		}
	}
	proxy.ModifyResponse = func(upstreamResponse *http.Response) error {
		upstreamResponse.Header.Del("X-Frame-Options")
		removeTeamChildBrowserFrameAncestors(upstreamResponse.Header)
		upstreamResponse.Header.Set("Cache-Control", "private, no-store, max-age=0")
		return nil
	}
	proxy.ErrorHandler = func(writer http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(writer, "Browser workspace is unreachable", http.StatusBadGateway)
	}
	return proxy
}

func removeTeamChildBrowserFrameAncestors(headers http.Header) {
	values := headers.Values("Content-Security-Policy")
	if len(values) == 0 {
		return
	}
	headers.Del("Content-Security-Policy")
	for _, value := range values {
		parts := strings.Split(value, ";")
		kept := make([]string, 0, len(parts))
		for _, part := range parts {
			if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(part)), "frame-ancestors") {
				kept = append(kept, strings.TrimSpace(part))
			}
		}
		if sanitized := strings.Join(kept, "; "); sanitized != "" {
			headers.Add("Content-Security-Policy", sanitized)
		}
	}
}
