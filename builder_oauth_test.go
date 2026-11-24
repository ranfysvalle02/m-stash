package main

import (
	"context"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// The post-login redirect is built from this, so it must never leave /console.
func TestConsoleReturnPathStaysOnTheConsole(t *testing.T) {
	cases := map[string]string{
		"":                                     "/console",
		"/console":                             "/console",
		"/console?claim=pk_abc&name=Riff+Shop": "/console?claim=pk_abc&name=Riff+Shop",
		"https://evil.example/console":         "/console",
		"//evil.example/console":               "/console",
		"/admin":                               "/console",
		"/console/../admin":                    "/console",
		"javascript:alert(1)":                  "/console",
	}
	for input, want := range cases {
		if got := consoleReturnPath(input); got != want {
			t.Errorf("consoleReturnPath(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestBuilderSignupErrorsMapToConsoleCodes(t *testing.T) {
	if oauthErrorCode(errBuilderSignupClosed) != "signup_closed" || oauthErrorCode(errBuilderSignupFull) != "signup_capacity" {
		t.Fatal("builder signup errors need their own oauth_error codes")
	}
}

// A reset (owner replaced, password changed) must retire tokens issued before
// it while leaving tokens from the new login working.
func TestTokensIssuedBeforeACredentialResetAreRetired(t *testing.T) {
	reset := time.Date(2026, 11, 24, 5, 0, 0, 500_000_000, time.UTC)
	if !tokenSurvivesReset(jwt.NewNumericDate(reset.Add(-time.Hour)), nil) {
		t.Fatal("an account that was never reset keeps its tokens")
	}
	if tokenSurvivesReset(jwt.NewNumericDate(reset.Add(-time.Second)), &reset) {
		t.Fatal("a token from before the reset must be refused")
	}
	if !tokenSurvivesReset(jwt.NewNumericDate(reset), &reset) || !tokenSurvivesReset(jwt.NewNumericDate(reset.Add(time.Minute)), &reset) {
		t.Fatal("a token issued at or after the reset must work")
	}
	if tokenSurvivesReset(nil, &reset) {
		t.Fatal("a token without iat cannot prove it is newer than the reset")
	}
}

func TestMemberTokensSkipThePrivilegedCheck(t *testing.T) {
	app := &application{}
	if ok, err := app.privilegedTokenCurrent(context.Background(), &Claims{Role: "user", UID: "x"}); !ok || err != nil {
		t.Fatal("member tokens must not need an account read")
	}
}
