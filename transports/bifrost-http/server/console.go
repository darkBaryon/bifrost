package server

import (
	"context"
	"fmt"
	"slices"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/transports/bifrost-http/handlers"
)

// validateConsoleOptions rejects invalid middleware chains before they can register routes.
func (s *BifrostHTTPServer) validateConsoleOptions() error {
	if s.ConsoleMiddlewares != nil {
		if len(s.ConsoleMiddlewares) == 0 {
			return fmt.Errorf("ConsoleMiddlewares must contain at least one middleware")
		}
		for i, middleware := range s.ConsoleMiddlewares {
			if middleware == nil {
				return fmt.Errorf("ConsoleMiddlewares[%d] is nil", i)
			}
		}
	}
	return nil
}

// prepareConsoleRoutes runs the embedder's startup work and selects only the console
// middleware. Bootstrap retains ownership of common and inference middleware.
func (s *BifrostHTTPServer) prepareConsoleRoutes(ctx context.Context) ([]schemas.BifrostHTTPMiddleware, error) {
	if err := s.validateConsoleOptions(); err != nil {
		return nil, err
	}
	if s.BeforeAPIRoutes != nil {
		if err := s.BeforeAPIRoutes(ctx, s); err != nil {
			return nil, fmt.Errorf("BeforeAPIRoutes: %w", err)
		}
	}
	if err := s.validateConsoleOptions(); err != nil {
		return nil, err
	}
	if s.ConsoleMiddlewares != nil {
		return slices.Clone(s.ConsoleMiddlewares), nil
	}
	if ctx.Value(schemas.BifrostContextKeyIsEnterprise) == nil && s.AuthMiddleware != nil {
		return []schemas.BifrostHTTPMiddleware{s.AuthMiddleware.APIMiddleware()}, nil
	}
	return nil, nil
}

// registerConsoleSessionRoutes keeps session routing independent of authentication.
// Give registrars their own slice so they cannot change middleware for later routes.
func (s *BifrostHTTPServer) registerConsoleSessionRoutes(middlewares ...schemas.BifrostHTTPMiddleware) {
	middlewares = slices.Clone(middlewares)
	if s.RegisterConsoleSessionRoutes != nil {
		s.RegisterConsoleSessionRoutes(s.Router, middlewares...)
	} else {
		handlers.NewSessionHandler(s.Config.ConfigStore, s.WSTicketStore).RegisterRoutes(s.Router, middlewares...)
	}
}
