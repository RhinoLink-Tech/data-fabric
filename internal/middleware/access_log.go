package middleware

import (
	"fmt"
	"io"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/gin-gonic/gin"
)

func logRoute(c *gin.Context) string {
	if route := c.FullPath(); route != "" {
		return route
	}
	return "/unmatched"
}

// AccessLog records route templates because continuation paths contain credentials.
func AccessLog(out io.Writer) gin.HandlerFunc {
	return func(c *gin.Context) {
		started := time.Now()
		c.Next()
		fmt.Fprintf(out, "[GIN] %s | %3d | %13v | %15s | %-7s %q\n",
			time.Now().Format("2006/01/02 - 15:04:05"), c.Writer.Status(), time.Since(started),
			c.ClientIP(), c.Request.Method, logRoute(c))
	}
}

// Recovery keeps stack diagnostics without dumping the request or panic value.
func Recovery(out io.Writer) gin.HandlerFunc {
	return gin.CustomRecoveryWithWriter(nil, func(c *gin.Context, _ any) {
		fmt.Fprintf(out, "[Recovery] panic recovered on %s %q\n%s", c.Request.Method, logRoute(c), debug.Stack())
		c.AbortWithStatus(http.StatusInternalServerError)
	})
}
