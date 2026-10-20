package security

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// JWTManager manages JWT tokens
type JWTManager struct {
	secretKey     string
	tokenDuration time.Duration
}

// Claims represents the JWT claims
type Claims struct {
	UserID   string   `json:"userId"`
	TenantID string   `json:"tenantId,omitempty"`
	Username string   `json:"username"`
	Roles    []string `json:"roles"`
	jwt.RegisteredClaims
}

// NewJWTManager creates a new JWTManager
func NewJWTManager(secretKey string, tokenDuration time.Duration) *JWTManager {
	return &JWTManager{
		secretKey:     secretKey,
		tokenDuration: tokenDuration,
	}
}

// GenerateToken generates a new JWT token. tenantID may be empty; tokens
// without a tenant act as members of the default tenant bucket (#9).
func (j *JWTManager) GenerateToken(userID, tenantID, username string, roles []string) (string, error) {
	claims := &Claims{
		UserID:   userID,
		TenantID: tenantID,
		Username: username,
		Roles:    roles,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(j.tokenDuration)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
			Issuer:    "data-fabric",
			Subject:   userID,
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(j.secretKey))
}

// ValidateToken validates a JWT token and returns the claims
func (j *JWTManager) ValidateToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		// Validate signing method
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(j.secretKey), nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to parse token: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}

	// Check if token is expired
	if claims.ExpiresAt != nil && claims.ExpiresAt.Time.Before(time.Now()) {
		return nil, fmt.Errorf("token has expired")
	}

	return claims, nil
}

// RefreshToken generates a new token with extended expiration
func (j *JWTManager) RefreshToken(claims *Claims) (string, error) {
	// Create new claims with same user info but extended expiration
	newClaims := &Claims{
		UserID:   claims.UserID,
		TenantID: claims.TenantID,
		Username: claims.Username,
		Roles:    claims.Roles,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(j.tokenDuration)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
			Issuer:    "data-fabric",
			Subject:   claims.UserID,
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, newClaims)
	return token.SignedString([]byte(j.secretKey))
}

// ExtractTokenFromHeader extracts JWT token from Authorization header
func (j *JWTManager) ExtractTokenFromHeader(authHeader string) (string, error) {
	if authHeader == "" {
		return "", fmt.Errorf("authorization header is required")
	}

	// Check if header starts with "Bearer "
	const bearerPrefix = "Bearer "
	if len(authHeader) < len(bearerPrefix) || authHeader[:len(bearerPrefix)] != bearerPrefix {
		return "", fmt.Errorf("authorization header must start with 'Bearer '")
	}

	return authHeader[len(bearerPrefix):], nil
}

// HasRole checks if the user has the specified role
func (c *Claims) HasRole(role string) bool {
	for _, r := range c.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// HasAnyRole checks if the user has any of the specified roles
func (c *Claims) HasAnyRole(roles ...string) bool {
	for _, role := range roles {
		if c.HasRole(role) {
			return true
		}
	}
	return false
}

// IsExpired checks if the token is expired
func (c *Claims) IsExpired() bool {
	return c.ExpiresAt != nil && c.ExpiresAt.Time.Before(time.Now())
}

// TimeUntilExpiry returns the duration until the token expires
func (c *Claims) TimeUntilExpiry() time.Duration {
	if c.ExpiresAt == nil {
		return 0
	}
	return time.Until(c.ExpiresAt.Time)
}
