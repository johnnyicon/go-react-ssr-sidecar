// cmd/api — Go API server skeleton.
//
// This is the API service that the web server proxies /api/* requests to.
// In Railway, deploy this as a separate service from cmd/web.
// For local development, run both binaries: `make dev` starts both.
//
// CUSTOMIZE: Add your database connection, middleware, and route handlers here.
// The web server (cmd/web/main.go) proxies /api/* here, and listens for the
// X-App-Invalidate response header to invalidate the HTML cache when content changes.
//
// To trigger HTML cache invalidation from an API handler:
//   c.Response().Header().Set("X-App-Invalidate", contentID)
// The web server's proxy middleware will see this and call invalidateContentHTML().
package main

import (
	"log"
	"net/http"
	"os"

	_ "github.com/joho/godotenv/autoload"
	"github.com/labstack/echo/v4"
	echoMiddleware "github.com/labstack/echo/v4/middleware"
)

func main() {
	e := echo.New()
	e.HideBanner = true
	e.Use(echoMiddleware.Logger())
	e.Use(echoMiddleware.Recover())
	e.Use(echoMiddleware.CORSWithConfig(echoMiddleware.CORSConfig{
		AllowOrigins:     []string{os.Getenv("CORS_ORIGIN")},
		AllowCredentials: true,
		AllowHeaders:     []string{"Content-Type", "Authorization"},
		AllowMethods:     []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete},
	}))

	// Health check
	e.GET("/health", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})

	// CUSTOMIZE: Add your API routes here.
	// Example:
	//   e.GET("/api/v1/posts", handleListPosts)
	//   e.GET("/api/v1/posts/:id", handleGetPost)
	//   e.PUT("/api/v1/posts/:id", handleUpdatePost)
	//
	// For routes that modify content, set X-App-Invalidate to trigger HTML
	// cache invalidation in the web server:
	//   c.Response().Header().Set("X-App-Invalidate", post.ID)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("api: listening on :%s", port)
	if err := e.Start(":" + port); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
