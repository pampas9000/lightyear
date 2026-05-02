package middleware

import (
	"strings"

	"transcoder/server/internal/api/response"

	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
)

func NewJWTMiddleware(secret string) fiber.Handler {
	return func(c fiber.Ctx) error {
		authHeader := c.Get("Authorization")
		if authHeader == "" {
			return response.RespondError(
				c,
				fiber.StatusUnauthorized,
				response.CodeUnauthorized,
				"Authorization header is required.",
			)
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			return response.RespondError(
				c,
				fiber.StatusUnauthorized,
				response.CodeUnauthorized,
				"Authorization header must be in the format 'Bearer <token>'.",
			)
		}

		tokenString := parts[1]
		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (any, error) {
			return []byte(secret), nil
		})

		if err != nil || !token.Valid {
			return response.RespondError(
				c,
				fiber.StatusUnauthorized,
				response.CodeUnauthorized,
				"Invalid or expired token.",
			)
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			return response.RespondError(
				c,
				fiber.StatusUnauthorized,
				response.CodeUnauthorized,
				"Invalid token claims.",
			)
		}

		userID := claims["sub"].(string)
		c.Locals("user_id", userID)

		return c.Next()
	}
}
