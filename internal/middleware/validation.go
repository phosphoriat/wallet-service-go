package middleware

import (
	"crypto/subtle"
	"strings"

	"wallet-service/internal/utils"

	"github.com/gofiber/fiber/v3"
)

type Validator interface {
	Validate() error
}

type bodyKey struct{}

// ValidateBody parses the JSON body into T (unknown fields are forbidden), calls Validate(), and stores the result in Locals. The handler gets it via Body[T].
// app.Post("/x", middleware.ValidateBody[handlers.XRequest](), h.X)
func ValidateBody[T any, PT interface {
	*T
	Validator
}]() fiber.Handler {
	return func(c fiber.Ctx) error {
		if !strings.HasPrefix(c.Get(fiber.HeaderContentType), fiber.MIMEApplicationJSON) {
			return &utils.RequestError{Status: fiber.StatusUnsupportedMediaType, Message: "Content-Type must be application/json"}
		}

		req := PT(new(T))
		if err := utils.DecodeStrictJSON(c.Body(), req); err != nil {
			return err
		}
		if err := req.Validate(); err != nil {
			return err
		}

		fiber.Locals[PT](c, bodyKey{}, req)
		return c.Next()
	}
}

// Body returns the body validated by ValidateBody[T]
func Body[T any](c fiber.Ctx) *T {
	return fiber.Locals[*T](c, bodyKey{})
}

// ValidateToken checks Authorization: Bearer <token>. An empty token means no authorization.
func ValidateToken(token string) fiber.Handler {
	return func(c fiber.Ctx) error {
		if token == "" {
			return c.Next()
		}

		got, ok := strings.CutPrefix(c.Get(fiber.HeaderAuthorization), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			return &utils.RequestError{Status: fiber.StatusUnauthorized, Message: "unauthorized"}
		}

		return c.Next()
	}
}
