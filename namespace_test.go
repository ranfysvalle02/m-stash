package main

import (
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestValidateNamespaceSlug(t *testing.T) {
	valid := normalizeNamespaceSlug(" Ada_Lovelace ")
	if valid != "ada_lovelace" {
		t.Fatalf("normalized username = %q, want ada_lovelace", valid)
	}
	if err := validateNamespaceSlug(valid); err != nil {
		t.Fatalf("valid username rejected: %v", err)
	}

	for _, value := range []string{
		"ab",
		"Ada",
		"ada.dev",
		"api",
		"with spaces",
		"ada-",
		"_ada",
		"ada--lovelace",
		"ada__lovelace",
		"ada-_lovelace",
		"ada-lovelacé",
	} {
		if err := validateNamespaceSlug(value); err == nil {
			t.Errorf("invalid username %q was accepted", value)
		}
	}
}

func TestValidateResourcePathSegments(t *testing.T) {
	if err := validateResourceType(normalizeResourcePathSegment("Reel")); err != nil {
		t.Fatalf("valid resource type rejected: %v", err)
	}
	if err := validateResourceSlug(normalizeResourcePathSegment("Demo-Reel")); err != nil {
		t.Fatalf("valid resource slug rejected: %v", err)
	}

	for _, value := range []string{"ab", "demo_reel", "-demo", "demo-", "demo--reel"} {
		if err := validateResourceSlug(value); err == nil {
			t.Errorf("invalid resource slug %q was accepted", value)
		}
	}
}

func TestNewPersonalNamespaceCreatesOwnerMembership(t *testing.T) {
	appID := bson.NewObjectID()
	userID := bson.NewObjectID()
	now := time.Date(2026, time.September, 28, 12, 0, 0, 0, time.UTC)
	namespace := newPersonalNamespace(appID, userID, "ada-lovelace", now)
	membership := newOwnerMembership(appID, namespace.ID, userID, now)

	if namespace.ID.IsZero() || namespace.AppID != appID || namespace.OwnerID != userID || namespace.Slug != "ada-lovelace" || namespace.Kind != personalNamespaceKind || !namespace.IsPublic {
		t.Fatalf("personal namespace = %#v", namespace)
	}
	if membership.NamespaceID != namespace.ID || membership.AppID != appID || membership.UserID != userID || membership.Role != namespaceOwnerRole {
		t.Fatalf("owner membership = %#v", membership)
	}
}

func TestSharedNamespaceAccessRules(t *testing.T) {
	adminClaims := &Claims{Role: "admin"}
	memberClaims := &Claims{Role: "user"}
	if !canWriteSharedNamespace(adminClaims) || canWriteSharedNamespace(memberClaims) || canWriteSharedNamespace(nil) {
		t.Fatal("shared namespace write access must be limited to administrators")
	}
	if filter := visibleToAuthenticatedUser(adminClaims); len(filter) != 0 {
		t.Fatalf("admin shared visibility filter = %#v, want unrestricted", filter)
	}
	filter := visibleToAuthenticatedUser(memberClaims)
	values, ok := filter["$in"].(bson.A)
	if !ok || len(values) != 2 || values[0] != authenticatedVisibility || values[1] != publicVisibility {
		t.Fatalf("member shared visibility filter = %#v", filter)
	}
}

func TestNewSharedNamespaceIsDeploymentManagedAndPrivateByDefault(t *testing.T) {
	appID := bson.NewObjectID()
	namespace := newSharedNamespace(appID, time.Now().UTC())
	if namespace.Kind != sharedNamespaceKind || namespace.AppID != appID || namespace.Slug != sharedNamespaceSlug || !namespace.OwnerID.IsZero() || namespace.IsPublic {
		t.Fatalf("shared namespace = %#v", namespace)
	}
}

func TestNewWelcomeResourceIsPublicAndOwned(t *testing.T) {
	appID := bson.NewObjectID()
	namespaceID := bson.NewObjectID()
	userID := bson.NewObjectID()
	now := time.Now().UTC()
	resource := newWelcomeResource(appID, namespaceID, userID, "ada", now)
	if resource.Type != "record" || resource.Slug != "welcome" {
		t.Fatalf("seed type/slug = %s/%s, want record/welcome", resource.Type, resource.Slug)
	}
	if resource.Visibility != publicVisibility {
		t.Fatalf("seed visibility = %s, want %s", resource.Visibility, publicVisibility)
	}
	if resource.AppID != appID || resource.NamespaceID != namespaceID || resource.CreatorID != userID {
		t.Fatalf("seed ownership fields = %#v", resource)
	}
	if resource.Title == "" || resource.Content == "" {
		t.Fatal("seed should include a title and content")
	}
}
