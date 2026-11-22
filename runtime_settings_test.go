package main

import (
	"context"
	"testing"
	"time"
)

func baselineConfig() Config {
	return Config{
		TrySandbox:              true,
		TrySandboxTTL:           24 * time.Hour,
		BuilderSignupOpen:       false,
		BuilderSignupDailyLimit: 50,
		DemoSeed:                true,
		WebhookDelivery:         false,
		FreeTierMaxApps:         3,
		FreeTierMaxUsers:        1000,
		FreeTierMaxRecords:      2000,
		FreeTierMaxRecordBytes:  65536,
		VibeMaxSandboxes:        1000,
		VibeDailyLimit:          500,
		VibeShareTTL:            72 * time.Hour,
	}
}

func TestResolvedSettingsFromConfigMirrorsConfig(t *testing.T) {
	resolved := resolvedSettingsFromConfig(baselineConfig())
	if !resolved.TrySandbox || resolved.BuilderSignupOpen || !resolved.DemoSeed || resolved.WebhookDelivery {
		t.Fatalf("boolean settings did not mirror config: %+v", resolved)
	}
	if resolved.FreeTierMaxApps != 3 || resolved.VibeDailyLimit != 500 {
		t.Fatalf("integer settings did not mirror config: %+v", resolved)
	}
	if resolved.TrySandboxTTL != 24*time.Hour || resolved.VibeShareTTL != 72*time.Hour {
		t.Fatalf("duration settings did not mirror config: %+v", resolved)
	}
	if resolved.TrySandboxTTLString != "24h0m0s" || resolved.VibeShareTTLString != "72h0m0s" {
		t.Fatalf("duration string mirrors wrong: %q / %q", resolved.TrySandboxTTLString, resolved.VibeShareTTLString)
	}
}

func TestMergeSettingsAppliesOverrides(t *testing.T) {
	open := true
	trySandbox := false
	limit := 9
	ttl := "6h"
	overrides := settingsOverrides{
		TrySandbox:              &trySandbox,
		BuilderSignupOpen:       &open,
		BuilderSignupDailyLimit: &limit,
		TrySandboxTTL:           &ttl,
	}
	resolved := mergeSettings(baselineConfig(), overrides)
	if resolved.TrySandbox {
		t.Fatal("trySandbox override (false) was not applied")
	}
	if !resolved.BuilderSignupOpen {
		t.Fatal("builderSignupOpen override (true) was not applied")
	}
	if resolved.BuilderSignupDailyLimit != 9 {
		t.Fatalf("builderSignupDailyLimit = %d, want 9", resolved.BuilderSignupDailyLimit)
	}
	if resolved.TrySandboxTTL != 6*time.Hour {
		t.Fatalf("trySandboxTTL = %v, want 6h", resolved.TrySandboxTTL)
	}
	// Untouched fields keep the baseline.
	if resolved.VibeDailyLimit != 500 {
		t.Fatalf("untouched vibeDailyLimit = %d, want 500", resolved.VibeDailyLimit)
	}
}

func TestMergeSettingsIgnoresInvalidDuration(t *testing.T) {
	bad := "not-a-duration"
	negative := "-5h"
	overrides := settingsOverrides{TrySandboxTTL: &bad, VibeShareTTL: &negative}
	resolved := mergeSettings(baselineConfig(), overrides)
	if resolved.TrySandboxTTL != 24*time.Hour {
		t.Fatalf("invalid trySandboxTTL override should fall back to config, got %v", resolved.TrySandboxTTL)
	}
	if resolved.VibeShareTTL != 72*time.Hour {
		t.Fatalf("non-positive vibeShareTTL override should fall back to config, got %v", resolved.VibeShareTTL)
	}
}

func TestClampSandboxTTL(t *testing.T) {
	if got := clampSandboxTTL(0, 24*time.Hour); got != 24*time.Hour {
		t.Fatalf("zero ttl = %v, want fallback 24h", got)
	}
	if got := clampSandboxTTL(-time.Hour, 24*time.Hour); got != 24*time.Hour {
		t.Fatalf("negative ttl = %v, want fallback 24h", got)
	}
	if got := clampSandboxTTL(2*time.Hour, 24*time.Hour); got != 2*time.Hour {
		t.Fatalf("normal ttl = %v, want 2h", got)
	}
	if got := clampSandboxTTL(1000*time.Hour, 24*time.Hour); got != vibeShareMaxTTL {
		t.Fatalf("oversized ttl = %v, want cap %v", got, vibeShareMaxTTL)
	}
}

func TestMergeSettingsClampsOversizedTTLOverride(t *testing.T) {
	huge := "1000h"
	resolved := mergeSettings(baselineConfig(), settingsOverrides{VibeShareTTL: &huge})
	if resolved.VibeShareTTL != vibeShareMaxTTL {
		t.Fatalf("oversized vibeShareTTL override = %v, want cap %v", resolved.VibeShareTTL, vibeShareMaxTTL)
	}
}

func TestEffectiveSettingsNilStoreUsesConfig(t *testing.T) {
	app := &application{config: baselineConfig()}
	if app.settings != nil {
		t.Fatal("expected a nil settings store for this test")
	}
	resolved := app.effectiveSettings(context.Background())
	if resolved.FreeTierMaxApps != 3 || !resolved.TrySandbox {
		t.Fatalf("nil-store effectiveSettings should mirror config: %+v", resolved)
	}
	if !app.demoSeedEnabled(context.Background()) {
		t.Fatal("demoSeedEnabled should reflect config DemoSeed=true with a nil store")
	}
}

func TestFreeTierLimitsFromSettings(t *testing.T) {
	limits := freeTierLimitsFromSettings(resolvedSettingsFromConfig(baselineConfig()))
	if limits.MaxUsers != 1000 || limits.MaxRecordsPerNamespace != 2000 || limits.MaxRecordBytes != 65536 {
		t.Fatalf("free-tier limits from settings wrong: %+v", limits)
	}
}
