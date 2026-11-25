package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/session"
	"github.com/google/uuid"
)

// The session middleware parses the session's user once; handlers read it
// with UserID. A session whose user_id is not a UUID is not a login.
func TestAuthMiddlewareSetsParsedUserID(t *testing.T) {
	store := session.NewStore()
	app := fiber.New()
	app.Post("/login/:id", func(c fiber.Ctx) error {
		sess, err := store.Get(c)
		if err != nil {
			return err
		}
		sess.Set("user_id", c.Params("id"))
		if err := sess.Save(); err != nil {
			return err
		}
		return nil
	})
	app.Get("/me", AuthMiddleware(store), func(c fiber.Ctx) error {
		id, ok := UserID(c)
		if !ok {
			return c.SendStatus(fiber.StatusTeapot)
		}
		return c.SendString(id.String())
	})
	me := func(sessionUser string) (int, string) {
		t.Helper()
		res, err := app.Test(httptest.NewRequest(http.MethodPost, "/login/"+sessionUser, nil))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		req := httptest.NewRequest(http.MethodGet, "/me", nil)
		for _, ck := range res.Cookies() {
			req.AddCookie(ck)
		}
		res, err = app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(body)
	}

	user := uuid.New()
	if status, body := me(user.String()); status != 200 || body != user.String() {
		t.Fatalf("valid session: %d %q, want 200 %q", status, body, user)
	}
	for _, bad := range []string{"not-a-uuid", uuid.Nil.String()} {
		if status, _ := me(bad); status != fiber.StatusUnauthorized {
			t.Fatalf("session user %q: %d, want 401", bad, status)
		}
	}
}
