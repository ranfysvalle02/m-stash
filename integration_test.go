package main

// Integration tests run the real HTTP handler against a real MongoDB replica
// set (transactions are required). They are skipped unless
// MSTASH_TEST_MONGO_URI is set, so `go test ./...` stays fast and offline:
//
//	MSTASH_TEST_MONGO_URI="mongodb://localhost:27017/?replicaSet=rs0" go test -run Integration ./...
//
// or `make test-integration`, which starts a throwaway replica set in Docker.
// CI always runs them. Each test gets its own database, dropped afterwards.

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"golang.org/x/crypto/bcrypt"
)

// fakeIdP stands in for GitHub: each authorization "code" maps to an identity.
type fakeIdP struct{ identities map[string]VerifiedIdentity }

func (fakeIdP) Name() string { return "github" }
func (fakeIdP) AuthorizeURL(_, state, _ string) string {
	return "https://idp.test/authorize?state=" + url.QueryEscape(state)
}
func (f fakeIdP) Exchange(_ context.Context, _, code, _ string) (VerifiedIdentity, error) {
	return f.identities[code], nil
}

type harness struct {
	t      *testing.T
	ctx    context.Context
	app    *application
	db     *mongo.Database
	server *httptest.Server
}

func newHarness(t *testing.T, identities map[string]VerifiedIdentity) *harness {
	t.Helper()
	uri := os.Getenv("MSTASH_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("set MSTASH_TEST_MONGO_URI to a MongoDB replica set to run integration tests")
	}
	ctx := context.Background()
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	suffix, _ := randomHex(6)
	db := client.Database("itest_" + suffix)
	t.Cleanup(func() {
		_ = db.Drop(context.Background())
		_ = client.Disconnect(context.Background())
	})
	if _, err := db.Collection("_users").Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "appId", Value: 1}, {Key: "email", Value: 1}}, Options: options.Index().SetUnique(true),
	}); err != nil {
		t.Fatal(err)
	}
	for _, ensure := range []func(context.Context, *mongo.Database) error{
		ensureAppIndexes, ensureNamespaceIndexes, ensureOutboxIndexes, ensureAuthRateLimitIndexes,
		ensureSessionIndexes, ensureWebhookIndexes, ensureVibeShareIndexes, ensureVibeRunIndexes, ensureOAuthIndexes,
	} {
		if err := ensure(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	cfg := Config{
		JWTSecret: "integration-secret-integration-secret-0123456789", JWTIssuer: "m-stash", JWTAudience: "m-stash-api",
		AccessTokenTTL: 15 * time.Minute, RefreshSessionTTL: 24 * time.Hour, AllowedOrigins: []string{"*"},
		BuilderSignupOpen: true, BuilderSignupDailyLimit: 1000, FreeTierMaxApps: 3,
		TrySandbox: true, TrySandboxTTL: 24 * time.Hour, VibeShareTTL: 72 * time.Hour, VibeMaxSandboxes: 100, VibeDailyLimit: 1000,
		VibeLearn: true, PublicStats: true,
	}
	app := newApplication(cfg, db, client)
	app.oauthAdapters = map[string]OAuthAdapter{"github": fakeIdP{identities: identities}}
	limiter := newAuthRateLimiter(db, 10_000, time.Minute)
	app.vibeLimiter, app.vibeDailyLimiter, app.builderSignupLimiter = limiter, limiter, limiter
	app.builderSignupDailyLimiter, app.tryLimiter, app.oauthLimiter = limiter, limiter, limiter
	server := httptest.NewServer(app.Handler(limiter))
	t.Cleanup(server.Close)
	return &harness{t: t, ctx: ctx, app: app, db: db, server: server}
}

// browser is a cookie-carrying client that never follows redirects, so tests
// can inspect every hop of a flow.
type browser struct {
	h      *harness
	client *http.Client
	bearer string
	appKey string
}

func (h *harness) browser() *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{h: h, client: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

type response struct {
	status int
	body   []byte
	header http.Header
}

func (r response) json() map[string]any {
	var out map[string]any
	_ = json.Unmarshal(r.body, &out)
	return out
}

func (r response) data() map[string]any {
	data, _ := r.json()["data"].(map[string]any)
	return data
}

func (b *browser) do(method, path string, body any) response {
	b.h.t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, _ := json.Marshal(body)
		reader = bytes.NewReader(encoded)
	}
	request, _ := http.NewRequest(method, b.h.server.URL+path, reader)
	request.Header.Set("Content-Type", "application/json")
	if b.bearer != "" {
		request.Header.Set("Authorization", "Bearer "+b.bearer)
	}
	if b.appKey != "" {
		request.Header.Set(appKeyHeader, b.appKey)
	}
	if token := b.cookie("m_stash_csrf"); token != "" {
		request.Header.Set("X-CSRF-Token", token)
	}
	resp, err := b.client.Do(request)
	if err != nil {
		b.h.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return response{status: resp.StatusCode, body: raw, header: resp.Header}
}

func (b *browser) cookie(name string) string {
	base, _ := url.Parse(b.h.server.URL)
	for _, cookie := range b.client.Jar.Cookies(base) {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

func expectStatus(t *testing.T, label string, got response, want int) {
	t.Helper()
	if got.status != want {
		t.Fatalf("%s: status %d, want %d: %s", label, got.status, want, got.body)
	}
}

// socialLogin drives console start -> provider -> callback and returns where
// the browser lands.
func (b *browser) socialLogin(code, next string) *url.URL {
	b.h.t.Helper()
	start := b.do("GET", "/v1/auth/oauth/github/start?console=1&next="+url.QueryEscape(next), nil)
	expectStatus(b.h.t, "oauth start", start, http.StatusFound)
	authorize, _ := url.Parse(start.header.Get("Location"))
	callback := b.do("GET", "/v1/auth/oauth/github/callback?code="+url.QueryEscape(code)+"&state="+url.QueryEscape(authorize.Query().Get("state")), nil)
	expectStatus(b.h.t, "oauth callback", callback, http.StatusFound)
	landed, _ := url.Parse(callback.header.Get("Location"))
	return landed
}

// socialSignIn completes a console social login and returns the exchange body.
func (b *browser) socialSignIn(code string) map[string]any {
	b.h.t.Helper()
	landed := b.socialLogin(code, "/console")
	if landed.Query().Get("oauth_code") == "" {
		b.h.t.Fatalf("no oauth_code: %s", landed)
	}
	exchange := b.do("POST", "/v1/builder/auth/oauth/exchange", map[string]any{"code": landed.Query().Get("oauth_code")})
	expectStatus(b.h.t, "builder exchange", exchange, http.StatusOK)
	return exchange.json()
}

func (b *browser) passwordBuilder(email string) string {
	b.h.t.Helper()
	signup := b.do("POST", "/v1/builder/auth/signup", map[string]any{"email": email, "password": "builder-password-123"})
	expectStatus(b.h.t, "builder signup", signup, http.StatusCreated)
	return signup.json()["token"].(string)
}

func (h *harness) operator() *browser {
	h.t.Helper()
	hash, _ := bcrypt.GenerateFromPassword([]byte("operator-password-123"), bcrypt.MinCost)
	if _, err := h.db.Collection("_users").InsertOne(h.ctx, User{ID: bson.NewObjectID(), AppID: bson.NilObjectID, Email: "op@example.com", PasswordHash: string(hash), Role: operatorRole, CreatedAt: time.Now().UTC()}); err != nil {
		h.t.Fatal(err)
	}
	op := h.browser()
	expectStatus(h.t, "operator login", op.do("POST", "/v1/builder/auth/login", map[string]any{"email": "op@example.com", "password": "operator-password-123"}), http.StatusOK)
	return op
}

func (h *harness) vibeSandbox() *vibeSandbox {
	h.t.Helper()
	sandbox, err := h.app.provisionVibeSandbox(h.ctx, appVibe{Spec: finalizeRecipe(vibeRecipes[0].build()), Idea: "an arcade", Source: vibeSourceRecipe, CreatedAt: time.Now()})
	if err != nil {
		h.t.Fatal(err)
	}
	return sandbox
}

// appLogin signs in to an app (not the console) and returns the bearer token.
func (h *harness) appLogin(publishableKey, email, password string) response {
	client := h.browser()
	client.appKey = publishableKey
	return client.do("POST", "/v1/auth/login", map[string]any{"email": email, "password": password})
}

// ---------------------------------------------------------------------------

func TestIntegrationBuilderSocialLogin(t *testing.T) {
	h := newHarness(t, map[string]VerifiedIdentity{
		"ada":      {Subject: "gh-ada", Email: "Ada@Example.com", EmailVerified: true},
		"pwclash":  {Subject: "gh-pw", Email: "pw@example.com", EmailVerified: true},
		"unverify": {Subject: "gh-unv", Email: "x@example.com", EmailVerified: false},
		"late":     {Subject: "gh-late", Email: "late@example.com", EmailVerified: true},
	})
	sandbox := h.vibeSandbox()
	pk := sandbox.record.PublishableKey

	t.Run("new builder lands back on the keep link with a one-time code", func(t *testing.T) {
		visitor := h.browser()
		landed := visitor.socialLogin("ada", "/console?claim="+pk+"&name=Neon+Arcade")
		if landed.Path != "/console" || landed.Query().Get("claim") != pk || landed.Query().Get("name") != "Neon Arcade" {
			t.Fatalf("landed on %s", landed)
		}
		code := landed.Query().Get("oauth_code")
		exchange := visitor.do("POST", "/v1/builder/auth/oauth/exchange", map[string]any{"code": code})
		expectStatus(t, "exchange", exchange, http.StatusOK)
		builder := exchange.json()["builder"].(map[string]any)
		if exchange.json()["isNewUser"] != true || builder["email"] != "ada@example.com" || builder["plan"] != freePlan {
			t.Fatalf("exchange = %s", exchange.body)
		}
		expectStatus(t, "replayed code", visitor.do("POST", "/v1/builder/auth/oauth/exchange", map[string]any{"code": code}), http.StatusUnauthorized)
		me := visitor.do("GET", "/v1/builder/me", nil)
		if me.status != http.StatusOK || me.data()["email"] != "ada@example.com" {
			t.Fatalf("session not established: %s", me.body)
		}
		claim := visitor.do("POST", "/v1/builder/apps/claim", map[string]any{"publishableKey": pk, "claimToken": sandbox.claimToken})
		expectStatus(t, "claim after social signup", claim, http.StatusOK)
	})

	t.Run("returning login is not a new user", func(t *testing.T) {
		if h.browser().socialSignIn("ada")["isNewUser"] != false {
			t.Fatal("second login reported a new user")
		}
	})

	t.Run("console and app tickets are not interchangeable", func(t *testing.T) {
		client := h.browser()
		code := client.socialLogin("ada", "/console").Query().Get("oauth_code")
		client.appKey = pk
		expectStatus(t, "console code at app exchange", client.do("POST", "/v1/auth/oauth/exchange", map[string]any{"code": code}), http.StatusUnauthorized)
	})

	t.Run("redirect can never leave the console", func(t *testing.T) {
		for _, next := range []string{"https://evil.example/steal", "//evil.example/console", "/admin", "/console/../admin"} {
			landed := h.browser().socialLogin("ada", next)
			if landed.Host != strings.TrimPrefix(h.server.URL, "http://") || landed.Path != "/console" {
				t.Fatalf("next=%q escaped to %s", next, landed)
			}
		}
	})

	t.Run("a password builder is never adopted by a provider email", func(t *testing.T) {
		h.browser().passwordBuilder("pw@example.com")
		if got := h.browser().socialLogin("pwclash", "/console").Query().Get("oauth_error"); got != "account_exists_link_required" {
			t.Fatalf("oauth_error = %q", got)
		}
	})

	t.Run("unverified provider email is refused", func(t *testing.T) {
		if got := h.browser().socialLogin("unverify", "/console").Query().Get("oauth_error"); got != "email_unverified" {
			t.Fatalf("oauth_error = %q", got)
		}
	})

	t.Run("closed signup refuses new builders but not existing ones", func(t *testing.T) {
		if _, err := h.db.Collection(runtimeSettingsCollection).UpdateOne(h.ctx, bson.M{"_id": runtimeSettingsDocID},
			bson.M{"$set": bson.M{"builderSignupOpen": false}}, options.UpdateOne().SetUpsert(true)); err != nil {
			t.Fatal(err)
		}
		h.app.settings.refresh(h.ctx)
		t.Cleanup(func() {
			_, _ = h.db.Collection(runtimeSettingsCollection).DeleteOne(h.ctx, bson.M{"_id": runtimeSettingsDocID})
			h.app.settings.refresh(h.ctx)
		})
		if got := h.browser().socialLogin("late", "/console").Query().Get("oauth_error"); got != "signup_closed" {
			t.Fatalf("oauth_error = %q", got)
		}
		h.browser().socialSignIn("ada")
	})
}

func TestIntegrationDeletedSocialBuilderCanReturn(t *testing.T) {
	h := newHarness(t, map[string]VerifiedIdentity{"ada": {Subject: "gh-ada", Email: "ada@example.com", EmailVerified: true}})
	first := h.browser().socialSignIn("ada")["builder"].(map[string]any)["id"].(string)

	op := h.operator()
	expectStatus(t, "operator deletes builder", op.do("DELETE", "/v1/admin/builders/"+first, nil), http.StatusNoContent)
	if left, _ := h.db.Collection(identityCollectionName).CountDocuments(h.ctx, bson.M{"appId": bson.NilObjectID}); left != 0 {
		t.Fatalf("deleting a builder must unlink their social identities, %d left", left)
	}

	again := h.browser().socialSignIn("ada")
	if again["isNewUser"] != true || again["builder"].(map[string]any)["id"] == first {
		t.Fatalf("a deleted builder must be able to sign up again: %v", again)
	}

	// An identity orphaned before the cleanup existed must not lock anyone out.
	ghost := bson.NewObjectID()
	if err := h.app.linkIdentity(h.ctx, bson.NilObjectID, ghost, VerifiedIdentity{Provider: "github", Subject: "gh-ghost", Email: "ghost@example.com", EmailVerified: true}); err != nil {
		t.Fatal(err)
	}
	h.app.oauthAdapters["github"] = fakeIdP{identities: map[string]VerifiedIdentity{"ghost": {Subject: "gh-ghost", Email: "ghost@example.com", EmailVerified: true}}}
	if h.browser().socialSignIn("ghost")["isNewUser"] != true {
		t.Fatal("an orphaned identity should be dropped and the sign-in treated as new")
	}
}

func TestIntegrationOwnerLogin(t *testing.T) {
	h := newHarness(t, nil)
	sandbox := h.vibeSandbox()
	pk := sandbox.record.PublishableKey
	builder := h.browser()
	builder.passwordBuilder("keeper@example.com")
	claim := builder.do("POST", "/v1/builder/apps/claim", map[string]any{"publishableKey": pk, "claimToken": sandbox.claimToken})
	expectStatus(t, "claim", claim, http.StatusOK)
	appID := claim.data()["id"].(string)
	ownerPath := "/v1/builder/apps/" + appID + "/owner"

	owners := builder.do("GET", ownerPath, nil).data()["emails"].([]any)
	if len(owners) != 1 || owners[0] != sandbox.owner.Email {
		t.Fatalf("owners = %v", owners)
	}

	oldOwner := h.appLogin(pk, sandbox.owner.Email, sandbox.owner.Password)
	expectStatus(t, "generated owner before reset", oldOwner, http.StatusOK)
	oldToken := oldOwner.json()["token"].(string)
	// JWT iat has one-second precision; make the reset strictly later.
	time.Sleep(1100 * time.Millisecond)

	expectStatus(t, "set owner", builder.do("PUT", ownerPath, map[string]any{"email": "Me@Example.com", "password": "my-owner-password-1"}), http.StatusOK)

	t.Run("the generated owner is replaced, including tokens already issued", func(t *testing.T) {
		expectStatus(t, "old owner login", h.appLogin(pk, sandbox.owner.Email, sandbox.owner.Password), http.StatusUnauthorized)
		stale := h.browser()
		stale.appKey, stale.bearer = pk, oldToken
		expectStatus(t, "old owner token writes shared data", stale.do("POST", "/v1/shared/resources/scores",
			map[string]any{"slug": "stale", "title": "Stale", "visibility": "public", "data": map[string]any{}}), http.StatusForbidden)
	})

	t.Run("the new owner signs in and writes shared data", func(t *testing.T) {
		login := h.appLogin(pk, "me@example.com", "my-owner-password-1")
		expectStatus(t, "new owner login", login, http.StatusOK)
		owner := h.browser()
		owner.appKey, owner.bearer = pk, login.json()["token"].(string)
		expectStatus(t, "owner shared write", owner.do("POST", "/v1/shared/resources/scores",
			map[string]any{"slug": "top", "title": "Top", "visibility": "public", "data": map[string]any{"n": 1}}), http.StatusCreated)
		if got := builder.do("GET", ownerPath, nil).data()["emails"].([]any); len(got) != 1 || got[0] != "me@example.com" {
			t.Fatalf("owners = %v", got)
		}
	})

	t.Run("guards", func(t *testing.T) {
		member := h.browser()
		member.appKey = pk
		expectStatus(t, "member signup", member.do("POST", "/v1/auth/signup", map[string]any{"email": "member@example.com", "password": "member-password-123", "username": "member1"}), http.StatusCreated)
		expectStatus(t, "member email takeover", builder.do("PUT", ownerPath, map[string]any{"email": "member@example.com", "password": "my-owner-password-1"}), http.StatusConflict)
		expectStatus(t, "sandbox email", builder.do("PUT", ownerPath, map[string]any{"email": "a@sandbox.invalid", "password": "my-owner-password-1"}), http.StatusBadRequest)
		expectStatus(t, "short password", builder.do("PUT", ownerPath, map[string]any{"email": "co@example.com", "password": "short"}), http.StatusBadRequest)
		expectStatus(t, "second owner", builder.do("PUT", ownerPath, map[string]any{"email": "co@example.com", "password": "co-owner-password-1"}), http.StatusOK)
		if got := builder.do("GET", ownerPath, nil).data()["emails"].([]any); len(got) != 2 {
			t.Fatalf("a second owner should be added: %v", got)
		}
		stranger := h.browser()
		stranger.passwordBuilder("stranger@example.com")
		expectStatus(t, "stranger reads owner", stranger.do("GET", ownerPath, nil), http.StatusNotFound)
		expectStatus(t, "stranger sets owner", stranger.do("PUT", ownerPath, map[string]any{"email": "evil@example.com", "password": "evil-password-1234"}), http.StatusNotFound)
	})
}

func TestIntegrationSignupConflictCodes(t *testing.T) {
	h := newHarness(t, nil)
	member := h.browser()
	member.appKey = h.vibeSandbox().record.PublishableKey
	signup := func(email, username string) response {
		return member.do("POST", "/v1/auth/signup", map[string]any{
			"email": email, "password": "member-password-123", "username": username,
		})
	}
	expectStatus(t, "initial signup", signup("member@example.com", "member1"), http.StatusCreated)
	for _, tc := range []struct{ email, username, code string }{
		{"MEMBER@example.com", "other1", "email_taken"},
		{"other@example.com", "MEMBER1", "username_taken"},
		{"member@example.com", "member1", "email_taken"},
	} {
		got := signup(tc.email, tc.username)
		expectStatus(t, tc.code, got, http.StatusConflict)
		if got.json()["code"] != tc.code || got.json()["error"] != "Email or username is already claimed" {
			t.Fatalf("conflict response = %s", got.body)
		}
	}
}

func TestIntegrationOperatorPasswordResetRetiresBuilderTokens(t *testing.T) {
	h := newHarness(t, nil)
	builder := h.browser()
	oldToken := builder.passwordBuilder("reset@example.com")
	id := builder.do("GET", "/v1/builder/me", nil).data()["id"].(string)
	time.Sleep(1100 * time.Millisecond)

	op := h.operator()
	expectStatus(t, "operator resets password", op.do("PATCH", "/v1/admin/builders/"+id, map[string]any{"password": "brand-new-password-1"}), http.StatusOK)

	stale := h.browser()
	stale.bearer = oldToken
	expectStatus(t, "token from before the reset", stale.do("GET", "/v1/builder/me", nil), http.StatusForbidden)
	expectStatus(t, "old password", h.browser().do("POST", "/v1/builder/auth/login", map[string]any{"email": "reset@example.com", "password": "builder-password-123"}), http.StatusUnauthorized)
	fresh := h.browser()
	login := fresh.do("POST", "/v1/builder/auth/login", map[string]any{"email": "reset@example.com", "password": "brand-new-password-1"})
	expectStatus(t, "new password", login, http.StatusOK)
	fresh.bearer = login.json()["token"].(string)
	expectStatus(t, "fresh token", fresh.do("GET", "/v1/builder/me", nil), http.StatusOK)
}

func TestIntegrationVibeLedgerFollowsAnAppToItsClaim(t *testing.T) {
	h := newHarness(t, nil)
	visitor := h.browser()
	launch := visitor.do("POST", "/v1/vibe", map[string]any{"recipe": "storefront"})
	expectStatus(t, "recipe launch", launch, http.StatusOK)
	body := launch.json()
	shareID := body["share"].(map[string]any)["id"].(string)
	pk := body["publishableKey"].(string)
	claimToken := body["claim"].(map[string]any)["token"].(string)

	for _, asset := range []string{"preview", "zip", "skill.md"} {
		expectStatus(t, asset, visitor.do("GET", "/v1/vibe/"+shareID+"/"+asset, nil), http.StatusOK)
	}
	visitor.passwordBuilder("vibe@example.com")
	expectStatus(t, "claim", visitor.do("POST", "/v1/builder/apps/claim", map[string]any{"publishableKey": pk, "claimToken": claimToken}), http.StatusOK)

	var run vibeRun
	if err := h.db.Collection(vibeRunsCollection).FindOne(h.ctx, bson.M{"_id": shareID}).Decode(&run); err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{vibeEventPreview, vibeEventZip, vibeEventSkill, vibeEventClaim} {
		if run.FirstAt[event].IsZero() {
			t.Errorf("run never recorded %q: %+v", event, run.FirstAt)
		}
	}
	if raw, _ := bson.Marshal(run); bytes.Contains(raw, []byte("Riff")) {
		t.Fatal("the run ledger must not store app names or idea text")
	}
	var claimed AppRecord
	if err := h.db.Collection(appsCollectionName).FindOne(h.ctx, bson.M{"publishableKey": pk}).Decode(&claimed); err != nil {
		t.Fatal(err)
	}
	if claimed.Sandbox || claimed.Vibe == nil || len(claimed.Vibe.Spec.Sections) == 0 {
		t.Fatalf("the spec must survive the claim: %+v", claimed.Vibe)
	}

	op := h.operator()
	funnel := op.do("GET", "/v1/admin/overview", nil).data()["vibeFunnel"].(map[string]any)
	cohort := funnel["cohorts"].([]any)[0].(map[string]any)
	if cohort["runs"] != float64(1) || cohort["claimed"] != float64(1) || cohort["claimRate"] != float64(1) {
		t.Fatalf("funnel cohort = %v", cohort)
	}
}

func TestIntegrationBuildKitCarriesShapesNotValues(t *testing.T) {
	h := newHarness(t, nil)
	builder := h.browser()
	builder.passwordBuilder("kit@example.com")
	created := builder.do("POST", "/v1/builder/apps", map[string]any{"slug": "noodle-bar", "displayName": "Noodle Bar", "adminEmail": "owner@noodle.test", "adminPassword": "owner-password-123"})
	expectStatus(t, "create app", created, http.StatusCreated)
	appID := created.data()["id"].(string)
	pk := created.data()["publishableKey"].(string)

	login := h.appLogin(pk, "owner@noodle.test", "owner-password-123")
	owner := h.browser()
	owner.appKey, owner.bearer = pk, login.json()["token"].(string)
	expectStatus(t, "menu", owner.do("POST", "/v1/shared/resources/menu", map[string]any{"slug": "ramen", "title": "Ramen", "visibility": "public",
		"data": map[string]any{"dish": "SECRET-DISH", "price": 14.5, "spicy": false}}), http.StatusCreated)

	kit := builder.do("GET", "/v1/builder/apps/"+appID+"/starter.zip", nil)
	expectStatus(t, "kit", kit, http.StatusOK)
	reader, err := zip.NewReader(bytes.NewReader(kit.body), int64(len(kit.body)))
	if err != nil {
		t.Fatal(err)
	}
	var spec vibeSpec
	for _, file := range reader.File {
		handle, _ := file.Open()
		contents, _ := io.ReadAll(handle)
		handle.Close()
		if bytes.Contains(contents, []byte("SECRET-DISH")) || bytes.Contains(contents, []byte("owner-password-123")) {
			t.Fatalf("%s leaked a record value or credential", file.Name)
		}
		if file.Name == "m-stash-starter/mstash.json" {
			_ = json.Unmarshal(contents, &spec)
		}
	}
	if len(spec.Sections) != 1 || spec.Sections[0].Type != "menu" || spec.Sections[0].Audience != vibeAudienceOwner {
		t.Fatalf("inferred spec = %+v", spec)
	}
	stranger := h.browser()
	stranger.passwordBuilder("other@example.com")
	expectStatus(t, "stranger kit", stranger.do("GET", "/v1/builder/apps/"+appID+"/starter.zip", nil), http.StatusNotFound)
}

func TestIntegrationSecretKeyIsWriteOnly(t *testing.T) {
	h := newHarness(t, nil)
	builder := h.browser()
	builder.passwordBuilder("sk@example.com")
	created := builder.do("POST", "/v1/builder/apps", map[string]any{"slug": "sk-app", "displayName": "SK", "adminEmail": "owner@sk.test", "adminPassword": "owner-password-123"})
	expectStatus(t, "create app", created, http.StatusCreated)
	service := h.browser()
	service.appKey, service.bearer = created.data()["publishableKey"].(string), created.json()["secretKey"].(string)

	expectStatus(t, "secret key creates", service.do("POST", "/v1/shared/resources/menus", map[string]any{"slug": "hidden", "title": "Hidden", "visibility": "private",
		"content": "TOP-SECRET", "data": map[string]any{"likes": 0}}), http.StatusCreated)
	increment := service.do("PATCH", "/v1/shared/resources/menus/hidden", map[string]any{"op": "increment", "field": "data.likes", "by": 0})
	if increment.status != http.StatusNoContent || len(increment.body) != 0 {
		t.Fatalf("secret-key increment must be 204 with no body, got %d %s", increment.status, increment.body)
	}
	expectStatus(t, "secret key read", service.do("GET", "/v1/shared/resources/menus/hidden", nil), http.StatusForbidden)
}

func TestIntegrationPublicStatsCountOnlyLiveApps(t *testing.T) {
	h := newHarness(t, nil)
	h.vibeSandbox() // sandbox users are not stashers
	builder := h.browser()
	builder.passwordBuilder("stats@example.com")
	created := builder.do("POST", "/v1/builder/apps", map[string]any{"slug": "stats-app", "displayName": "Stats", "adminEmail": "owner@stats.test", "adminPassword": "owner-password-123"})
	expectStatus(t, "create app", created, http.StatusCreated)

	stats := h.browser().do("GET", "/v1/stats", nil)
	expectStatus(t, "stats", stats, http.StatusOK)
	data := stats.data()
	if data["builders"] != float64(1) || data["appUsers"] != float64(1) || data["stashers"] != float64(2) || data["liveApps"] != float64(1) {
		t.Fatalf("stats = %v", data)
	}
	for _, forbidden := range []string{"stats@example.com", "owner@stats.test", "Stats"} {
		if bytes.Contains(stats.body, []byte(forbidden)) {
			t.Fatalf("stats leaked %q", forbidden)
		}
	}
}
