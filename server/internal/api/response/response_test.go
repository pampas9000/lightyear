package response

import (
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/requestid"
)

// A 500 carries the request ID, never the internal error.
func TestRespondInternalHidesErrorAndCarriesRequestID(t *testing.T) {
	app := fiber.New()
	app.Use(requestid.New())
	app.Get("/", func(c fiber.Ctx) error {
		return RespondInternal(c, "Failed to fetch task.", errors.New(`pq: relation "jobs" does not exist`))
	})
	res, err := app.Test(httptest.NewRequest("GET", "/", nil))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	var body struct {
		Code    string            `json:"code"`
		Message string            `json:"message"`
		Details map[string]string `json:"details"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	rid := res.Header.Get(fiber.HeaderXRequestID)
	if res.StatusCode != 500 || body.Code != "error.internal" || body.Message != "Failed to fetch task." ||
		rid == "" || body.Details["request_id"] != rid || strings.Contains(string(raw), "relation") {
		t.Fatalf("%d %s (X-Request-ID %q)", res.StatusCode, raw, rid)
	}
}
