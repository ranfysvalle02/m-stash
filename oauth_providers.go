package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const oauthMaxResponseBytes = 64 * 1024

// oauthHTTPClient is the dedicated outbound client for provider calls: a tight
// timeout, bounded reads at the call sites, and no redirect following (a token
// or userinfo endpoint answering with a 3xx is treated as a failure).
func oauthHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("oauth client does not follow redirects")
		},
	}
}

// newOAuthAdapters builds one adapter per fully-configured provider. A provider
// with only half a credential pair never reaches here — loadConfig fails boot.
func newOAuthAdapters(cfg Config) map[string]OAuthAdapter {
	adapters := map[string]OAuthAdapter{}
	client := oauthHTTPClient()
	if cfg.GitHubClientID != "" && cfg.GitHubClientSecret != "" {
		adapters["github"] = &githubOAuthAdapter{
			clientID:     cfg.GitHubClientID,
			clientSecret: cfg.GitHubClientSecret,
			httpClient:   client,
		}
	}
	if cfg.GoogleClientID != "" && cfg.GoogleClientSecret != "" {
		adapters["google"] = &googleOAuthAdapter{
			clientID:     cfg.GoogleClientID,
			clientSecret: cfg.GoogleClientSecret,
			httpClient:   client,
		}
	}
	return adapters
}

// oauthPostForm posts a urlencoded body expecting a JSON response, bounding the
// read so a hostile or broken endpoint can't exhaust memory.
func oauthPostForm(ctx context.Context, client *http.Client, endpoint string, form url.Values, out any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	return oauthDoJSON(client, request, out)
}

// oauthGetJSON performs a bearer-authenticated GET expecting JSON.
func oauthGetJSON(ctx context.Context, client *http.Client, endpoint, bearer, accept string, out any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+bearer)
	if accept == "" {
		accept = "application/json"
	}
	request.Header.Set("Accept", accept)
	return oauthDoJSON(client, request, out)
}

func oauthDoJSON(client *http.Client, request *http.Request, out any) error {
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body := io.LimitReader(response.Body, oauthMaxResponseBytes)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, body)
		return fmt.Errorf("oauth endpoint returned status %d", response.StatusCode)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, body)
		return nil
	}
	return json.NewDecoder(body).Decode(out)
}

// --- GitHub ----------------------------------------------------------------

type githubOAuthAdapter struct {
	clientID     string
	clientSecret string
	httpClient   *http.Client

	// Endpoints are overridable so tests can point at an httptest server.
	authorizeEndpoint string
	tokenEndpoint     string
	userEndpoint      string
	emailsEndpoint    string
}

func (adapter *githubOAuthAdapter) Name() string { return "github" }

func (adapter *githubOAuthAdapter) authorizeURL() string {
	if adapter.authorizeEndpoint != "" {
		return adapter.authorizeEndpoint
	}
	return "https://github.com/login/oauth/authorize"
}

func (adapter *githubOAuthAdapter) tokenURL() string {
	if adapter.tokenEndpoint != "" {
		return adapter.tokenEndpoint
	}
	return "https://github.com/login/oauth/access_token"
}

func (adapter *githubOAuthAdapter) userURL() string {
	if adapter.userEndpoint != "" {
		return adapter.userEndpoint
	}
	return "https://api.github.com/user"
}

func (adapter *githubOAuthAdapter) emailsURL() string {
	if adapter.emailsEndpoint != "" {
		return adapter.emailsEndpoint
	}
	return "https://api.github.com/user/emails"
}

func (adapter *githubOAuthAdapter) AuthorizeURL(callbackURL, state, codeChallenge string) string {
	query := url.Values{}
	query.Set("client_id", adapter.clientID)
	query.Set("redirect_uri", callbackURL)
	query.Set("scope", "read:user user:email")
	query.Set("state", state)
	query.Set("response_type", "code")
	query.Set("code_challenge", codeChallenge)
	query.Set("code_challenge_method", "S256")
	query.Set("allow_signup", "true")
	return adapter.authorizeURL() + "?" + query.Encode()
}

func (adapter *githubOAuthAdapter) Exchange(ctx context.Context, callbackURL, code, codeVerifier string) (VerifiedIdentity, error) {
	form := url.Values{}
	form.Set("client_id", adapter.clientID)
	form.Set("client_secret", adapter.clientSecret)
	form.Set("code", code)
	form.Set("redirect_uri", callbackURL)
	form.Set("grant_type", "authorization_code")
	form.Set("code_verifier", codeVerifier)

	var tokenResponse struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		Error       string `json:"error"`
	}
	if err := oauthPostForm(ctx, adapter.httpClient, adapter.tokenURL(), form, &tokenResponse); err != nil {
		return VerifiedIdentity{}, err
	}
	if tokenResponse.AccessToken == "" {
		return VerifiedIdentity{}, fmt.Errorf("github token exchange failed: %s", tokenResponse.Error)
	}

	var profile struct {
		ID        int64  `json:"id"`
		Login     string `json:"login"`
		Name      string `json:"name"`
		AvatarURL string `json:"avatar_url"`
	}
	if err := oauthGetJSON(ctx, adapter.httpClient, adapter.userURL(), tokenResponse.AccessToken, "application/vnd.github+json", &profile); err != nil {
		return VerifiedIdentity{}, err
	}
	if profile.ID == 0 {
		return VerifiedIdentity{}, errors.New("github profile missing id")
	}

	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := oauthGetJSON(ctx, adapter.httpClient, adapter.emailsURL(), tokenResponse.AccessToken, "application/vnd.github+json", &emails); err != nil {
		return VerifiedIdentity{}, err
	}
	email, verified := selectGitHubEmail(emails)

	displayName := profile.Name
	if displayName == "" {
		displayName = profile.Login
	}
	return VerifiedIdentity{
		Provider:      "github",
		Subject:       strconv.FormatInt(profile.ID, 10),
		Email:         email,
		EmailVerified: verified,
		DisplayName:   displayName,
		AvatarURL:     profile.AvatarURL,
		UsernameHint:  profile.Login,
	}, nil
}

// selectGitHubEmail prefers the primary verified address, then any verified
// address. Unverified addresses are never returned as verified.
func selectGitHubEmail(emails []struct {
	Email    string `json:"email"`
	Primary  bool   `json:"primary"`
	Verified bool   `json:"verified"`
}) (string, bool) {
	var firstVerified string
	for _, entry := range emails {
		if !entry.Verified || entry.Email == "" {
			continue
		}
		if entry.Primary {
			return entry.Email, true
		}
		if firstVerified == "" {
			firstVerified = entry.Email
		}
	}
	if firstVerified != "" {
		return firstVerified, true
	}
	return "", false
}

// --- Google ----------------------------------------------------------------

type googleOAuthAdapter struct {
	clientID     string
	clientSecret string
	httpClient   *http.Client

	authorizeEndpoint string
	tokenEndpoint     string
	userInfoEndpoint  string
}

func (adapter *googleOAuthAdapter) Name() string { return "google" }

func (adapter *googleOAuthAdapter) authorizeURL() string {
	if adapter.authorizeEndpoint != "" {
		return adapter.authorizeEndpoint
	}
	return "https://accounts.google.com/o/oauth2/v2/auth"
}

func (adapter *googleOAuthAdapter) tokenURL() string {
	if adapter.tokenEndpoint != "" {
		return adapter.tokenEndpoint
	}
	return "https://oauth2.googleapis.com/token"
}

func (adapter *googleOAuthAdapter) userInfoURL() string {
	if adapter.userInfoEndpoint != "" {
		return adapter.userInfoEndpoint
	}
	return "https://openidconnect.googleapis.com/v1/userinfo"
}

func (adapter *googleOAuthAdapter) AuthorizeURL(callbackURL, state, codeChallenge string) string {
	query := url.Values{}
	query.Set("client_id", adapter.clientID)
	query.Set("redirect_uri", callbackURL)
	query.Set("response_type", "code")
	query.Set("scope", "openid email profile")
	query.Set("state", state)
	query.Set("code_challenge", codeChallenge)
	query.Set("code_challenge_method", "S256")
	query.Set("access_type", "online")
	query.Set("prompt", "select_account")
	return adapter.authorizeURL() + "?" + query.Encode()
}

func (adapter *googleOAuthAdapter) Exchange(ctx context.Context, callbackURL, code, codeVerifier string) (VerifiedIdentity, error) {
	form := url.Values{}
	form.Set("client_id", adapter.clientID)
	form.Set("client_secret", adapter.clientSecret)
	form.Set("code", code)
	form.Set("redirect_uri", callbackURL)
	form.Set("grant_type", "authorization_code")
	form.Set("code_verifier", codeVerifier)

	var tokenResponse struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := oauthPostForm(ctx, adapter.httpClient, adapter.tokenURL(), form, &tokenResponse); err != nil {
		return VerifiedIdentity{}, err
	}
	if tokenResponse.AccessToken == "" {
		return VerifiedIdentity{}, fmt.Errorf("google token exchange failed: %s", tokenResponse.Error)
	}

	var profile struct {
		Subject       string `json:"sub"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
		Picture       string `json:"picture"`
	}
	if err := oauthGetJSON(ctx, adapter.httpClient, adapter.userInfoURL(), tokenResponse.AccessToken, "", &profile); err != nil {
		return VerifiedIdentity{}, err
	}
	if profile.Subject == "" {
		return VerifiedIdentity{}, errors.New("google userinfo missing sub")
	}

	usernameHint := profile.Name
	if at := strings.IndexByte(profile.Email, '@'); usernameHint == "" && at > 0 {
		usernameHint = profile.Email[:at]
	}
	return VerifiedIdentity{
		Provider:      "google",
		Subject:       profile.Subject,
		Email:         profile.Email,
		EmailVerified: profile.EmailVerified,
		DisplayName:   profile.Name,
		AvatarURL:     profile.Picture,
		UsernameHint:  usernameHint,
	}, nil
}
