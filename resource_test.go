package main

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestParseResourcePath(t *testing.T) {
	typeName, slug, ok := parseResourcePath("/v1/me/resources/Reel/demo-reel", "/v1/me/resources/")
	if !ok || typeName != "reel" || slug != "demo-reel" {
		t.Fatalf("resource path = (%q, %q, %t)", typeName, slug, ok)
	}

	typeName, slug, ok = parseResourcePath("/v1/me/resources/reel", "/v1/me/resources/")
	if !ok || typeName != "reel" || slug != "" {
		t.Fatalf("resource collection path = (%q, %q, %t)", typeName, slug, ok)
	}

	for _, path := range []string{
		"/v1/me/resources/reel/demo/extra",
		"/v1/me/resources/reel/demo_reel",
		"/v1/me/resources/ab",
	} {
		if _, _, ok := parseResourcePath(path, "/v1/me/resources/"); ok {
			t.Errorf("invalid resource path %q was accepted", path)
		}
	}
}

func TestResourceInputNormalizeAndValidate(t *testing.T) {
	input := resourceInput{
		Slug:       " Demo-Reel ",
		Title:      " Demo reel ",
		Summary:    " First public project ",
		Visibility: "PUBLIC",
	}
	if err := input.normalizeAndValidate(true); err != nil {
		t.Fatalf("valid resource input rejected: %v", err)
	}
	if input.Slug != "demo-reel" || input.Title != "Demo reel" || input.Visibility != publicVisibility {
		t.Fatalf("normalized resource input = %#v", input)
	}

	for _, input := range []resourceInput{
		{Slug: "demo", Title: "", Visibility: privateVisibility},
		{Slug: "demo", Title: "Demo", Visibility: "shared"},
		{Slug: "demo_reel", Title: "Demo", Visibility: privateVisibility},
	} {
		if err := input.normalizeAndValidate(true); err == nil {
			t.Errorf("invalid resource input %#v was accepted", input)
		}
	}
}

func TestResourceInputAllowsAuthenticatedVisibility(t *testing.T) {
	input := resourceInput{Slug: "game-state", Title: "Game state", Visibility: authenticatedVisibility}
	if err := input.normalizeAndValidate(true); err != nil {
		t.Fatalf("authenticated visibility rejected: %v", err)
	}
}

func TestAuthenticatedVisibilityIsRestrictedToSharedNamespaces(t *testing.T) {
	if err := validateResourceVisibilityForNamespace(Namespace{Kind: personalNamespaceKind}, authenticatedVisibility); err == nil {
		t.Fatal("personal namespace accepted authenticated visibility")
	}
	if err := validateResourceVisibilityForNamespace(Namespace{Kind: sharedNamespaceKind}, authenticatedVisibility); err != nil {
		t.Fatalf("shared namespace rejected authenticated visibility: %v", err)
	}
}

func TestCreatorIDForServiceClaimsUsesSentinelID(t *testing.T) {
	creatorID, err := creatorIDForClaims(&Claims{Role: sharedServiceRole})
	if err != nil || creatorID != bson.NilObjectID {
		t.Fatalf("service creator ID = %s, %v", creatorID, err)
	}
}
