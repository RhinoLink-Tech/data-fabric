package security

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

var (
	ErrTokenUnavailable         = errors.New("current access token is unavailable")
	ErrTokenRotationUnsupported = errors.New("token rotation is unsupported")
	ErrTokenRotationConflict    = errors.New("access token changed during rotation")
)

// TokenStore persists access tokens as encrypted data-source credentials. A
// compare-and-swap prevents one gateway instance from overwriting a newer token.
type TokenStore interface {
	LoadAccessToken(ctx context.Context, dataSourceID string) (token string, expiresAt time.Time, err error)
	CompareAndSwapAccessToken(ctx context.Context, dataSourceID, expectedToken, replacement string, expiresAt time.Time) (swapped bool, err error)
}

type tokenRotation struct {
	currentToken string
	expiresAt    time.Time
	onRotate     func(ctx context.Context) (newToken string, expiresAt time.Time, err error)
	lastRotation time.Time
	mu           sync.Mutex
}

// TokenManager handles automatic rotation of authentication tokens.
type TokenManager struct {
	rotationCheck  time.Duration
	rotationBuffer time.Duration

	rotationsMu sync.RWMutex
	rotations   map[string]*tokenRotation

	storeMu sync.RWMutex
	store   TokenStore

	lifecycleMu sync.Mutex
	started     bool
	stopped     bool
	stopChan    chan struct{}
	done        chan struct{}
	cancel      context.CancelFunc
	stopOnce    sync.Once
}

// NewTokenManager creates a token manager. The vault argument is retained for
// source compatibility; encrypted persistence is supplied by TokenStore.
func NewTokenManager(_ *CredentialVault) *TokenManager {
	return &TokenManager{
		rotationCheck:  5 * time.Minute,
		rotationBuffer: 5 * time.Minute,
		rotations:      make(map[string]*tokenRotation),
		stopChan:       make(chan struct{}),
	}
}

// SetStore installs encrypted persistence for token reads and replacements.
func (tm *TokenManager) SetStore(store TokenStore) {
	if tm == nil {
		return
	}
	tm.storeMu.Lock()
	tm.store = store
	tm.storeMu.Unlock()
}

func (tm *TokenManager) tokenStore() TokenStore {
	tm.storeMu.RLock()
	defer tm.storeMu.RUnlock()
	return tm.store
}

// RegisterToken registers the provider for a data source's access token. It
// preserves an existing cached token when the provider is re-registered.
func (tm *TokenManager) RegisterToken(dataSourceID string, onRotate func(ctx context.Context) (string, time.Time, error)) error {
	if tm == nil {
		return fmt.Errorf("%w: token manager is unavailable", ErrTokenRotationUnsupported)
	}
	if strings.TrimSpace(dataSourceID) == "" {
		return errors.New("data source ID is required")
	}
	if onRotate == nil {
		return fmt.Errorf("token rotation provider is required for data source %s", dataSourceID)
	}

	tm.rotationsMu.Lock()
	defer tm.rotationsMu.Unlock()
	if existing := tm.rotations[dataSourceID]; existing != nil {
		existing.mu.Lock()
		existing.onRotate = onRotate
		existing.mu.Unlock()
		return nil
	}
	tm.rotations[dataSourceID] = &tokenRotation{onRotate: onRotate}
	return nil
}

// RemoveToken unregisters a data source from automatic rotation.
func (tm *TokenManager) RemoveToken(dataSourceID string) {
	if tm == nil {
		return
	}
	tm.rotationsMu.Lock()
	delete(tm.rotations, dataSourceID)
	tm.rotationsMu.Unlock()
}

// Start launches the background checker once. It checks immediately and then
// at the configured interval.
func (tm *TokenManager) Start(ctx context.Context) {
	if tm == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	tm.lifecycleMu.Lock()
	if tm.started || tm.stopped {
		tm.lifecycleMu.Unlock()
		return
	}
	tm.started = true
	runCtx, cancel := context.WithCancel(ctx)
	tm.cancel = cancel
	tm.done = make(chan struct{})
	done := tm.done
	tm.lifecycleMu.Unlock()

	go func() {
		defer close(done)
		ticker := time.NewTicker(tm.rotationCheck)
		defer ticker.Stop()
		tm.checkAndRotateTokens(runCtx)
		for {
			select {
			case <-runCtx.Done():
				return
			case <-tm.stopChan:
				return
			case <-ticker.C:
				tm.checkAndRotateTokens(ctx)
			}
		}
	}()
}

// Stop stops the background checker and waits for it to exit. It is safe to
// call repeatedly, including when Start was never called.
func (tm *TokenManager) Stop() {
	if tm == nil {
		return
	}
	tm.stopOnce.Do(func() {
		tm.lifecycleMu.Lock()
		tm.stopped = true
		close(tm.stopChan)
		cancel := tm.cancel
		done := tm.done
		tm.lifecycleMu.Unlock()
		if cancel != nil {
			cancel()
		}
		if done != nil {
			<-done
		}
	})
}

func (tm *TokenManager) checkAndRotateTokens(ctx context.Context) {
	tm.rotationsMu.RLock()
	candidates := make(map[string]*tokenRotation, len(tm.rotations))
	for id, rotation := range tm.rotations {
		candidates[id] = rotation
	}
	tm.rotationsMu.RUnlock()

	for id, rotation := range candidates {
		rotation.mu.Lock()
		needsRotation := rotation.expiresAt.IsZero() || time.Until(rotation.expiresAt) < tm.rotationBuffer
		rotation.mu.Unlock()
		if !needsRotation {
			continue
		}
		if _, _, err := tm.RotateTokenWithResult(ctx, id); err != nil && ctx.Err() == nil {
			log.Printf("failed to rotate access token for data source %s: %v", id, err)
		}
	}
}

// EnsureToken returns a safely usable token, refreshing it when missing,
// expired, or within the configured rotation buffer. If refresh fails while
// the previous token is still valid, that token remains usable.
func (tm *TokenManager) EnsureToken(ctx context.Context, dataSourceID string) error {
	if tm == nil {
		return fmt.Errorf("%w: token manager is unavailable", ErrTokenRotationUnsupported)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tm.rotationsMu.RLock()
	rotation := tm.rotations[dataSourceID]
	tm.rotationsMu.RUnlock()
	if rotation == nil {
		return fmt.Errorf("%w for data source %s", ErrTokenRotationUnsupported, dataSourceID)
	}

	rotation.mu.Lock()
	store := tm.tokenStore()
	if store != nil {
		token, expiresAt, err := store.LoadAccessToken(ctx, dataSourceID)
		if err != nil {
			cachedUsable := rotation.currentToken != "" && time.Until(rotation.expiresAt) > tm.rotationBuffer
			rotation.mu.Unlock()
			if cachedUsable {
				return nil
			}
			return fmt.Errorf("load current token for data source %s: %w", dataSourceID, err)
		}
		if token != "" && time.Now().Before(expiresAt) {
			rotation.currentToken = token
			rotation.expiresAt = expiresAt
		}
	}
	needsRotation := rotation.currentToken == "" || !time.Now().Before(rotation.expiresAt) ||
		time.Until(rotation.expiresAt) < tm.rotationBuffer
	rotation.mu.Unlock()

	if !needsRotation {
		return nil
	}
	if _, _, err := tm.RotateTokenWithResult(ctx, dataSourceID); err != nil {
		if _, tokenErr := tm.GetToken(ctx, dataSourceID); tokenErr == nil {
			return nil
		}
		return err
	}
	return nil
}

// RotateToken rotates a specific token and logs errors without exposing secrets.
func (tm *TokenManager) RotateToken(ctx context.Context, dataSourceID string) {
	if _, _, err := tm.RotateTokenWithResult(ctx, dataSourceID); err != nil {
		log.Printf("failed to rotate access token for data source %s: %v", dataSourceID, err)
	}
}

// RotateTokenWithResult fetches and validates a replacement before atomically
// persisting and publishing it. On failure the prior token remains untouched.
func (tm *TokenManager) RotateTokenWithResult(ctx context.Context, dataSourceID string) (string, time.Time, error) {
	if tm == nil {
		return "", time.Time{}, fmt.Errorf("%w: token manager is unavailable", ErrTokenRotationUnsupported)
	}
	tm.rotationsMu.RLock()
	rotation := tm.rotations[dataSourceID]
	tm.rotationsMu.RUnlock()
	if rotation == nil {
		return "", time.Time{}, fmt.Errorf("%w for data source %s", ErrTokenRotationUnsupported, dataSourceID)
	}

	rotation.mu.Lock()
	defer rotation.mu.Unlock()

	expectedToken := rotation.currentToken
	store := tm.tokenStore()
	if store != nil {
		persistedToken, persistedExpiry, err := store.LoadAccessToken(ctx, dataSourceID)
		if err != nil {
			return "", time.Time{}, fmt.Errorf("load current token for data source %s: %w", dataSourceID, err)
		}
		expectedToken = persistedToken
		if persistedToken != "" && time.Now().Before(persistedExpiry) {
			rotation.currentToken = persistedToken
			rotation.expiresAt = persistedExpiry
		}
	}

	newToken, expiresAt, err := rotation.onRotate(ctx)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("refresh token for data source %s: %w", dataSourceID, err)
	}
	if strings.TrimSpace(newToken) == "" {
		return "", time.Time{}, fmt.Errorf("refresh token for data source %s: provider returned an empty token", dataSourceID)
	}
	if !expiresAt.After(time.Now()) {
		return "", time.Time{}, fmt.Errorf("refresh token for data source %s: provider returned an expired token", dataSourceID)
	}

	if store != nil {
		swapped, err := store.CompareAndSwapAccessToken(ctx, dataSourceID, expectedToken, newToken, expiresAt)
		if err != nil {
			return "", time.Time{}, fmt.Errorf("persist refreshed token for data source %s: %w", dataSourceID, err)
		}
		if !swapped {
			currentToken, currentExpiry, err := store.LoadAccessToken(ctx, dataSourceID)
			if err != nil {
				return "", time.Time{}, fmt.Errorf("reload token after concurrent rotation for data source %s: %w", dataSourceID, err)
			}
			if currentToken == "" || !time.Now().Before(currentExpiry) {
				return "", time.Time{}, fmt.Errorf("%w for data source %s", ErrTokenRotationConflict, dataSourceID)
			}
			rotation.currentToken = currentToken
			rotation.expiresAt = currentExpiry
			return currentToken, currentExpiry, nil
		}
	}

	rotation.currentToken = newToken
	rotation.expiresAt = expiresAt
	rotation.lastRotation = time.Now()
	return newToken, expiresAt, nil
}

// GetToken retrieves the current unexpired token from memory or encrypted
// storage. It never treats an empty placeholder as a valid token.
func (tm *TokenManager) GetToken(ctx context.Context, dataSourceID string) (string, error) {
	if tm == nil {
		return "", fmt.Errorf("%w: token manager is unavailable", ErrTokenUnavailable)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tm.rotationsMu.RLock()
	rotation := tm.rotations[dataSourceID]
	tm.rotationsMu.RUnlock()

	var cachedToken string
	var cachedExpiry time.Time
	if rotation != nil {
		rotation.mu.Lock()
		cachedToken, cachedExpiry = rotation.currentToken, rotation.expiresAt
		rotation.mu.Unlock()
	}

	if store := tm.tokenStore(); store != nil {
		token, expiresAt, err := store.LoadAccessToken(ctx, dataSourceID)
		if err == nil && token != "" && time.Now().Before(expiresAt) {
			if rotation != nil {
				rotation.mu.Lock()
				rotation.currentToken = token
				rotation.expiresAt = expiresAt
				rotation.mu.Unlock()
			}
			return token, nil
		}
		if err != nil && (cachedToken == "" || !time.Now().Before(cachedExpiry)) {
			return "", fmt.Errorf("load current token for data source %s: %w", dataSourceID, err)
		}
	}

	if cachedToken != "" && time.Now().Before(cachedExpiry) {
		return cachedToken, nil
	}
	return "", fmt.Errorf("%w for data source %s", ErrTokenUnavailable, dataSourceID)
}
