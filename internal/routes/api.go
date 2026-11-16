package routes

import (
	"errors"
	"log/slog"

	"wallet-service/internal/handlers"
	"wallet-service/internal/middleware"
	"wallet-service/internal/utils"

	"github.com/gofiber/fiber/v3"
)

type Handlers struct {
	Addresses    *handlers.AddressHandler
	Transactions *handlers.TransactionHandler
}

func Register(app *fiber.App, h Handlers, authToken string) {
	app.Get("/health", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	v1 := app.Group("/api/v1", middleware.ValidateToken(authToken))

	v1.Post("/createaddress", middleware.ValidateBody[handlers.CreateAddressRequest](), h.Addresses.Create)
	v1.Post("/validateaddress", middleware.ValidateBody[handlers.ValidateAddressRequest](), h.Addresses.Validate)
	v1.Post("/tx", middleware.ValidateBody[handlers.SignTxRequest](), h.Transactions.Sign)
}

func ErrorHandler(log *slog.Logger) fiber.ErrorHandler {
	return func(c fiber.Ctx, err error) error {
		status, message := fiber.StatusInternalServerError, "Internal Error"

		var reqErr *utils.RequestError
		var fiberErr *fiber.Error
		switch {
		case errors.As(err, &reqErr):
			status, message = reqErr.Status, reqErr.Message
		case errors.As(err, &fiberErr):
			status, message = fiberErr.Code, fiberErr.Message
		default:
			log.Error("Request failed", "method", c.Method(), "path", c.Path(), "error", err)
		}

		return c.Status(status).JSON(fiber.Map{"error": message})
	}
}
