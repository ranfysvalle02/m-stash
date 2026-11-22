package main

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// handleTry provisions a blank "try it" sandbox: a real, isolated app with no
// seed data and no admin, so a visitor can run the whole Register-to-public API
// loop against a live backend before deciding to keep it. It mirrors the vibe
// lab's ephemeral tenant but without a generated spec, and returns the one-time
// claim token so the sandbox can later be promoted to a live app in place.
func (app *application) handleTry(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if !app.effectiveSettings(r.Context()).TrySandbox {
		writeError(w, http.StatusNotFound, "The try-it sandbox is not enabled on this deployment")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	// The resolved TrySandboxTTL is already capped by the shared ceiling, so the
	// store never accumulates long-lived throwaway tenants.
	provision, err := app.provisionSandbox(ctx, sandboxOptions{
		DisplayName: "Try m-stash",
		TTL:         app.effectiveSettings(ctx).TrySandboxTTL,
	})
	if err != nil {
		if errors.Is(err, errVibeSandboxFull) {
			writeError(w, http.StatusServiceUnavailable, "The try-it playground is full right now. Please try again in a few minutes.")
			return
		}
		app.logger.Error("try sandbox provisioning failed", "request_id", requestIDFromContext(ctx), "error", err)
		writeError(w, http.StatusServiceUnavailable, "Could not start a sandbox")
		return
	}

	app.metrics.recordTrySandbox()
	w.Header().Set("Cache-Control", "no-store")
	// The claim token is returned once, here, and never persisted anywhere a later
	// reader (share GET, zip) could retrieve it. The publishable key is not a
	// secret; the claim token is what authorises keeping the app.
	writeJSON(w, http.StatusCreated, map[string]any{
		"publishableKey": provision.record.PublishableKey,
		"expiresAt":      provision.record.ExpiresAt,
		"apiBase":        app.publicBaseURL(r),
		"claim":          map[string]any{"token": provision.claimToken},
	})
}
