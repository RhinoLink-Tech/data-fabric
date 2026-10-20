package security

import (
	"net/http"

	"github.com/RhinoLink-Tech/data-fabric/pkg/response"
	"github.com/gin-gonic/gin"
)

// AuthMiddleware provides JWT authentication middleware
type AuthMiddleware struct {
	jwtManager *JWTManager
}

// NewAuthMiddleware creates a new AuthMiddleware
func NewAuthMiddleware(jwtManager *JWTManager) *AuthMiddleware {
	return &AuthMiddleware{
		jwtManager: jwtManager,
	}
}

// RequireAuth creates a middleware that requires authentication
// authenticate validates the Bearer token and stores the claims. It never
// advances the chain, so middlewares composed around it can run further
// checks before any route handler executes. Authentication failures are
// answered and the context aborted; on success the claims are returned.
func (am *AuthMiddleware) authenticate(c *gin.Context) *Claims {
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		c.JSON(http.StatusUnauthorized, response.UnauthorizedResponse(
			"Authorization header is required",
			am.getCorrelationID(c),
		))
		c.Abort()
		return nil
	}

	// Extract token from header
	token, err := am.jwtManager.ExtractTokenFromHeader(authHeader)
	if err != nil {
		c.JSON(http.StatusUnauthorized, response.UnauthorizedResponse(
			err.Error(),
			am.getCorrelationID(c),
		))
		c.Abort()
		return nil
	}

	// Validate token
	claims, err := am.jwtManager.ValidateToken(token)
	if err != nil {
		c.JSON(http.StatusUnauthorized, response.UnauthorizedResponse(
			"Invalid or expired token",
			am.getCorrelationID(c),
		))
		c.Abort()
		return nil
	}

	// Store claims in context
	c.Set("user_claims", claims)
	c.Set("user_id", claims.UserID)
	c.Set("username", claims.Username)
	c.Set("user_roles", claims.Roles)
	return claims
}

func (am *AuthMiddleware) RequireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if am.authenticate(c) == nil {
			return
		}
		c.Next()
	}
}

// RequireRole creates a middleware that requires a specific role. The role
// check runs after authentication but before the chain advances, so a caller
// without the role never reaches the route handler.
func (am *AuthMiddleware) RequireRole(role string) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := am.authenticate(c)
		if claims == nil {
			return
		}
		if !claims.HasRole(role) {
			am.rejectRole(c)
			return
		}
		c.Next()
	}
}

// RequireAnyRole creates a middleware that requires any of the specified roles
func (am *AuthMiddleware) RequireAnyRole(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := am.authenticate(c)
		if claims == nil {
			return
		}
		if !claims.HasAnyRole(roles...) {
			am.rejectRole(c)
			return
		}
		c.Next()
	}
}

func (am *AuthMiddleware) rejectRole(c *gin.Context) {
	c.JSON(http.StatusForbidden, response.ForbiddenResponse(
		"Insufficient permissions",
		am.getCorrelationID(c),
	))
	c.Abort()
}

// OptionalAuth creates a middleware that optionally validates authentication
func (am *AuthMiddleware) OptionalAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			// No authorization header, continue without authentication
			c.Next()
			return
		}

		// Try to authenticate
		token, err := am.jwtManager.ExtractTokenFromHeader(authHeader)
		if err != nil {
			// Invalid header format, continue without authentication
			c.Next()
			return
		}

		claims, err := am.jwtManager.ValidateToken(token)
		if err != nil {
			// Invalid token, continue without authentication
			c.Next()
			return
		}

		// Store claims in context
		c.Set("user_claims", claims)
		c.Set("user_id", claims.UserID)
		c.Set("username", claims.Username)
		c.Set("user_roles", claims.Roles)

		c.Next()
	}
}

// SkipAuth creates a middleware that skips authentication
func (am *AuthMiddleware) SkipAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
	}
}

// Helper functions

func (am *AuthMiddleware) combineMiddleware(middlewares ...gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		for _, middleware := range middlewares {
			middleware(c)
			if c.IsAborted() {
				return
			}
		}
		c.Next()
	}
}

func (am *AuthMiddleware) getCorrelationID(c *gin.Context) string {
	if correlationID, exists := c.Get("correlation_id"); exists {
		if id, ok := correlationID.(string); ok {
			return id
		}
	}
	return ""
}

// Helper functions for getting user info from context

// GetUserID extracts user ID from context
func GetUserID(c *gin.Context) (string, bool) {
	userID, exists := c.Get("user_id")
	if !exists {
		return "", false
	}
	id, ok := userID.(string)
	return id, ok
}

// GetUsername extracts username from context
func GetUsername(c *gin.Context) (string, bool) {
	username, exists := c.Get("username")
	if !exists {
		return "", false
	}
	name, ok := username.(string)
	return name, ok
}

// GetUserRoles extracts user roles from context
func GetUserRoles(c *gin.Context) ([]string, bool) {
	roles, exists := c.Get("user_roles")
	if !exists {
		return nil, false
	}
	userRoles, ok := roles.([]string)
	return userRoles, ok
}

// GetUserClaims extracts user claims from context
func GetUserClaims(c *gin.Context) (*Claims, bool) {
	claims, exists := c.Get("user_claims")
	if !exists {
		return nil, false
	}
	userClaims, ok := claims.(*Claims)
	return userClaims, ok
}

// IsAuthenticated checks if user is authenticated
func IsAuthenticated(c *gin.Context) bool {
	_, exists := c.Get("user_claims")
	return exists
}

// HasRole checks if user has specific role
func HasRole(c *gin.Context, role string) bool {
	claims, exists := GetUserClaims(c)
	if !exists {
		return false
	}
	return claims.HasRole(role)
}

// HasAnyRole checks if user has any of the specified roles
func HasAnyRole(c *gin.Context, roles ...string) bool {
	claims, exists := GetUserClaims(c)
	if !exists {
		return false
	}
	return claims.HasAnyRole(roles...)
}
