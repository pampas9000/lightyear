package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"transcoder/server/internal/models"
	"transcoder/server/internal/services/auth"
	"transcoder/server/internal/testutil"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/session"
)

type fixedProvider struct{ user auth.OAuthUser }

func (p *fixedProvider) GetLoginURL(state string) string {
	return "https://provider.test/authorize?state=" + url.QueryEscape(state)
}

func (p *fixedProvider) Exchange(context.Context, string) (*auth.OAuthUser, error) {
	u := p.user
	return &u, nil
}

func TestAuthHandlerSessionAndOAuthRedirects(t *testing.T) {
	db := testutil.OpenDatabase(t)
	store := session.NewStore(session.Config{CookieHTTPOnly: true})
	h := &AuthHandler{
		service: auth.NewServiceWithProviders(db, map[string]auth.OAuthProvider{
			"taken":      &fixedProvider{auth.OAuthUser{ID: "1", Email: "alice@example.com", Name: "x", Verified: true}},
			"unverified": &fixedProvider{auth.OAuthUser{ID: "2", Email: "bob@example.com", Name: "bob", Verified: false}},
			"ok":         &fixedProvider{auth.OAuthUser{ID: "3", Email: "carol@example.com", Name: "carol", Verified: true}},
		}),
		store:       store,
		frontendURL: "http://localhost:3000",
	}
	app := fiber.New()
	h.Install(app.Group("/api"))

	do := func(req *http.Request) (*http.Response, string) {
		t.Helper()
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		return res, string(raw)
	}
	sessionCookie := func(res *http.Response) *http.Cookie {
		for _, c := range res.Cookies() {
			if c.Name == "session_id" {
				return c
			}
		}
		return nil
	}

	// Register: the session ID is only in the HttpOnly cookie.
	req := httptest.NewRequest(http.MethodPost, "/api/register", strings.NewReader(`{"username":"alice","email":"alice@example.com","password":"password123"}`))
	req.Header.Set("Content-Type", "application/json")
	res, raw := do(req)
	ck := sessionCookie(res)
	if res.StatusCode != 200 || ck == nil || !ck.HttpOnly {
		t.Fatalf("register: %d cookie=%v %s", res.StatusCode, ck, raw)
	}
	var body struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body.Data["token"]; ok || strings.Contains(raw, ck.Value) {
		t.Fatalf("register response exposes the session ID: %s", raw)
	}

	// oauth starts a flow and follows its callback, returning the redirect target.
	oauth := func(provider string, tamperState bool) *url.URL {
		t.Helper()
		res, raw := do(httptest.NewRequest(http.MethodGet, "/api/auth/"+provider, nil))
		var start struct {
			Data struct {
				URL string `json:"url"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(raw), &start); err != nil || res.StatusCode != 200 {
			t.Fatalf("start %s: %d %s", provider, res.StatusCode, raw)
		}
		loginURL, _ := url.Parse(start.Data.URL)
		state := loginURL.Query().Get("state")
		if tamperState {
			state = "forged"
		}
		cb := httptest.NewRequest(http.MethodGet, "/api/auth/"+provider+"/callback?code=c&state="+url.QueryEscape(state), nil)
		if c := sessionCookie(res); c != nil {
			cb.AddCookie(c)
		}
		res, raw = do(cb)
		if res.StatusCode < 300 || res.StatusCode >= 400 {
			t.Fatalf("callback %s: %d %s; want a redirect", provider, res.StatusCode, raw)
		}
		loc, err := url.Parse(res.Header.Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		return loc
	}

	for provider, want := range map[string]string{"taken": "email_taken", "unverified": "email_unverified"} {
		loc := oauth(provider, false)
		if loc.Host != "localhost:3000" || loc.Query().Get("auth_error") != want {
			t.Errorf("%s: redirected to %s, want auth_error=%s", provider, loc, want)
		}
	}
	if loc := oauth("ok", true); loc.Query().Get("auth_error") != "invalid_state" {
		t.Errorf("forged state: redirected to %s", loc)
	}
	var bindings int64
	db.Model(&models.UserOauthAccount{}).Count(&bindings)
	if bindings != 0 {
		t.Fatalf("failed flows created %d bindings", bindings)
	}
	if loc := oauth("ok", false); loc.String() != "http://localhost:3000" {
		t.Errorf("success: redirected to %s", loc)
	}
}
