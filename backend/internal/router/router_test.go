package router

import (
	"testing"

	"feed-backend/internal/bootstrap"
)

func TestNewEngineRegistersUserRoutes(t *testing.T) {
	runtime := &bootstrap.Runtime{
		Config: &bootstrap.Config{
			App: bootstrap.AppConfig{
				StaticBaseURL: "http://localhost:18080",
			},
			JWT: bootstrap.JWTConfig{
				Secret:      "test-secret",
				ExpireHours: 1,
			},
			Pagination: bootstrap.PaginationConfig{
				DefaultLimit: 10,
				MaxLimit:     20,
			},
			RabbitMQ: bootstrap.RabbitMQConfig{
				Exchange: "feed.events",
			},
		},
	}

	engine := NewEngine(runtime)
	routes := make(map[string]struct{})
	for _, route := range engine.Routes() {
		routes[route.Method+" "+route.Path] = struct{}{}
	}

	expectedRoutes := []string{
		"GET /api/v1/users/*path",
		"PUT /api/v1/users/me/profile",
		"PUT /api/v1/users/me/avatar",
		"PUT /api/v1/users/me/password",
		"POST /api/v1/users/:user_id/follow",
		"DELETE /api/v1/users/:user_id/follow",
	}
	for _, route := range expectedRoutes {
		if _, ok := routes[route]; !ok {
			t.Fatalf("expected route %s to be registered", route)
		}
	}
}
