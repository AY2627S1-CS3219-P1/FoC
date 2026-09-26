package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/auth/tokenclaims"
	"github.com/MicahParks/jwkset"
	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

// AccessClaims are the verified claims available to protected handlers.
type AccessClaims struct {
	Subject   string
	SessionID string
	Role      string
	IssuedAt  time.Time
	ExpiresAt time.Time
	TokenID   string
}

type AuthConfig struct {
	JWKSURL  string
	Issuer   string
	Audience string
	Client   *http.Client
}

const (
	TokenIssuer      = "foc-user-service"
	TokenAudience    = "foc-services"
	keyCacheLifetime = time.Hour
)

type keyCache struct {
	keys    keyfunc.Keyfunc
	expires time.Time
}

// Authenticator owns the public-key cache for one service instance.
type Authenticator struct {
	config    AuthConfig
	client    *http.Client
	cache     atomic.Pointer[keyCache]
	refreshMu sync.Mutex
	lastMiss  time.Time
}

// NewAuthenticator fetches the initial key set before protected routes are served.
func NewAuthenticator(ctx context.Context, config AuthConfig) (*Authenticator, error) {
	parsed, err := url.Parse(config.JWKSURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") ||
		config.Issuer == "" || config.Audience == "" {
		return nil, errors.New("invalid authenticator configuration")
	}
	if parsed.Scheme == "http" && os.Getenv("APP_ENV") != "local" {
		return nil, errors.New("JWKS URL must use HTTPS outside local mode")
	}
	client := config.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	clientCopy := *client
	clientCopy.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	a := &Authenticator{config: config, client: &clientCopy}
	cache, err := a.fetch(ctx)
	if err != nil {
		return nil, fmt.Errorf("load authentication keys: %w", err)
	}
	a.cache.Store(cache)
	return a, nil
}

// NewUserServiceAuthenticator reads USER_SERVICE_BASE_URL and fetches the
// public keys published by the user service.
func NewUserServiceAuthenticator(ctx context.Context) (*Authenticator, error) {
	baseURL := strings.TrimSpace(os.Getenv("USER_SERVICE_BASE_URL"))
	if baseURL == "" {
		return nil, errors.New("USER_SERVICE_BASE_URL is required")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("invalid user service URL")
	}
	jwksURL, err := url.JoinPath(baseURL, ".well-known/jwks.json")
	if err != nil {
		return nil, fmt.Errorf("build user service JWKS URL: %w", err)
	}
	return NewAuthenticator(ctx, AuthConfig{JWKSURL: jwksURL,
		Issuer: TokenIssuer, Audience: TokenAudience})
}

func (a *Authenticator) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			api.WriteError(unauthorizedError{}, w, r.Context())
			return
		}
		claims, err := a.verify(r.Context(), parts[1])
		if err != nil {
			if errors.Is(err, errKeysUnavailable) {
				api.WriteError(keysUnavailableError{}, w, r.Context())
			} else {
				api.WriteError(unauthorizedError{}, w, r.Context())
			}
			return
		}
		ctx := context.WithValue(r.Context(), claimsKey[AccessClaims]{}, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

var (
	errInvalidAccessToken = errors.New("invalid access token")
	errKeysUnavailable    = errors.New("authentication keys unavailable")
)

func (a *Authenticator) verify(ctx context.Context, token string) (AccessClaims, error) {
	if len(token) > 8192 {
		return AccessClaims{}, errInvalidAccessToken
	}
	payload := tokenclaims.New(tokenclaims.AccessToken)
	parsed, err := jwt.ParseWithClaims(token, payload, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		if t.Header["typ"] != "JWT" || kid == "" {
			return nil, errInvalidAccessToken
		}
		return a.key(ctx, t)
	}, jwt.WithValidMethods([]string{jwt.SigningMethodES256.Alg()}),
		jwt.WithIssuer(a.config.Issuer), jwt.WithAudience(a.config.Audience),
		jwt.WithExpirationRequired(), jwt.WithNotBeforeRequired(),
		jwt.WithIssuedAt(), jwt.WithStrictDecoding())
	if err != nil {
		if errors.Is(err, errKeysUnavailable) {
			return AccessClaims{}, errKeysUnavailable
		}
		return AccessClaims{}, errInvalidAccessToken
	}
	if !parsed.Valid {
		return AccessClaims{}, errInvalidAccessToken
	}
	return AccessClaims{Subject: payload.Subject, SessionID: payload.SessionID, Role: string(payload.Role),
		IssuedAt: payload.IssuedAt.Time.UTC(), ExpiresAt: payload.ExpiresAt.Time.UTC(), TokenID: payload.ID}, nil
}

func (a *Authenticator) key(ctx context.Context, token *jwt.Token) (any, error) {
	cache := a.cache.Load()
	now := time.Now()
	key, err := lookupKey(ctx, cache, token)
	if cache != nil && now.Before(cache.expires) && err == nil {
		return key, nil
	}
	if err != nil && !errors.Is(err, jwkset.ErrKeyNotFound) {
		return nil, errInvalidAccessToken
	}
	return a.refreshKey(ctx, token)
}

func (a *Authenticator) refreshKey(ctx context.Context, token *jwt.Token) (any, error) {
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()

	cache := a.cache.Load()
	now := time.Now()
	key, err := lookupKey(ctx, cache, token)
	if cache != nil && now.Before(cache.expires) && err == nil {
		return key, nil
	}
	if err != nil && !errors.Is(err, jwkset.ErrKeyNotFound) {
		return nil, errInvalidAccessToken
	}
	// An unknown kid triggers one early refresh. Limit repeated misses caused
	// by forged tokens with random key IDs.
	if errors.Is(err, jwkset.ErrKeyNotFound) && cache != nil && now.Before(cache.expires) {
		if now.Sub(a.lastMiss) < 5*time.Second {
			return nil, errInvalidAccessToken
		}
		a.lastMiss = now
	}
	cache, err = a.fetch(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errKeysUnavailable, err)
	}
	a.cache.Store(cache)
	key, err = lookupKey(ctx, cache, token)
	if errors.Is(err, jwkset.ErrKeyNotFound) {
		return nil, errInvalidAccessToken
	}
	if err != nil {
		return nil, errInvalidAccessToken
	}
	return key, nil
}

func lookupKey(ctx context.Context, cache *keyCache, token *jwt.Token) (any, error) {
	if cache == nil || cache.keys == nil {
		return nil, jwkset.ErrKeyNotFound
	}
	return cache.keys.KeyfuncCtx(ctx)(token)
}

func (a *Authenticator) fetch(ctx context.Context) (*keyCache, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.config.JWKSURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("JWKS returned HTTP %d", resp.StatusCode)
	}
	const maxJWKSSize = 1 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxJWKSSize+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxJWKSSize {
		return nil, errors.New("JWKS response exceeds 1 MiB")
	}
	var set jwkset.JWKSMarshal
	if err := json.Unmarshal(body, &set); err != nil {
		return nil, err
	}
	storage := jwkset.NewMemoryStorage()
	seen := make(map[string]struct{}, len(set.Keys))
	for _, item := range set.Keys {
		if item.KTY != jwkset.KtyEC || item.CRV != jwkset.CrvP256 || item.ALG != jwkset.AlgES256 ||
			item.USE != jwkset.UseSig || item.KID == "" {
			return nil, errors.New("unsupported JWKS key")
		}
		if _, duplicate := seen[item.KID]; duplicate {
			return nil, errors.New("duplicate JWKS key")
		}
		seen[item.KID] = struct{}{}
		key, err := jwkset.NewJWKFromMarshal(item, jwkset.JWKMarshalOptions{},
			jwkset.JWKValidateOptions{StrictPadding: true})
		if err != nil {
			return nil, fmt.Errorf("invalid JWKS key: %w", err)
		}
		if err := storage.KeyWrite(ctx, key); err != nil {
			return nil, fmt.Errorf("store JWKS key: %w", err)
		}
	}
	if len(seen) == 0 {
		return nil, errors.New("empty JWKS")
	}
	keys, err := keyfunc.New(keyfunc.Options{Storage: storage, UseWhitelist: []jwkset.USE{jwkset.UseSig}})
	if err != nil {
		return nil, fmt.Errorf("create JWKS key function: %w", err)
	}
	return &keyCache{keys: keys, expires: time.Now().Add(keyCacheLifetime)}, nil
}

type keysUnavailableError struct{}

func (keysUnavailableError) Error() string        { return "authentication keys unavailable" }
func (e keysUnavailableError) ErrorTrace() string { return e.Error() }
func (keysUnavailableError) Code() int            { return http.StatusServiceUnavailable }
