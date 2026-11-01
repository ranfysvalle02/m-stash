package main

import "testing"

func TestValidateVibeIdea(t *testing.T) {
	if _, err := validateVibeIdea("  "); err == nil {
		t.Fatal("blank idea should be rejected")
	}
	if _, err := validateVibeIdea("ok"); err == nil {
		t.Fatal("too-short idea should be rejected")
	}
	long := make([]byte, vibeMaxIdeaLength+1)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := validateVibeIdea(string(long)); err == nil {
		t.Fatal("over-length idea should be rejected")
	}
	cleaned, err := validateVibeIdea("  a workout tracker with saved routines  ")
	if err != nil {
		t.Fatalf("valid idea rejected: %v", err)
	}
	if cleaned != "a workout tracker with saved routines" {
		t.Fatalf("idea not trimmed: %q", cleaned)
	}
}

func TestSanitizeVibePlanBoundsEverything(t *testing.T) {
	bigPurpose := make([]byte, 400)
	for i := range bigPurpose {
		bigPurpose[i] = 'x'
	}
	plan := vibePlan{
		Summary:   "  a summary  ",
		DataModel: make([]vibeModelPart, 20),
		Snippets:  make([]vibeSnippet, 20),
	}
	for i := range plan.DataModel {
		plan.DataModel[i] = vibeModelPart{Name: "n", Purpose: string(bigPurpose)}
	}
	cleaned := sanitizeVibePlan(plan)
	if cleaned.Summary != "a summary" {
		t.Fatalf("summary not trimmed: %q", cleaned.Summary)
	}
	if len(cleaned.DataModel) != 8 {
		t.Fatalf("data model not capped: got %d", len(cleaned.DataModel))
	}
	if len(cleaned.Snippets) != 4 {
		t.Fatalf("snippets not capped: got %d", len(cleaned.Snippets))
	}
	if len(cleaned.DataModel[0].Purpose) > 200 {
		t.Fatalf("purpose not clamped: got %d", len(cleaned.DataModel[0].Purpose))
	}
}

func TestParseVibeResponseExtractsPlan(t *testing.T) {
	raw := []byte(`{"candidates":[{"content":{"parts":[{"text":"{\"summary\":\"Backend for a notes app\",\"dataModel\":[{\"name\":\"Note\",\"scope\":\"personal\",\"type\":\"note\",\"visibility\":\"private\",\"purpose\":\"a user's note\"}],\"snippets\":[{\"title\":\"Save\",\"language\":\"javascript\",\"code\":\"fetch('/v1/me/resources/note')\"}],\"whyMStash\":\"ownership enforced\"}"}]}}]}`)
	plan, err := parseVibeResponse(raw)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if plan.Summary != "Backend for a notes app" {
		t.Fatalf("summary = %q", plan.Summary)
	}
	if len(plan.DataModel) != 1 || plan.DataModel[0].Type != "note" {
		t.Fatalf("data model = %#v", plan.DataModel)
	}
	if len(plan.Snippets) != 1 || plan.Snippets[0].Language != "javascript" {
		t.Fatalf("snippets = %#v", plan.Snippets)
	}
}

func TestParseVibeResponseRejectsEmpty(t *testing.T) {
	if _, err := parseVibeResponse([]byte(`{"candidates":[]}`)); err == nil {
		t.Fatal("empty candidates should error")
	}
	if _, err := parseVibeResponse([]byte(`{"candidates":[{"content":{"parts":[{"text":"{}"}]}}]}`)); err == nil {
		t.Fatal("empty plan should error")
	}
}
