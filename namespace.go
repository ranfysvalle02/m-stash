package main

import (
	"context"
	"errors"
	"log"
	"strconv"
	"strings"
	"time"
	"unicode"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

const (
	namespaceCollectionName       = "_m_stash_namespaces"
	namespaceMembershipCollection = "_m_stash_namespace_members"
	resourceCollectionName        = "resources"

	personalNamespaceKind   = "personal"
	sharedNamespaceKind     = "shared"
	sharedNamespaceSlug     = "shared"
	namespaceOwnerRole      = "owner"
	privateVisibility       = "private"
	authenticatedVisibility = "authenticated"
	publicVisibility        = "public"
)

var reservedNamespaceSlugs = map[string]struct{}{
	"api": {}, "app": {}, "discover": {}, "guide": {}, "healthz": {}, "login": {},
	"logout": {}, "metrics": {}, "public": {}, "readyz": {}, "signup": {}, "v1": {},
	sharedNamespaceSlug: {},
}

type Namespace struct {
	ID          bson.ObjectID `bson:"_id,omitempty" json:"id"`
	AppID       bson.ObjectID `bson:"appId" json:"-"`
	Slug        string        `bson:"slug" json:"slug"`
	Kind        string        `bson:"kind" json:"kind"`
	OwnerID     bson.ObjectID `bson:"ownerId" json:"-"`
	DisplayName string        `bson:"displayName" json:"displayName"`
	Bio         string        `bson:"bio,omitempty" json:"bio,omitempty"`
	AvatarURL   string        `bson:"avatarURL,omitempty" json:"avatarURL,omitempty"`
	IsPublic    bool          `bson:"isPublic" json:"isPublic"`
	CreatedAt   time.Time     `bson:"createdAt" json:"createdAt"`
	UpdatedAt   time.Time     `bson:"updatedAt" json:"updatedAt"`
}

type NamespaceMember struct {
	ID          bson.ObjectID `bson:"_id,omitempty" json:"id"`
	AppID       bson.ObjectID `bson:"appId" json:"-"`
	NamespaceID bson.ObjectID `bson:"namespaceId" json:"namespaceId"`
	UserID      bson.ObjectID `bson:"userId" json:"userId"`
	Role        string        `bson:"role" json:"role"`
	CreatedAt   time.Time     `bson:"createdAt" json:"createdAt"`
}

type Resource struct {
	ID          bson.ObjectID `bson:"_id,omitempty" json:"id"`
	AppID       bson.ObjectID `bson:"appId" json:"-"`
	NamespaceID bson.ObjectID `bson:"namespaceId" json:"namespaceId"`
	CreatorID   bson.ObjectID `bson:"creatorId" json:"creatorId"`
	Type        string        `bson:"type" json:"type"`
	Slug        string        `bson:"slug" json:"slug"`
	Title       string        `bson:"title" json:"title"`
	Summary     string        `bson:"summary,omitempty" json:"summary,omitempty"`
	Content     string        `bson:"content,omitempty" json:"content,omitempty"`
	Data        bson.M        `bson:"data,omitempty" json:"data,omitempty"`
	Visibility  string        `bson:"visibility" json:"visibility"`
	CreatedAt   time.Time     `bson:"createdAt" json:"createdAt"`
	UpdatedAt   time.Time     `bson:"updatedAt" json:"updatedAt"`
}

func newPersonalNamespace(appID, userID bson.ObjectID, username string, now time.Time) Namespace {
	return Namespace{
		ID:          bson.NewObjectID(),
		AppID:       appID,
		Slug:        username,
		Kind:        personalNamespaceKind,
		OwnerID:     userID,
		DisplayName: username,
		IsPublic:    true,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func newSharedNamespace(appID bson.ObjectID, now time.Time) Namespace {
	return Namespace{
		ID:          bson.NewObjectID(),
		AppID:       appID,
		Slug:        sharedNamespaceSlug,
		Kind:        sharedNamespaceKind,
		DisplayName: "Shared space",
		IsPublic:    false,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func (app *application) sharedNamespace(ctx context.Context) (Namespace, error) {
	filter := bson.M{"kind": sharedNamespaceKind}
	if appID, ok := appIDFromContext(ctx); ok {
		filter["appId"] = appID
	}
	var namespace Namespace
	err := app.database.Collection(namespaceCollectionName).FindOne(ctx, filter).Decode(&namespace)
	return namespace, err
}

func canWriteSharedNamespace(claims *Claims) bool {
	return claims != nil && (claims.Role == "admin" || claims.Role == sharedServiceRole)
}

func visibleToAuthenticatedUser(claims *Claims) bson.M {
	if canWriteSharedNamespace(claims) {
		return bson.M{}
	}
	return bson.M{"$in": bson.A{authenticatedVisibility, publicVisibility}}
}

func newOwnerMembership(appID, namespaceID, userID bson.ObjectID, now time.Time) NamespaceMember {
	return NamespaceMember{
		ID:          bson.NewObjectID(),
		AppID:       appID,
		NamespaceID: namespaceID,
		UserID:      userID,
		Role:        namespaceOwnerRole,
		CreatedAt:   now,
	}
}

// newWelcomeResource seeds a new account with a live, editable public record so
// the landing dashboard is never empty and the user has a shareable link
// (/{username}/record/welcome) the moment they sign in.
func newWelcomeResource(appID, namespaceID, userID bson.ObjectID, username string, now time.Time) Resource {
	return Resource{
		ID:          bson.NewObjectID(),
		AppID:       appID,
		NamespaceID: namespaceID,
		CreatorID:   userID,
		Type:        "record",
		Slug:        "welcome",
		Title:       "Welcome to your space",
		Summary:     "A sample public record — edit the text or delete it anytime.",
		Content: "# Hi, I’m @" + username + "\n\n" +
			"m-stash created this sample record for you. It’s **public**, so anyone can read it at your link above.\n\n" +
			"- Edit this text and press **Save**.\n" +
			"- Switch visibility to **Private** to hide it.\n" +
			"- Create more records from your dashboard.\n\n" +
			"Everything here is yours, served straight from your own space.",
		Visibility: publicVisibility,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

func (app *application) createAccountWithPersonalNamespace(ctx context.Context, user User, namespace Namespace) error {
	session, err := app.mongoClient.StartSession()
	if err != nil {
		return err
	}
	defer session.EndSession(ctx)

	_, err = session.WithTransaction(ctx, func(transactionContext context.Context) (any, error) {
		if _, err := app.database.Collection("_users").InsertOne(transactionContext, user); err != nil {
			return nil, err
		}
		if _, err := app.database.Collection(namespaceCollectionName).InsertOne(transactionContext, namespace); err != nil {
			return nil, err
		}
		membership := newOwnerMembership(namespace.AppID, namespace.ID, user.ID, namespace.CreatedAt)
		if _, err := app.database.Collection(namespaceMembershipCollection).InsertOne(transactionContext, membership); err != nil {
			return nil, err
		}
		seed := newWelcomeResource(namespace.AppID, namespace.ID, user.ID, namespace.Slug, namespace.CreatedAt)
		if _, err := app.database.Collection(resourceCollectionName).InsertOne(transactionContext, seed); err != nil {
			return nil, err
		}
		return nil, nil
	}, options.Transaction().SetWriteConcern(writeconcern.Majority()))
	return err
}

func ensureNamespaceIndexes(ctx context.Context, database *mongo.Database) error {
	if err := dropLegacySingleTenantIndexes(ctx, database); err != nil {
		return err
	}
	if _, err := database.Collection(namespaceCollectionName).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "appId", Value: 1}, {Key: "slug", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "appId", Value: 1}, {Key: "ownerId", Value: 1}}, Options: options.Index().SetUnique(true).SetPartialFilterExpression(bson.M{"kind": personalNamespaceKind})},
		{Keys: bson.D{{Key: "appId", Value: 1}, {Key: "kind", Value: 1}}, Options: options.Index().SetUnique(true).SetPartialFilterExpression(bson.M{"kind": sharedNamespaceKind})},
	}); err != nil {
		return err
	}
	if _, err := database.Collection(namespaceMembershipCollection).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "namespaceId", Value: 1}, {Key: "userId", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "role", Value: 1}}},
	}); err != nil {
		return err
	}
	if _, err := database.Collection(resourceCollectionName).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "namespaceId", Value: 1}, {Key: "type", Value: 1}, {Key: "slug", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "namespaceId", Value: 1}, {Key: "type", Value: 1}, {Key: "visibility", Value: 1}, {Key: "createdAt", Value: -1}, {Key: "_id", Value: -1}}},
		{Keys: bson.D{{Key: "title", Value: "text"}, {Key: "summary", Value: "text"}, {Key: "content", Value: "text"}}, Options: options.Index().SetName("resources_text")},
	}); err != nil {
		return err
	}
	return nil
}

// dropLegacySingleTenantIndexes removes global unique indexes left over from the
// pre-multi-tenant schema. They enforced uniqueness across all apps and would
// reject per-app documents that legitimately share a slug/email/type. Missing
// indexes are ignored, so this is safe to run on every boot.
func dropLegacySingleTenantIndexes(ctx context.Context, database *mongo.Database) error {
	legacy := map[string][]string{
		namespaceCollectionName: {"slug_1", "ownerId_1", "kind_1"},
		"_users":                {"email_1"},
		resourceCollectionName:  {"type_1_slug_1", "slug_1"},
	}
	for collection, names := range legacy {
		for _, name := range names {
			if err := database.Collection(collection).Indexes().DropOne(ctx, name); err != nil {
				if isIndexNotFoundError(err) {
					continue
				}
				return err
			}
			log.Printf("🧹 Dropped legacy index %s.%s", collection, name)
		}
	}
	return nil
}

func isIndexNotFoundError(err error) bool {
	var commandErr mongo.CommandError
	if errors.As(err, &commandErr) {
		return commandErr.Code == 27
	}
	return strings.Contains(strings.ToLower(err.Error()), "index not found")
}

func normalizeNamespaceSlug(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func validateNamespaceSlug(value string) error {
	if len(value) < 3 || len(value) > 32 {
		return errors.New("username must contain 3-32 lowercase letters, numbers, hyphens, or underscores")
	}
	if _, reserved := reservedNamespaceSlugs[value]; reserved {
		return errors.New("username is reserved")
	}
	if !isASCIIAlphaNumeric(value[0]) || !isASCIIAlphaNumeric(value[len(value)-1]) {
		return errors.New("username must begin and end with a lowercase letter or number")
	}
	previousWasSeparator := false
	for _, character := range []byte(value) {
		isSeparator := character == '-' || character == '_'
		if !isASCIIAlphaNumeric(character) && !isSeparator {
			return errors.New("username must contain 3-32 lowercase letters, numbers, hyphens, or underscores")
		}
		if isSeparator && previousWasSeparator {
			return errors.New("username cannot contain adjacent hyphens or underscores")
		}
		previousWasSeparator = isSeparator
	}
	return nil
}

func isASCIIAlphaNumeric(character byte) bool {
	return character >= 'a' && character <= 'z' || character >= '0' && character <= '9'
}

func normalizeResourcePathSegment(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func validateResourceType(value string) error {
	return validateResourcePathSegment(value, "resource type", 3, 32)
}

func validateResourceSlug(value string) error {
	return validateResourcePathSegment(value, "resource slug", 3, 64)
}

func validateResourcePathSegment(value, label string, minimum, maximum int) error {
	if len(value) < minimum || len(value) > maximum {
		return errors.New(label + " must contain " + strconv.Itoa(minimum) + "-" + strconv.Itoa(maximum) + " lowercase letters, numbers, or hyphens")
	}
	if strings.HasPrefix(value, "-") || strings.HasSuffix(value, "-") || strings.Contains(value, "--") {
		return errors.New(label + " must not start, end, or repeat a hyphen")
	}
	for _, character := range value {
		if !unicode.IsLower(character) && !unicode.IsDigit(character) && character != '-' {
			return errors.New(label + " must contain " + strconv.Itoa(minimum) + "-" + strconv.Itoa(maximum) + " lowercase letters, numbers, or hyphens")
		}
	}
	return nil
}
