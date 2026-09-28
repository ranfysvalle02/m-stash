package main

import (
	"context"
	"errors"
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
	NamespaceID bson.ObjectID `bson:"namespaceId" json:"namespaceId"`
	UserID      bson.ObjectID `bson:"userId" json:"userId"`
	Role        string        `bson:"role" json:"role"`
	CreatedAt   time.Time     `bson:"createdAt" json:"createdAt"`
}

type Resource struct {
	ID          bson.ObjectID `bson:"_id,omitempty" json:"id"`
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

func newPersonalNamespace(userID bson.ObjectID, username string, now time.Time) Namespace {
	return Namespace{
		ID:          bson.NewObjectID(),
		Slug:        username,
		Kind:        personalNamespaceKind,
		OwnerID:     userID,
		DisplayName: username,
		IsPublic:    true,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func newSharedNamespace(now time.Time) Namespace {
	return Namespace{
		ID:          bson.NewObjectID(),
		Slug:        sharedNamespaceSlug,
		Kind:        sharedNamespaceKind,
		DisplayName: "Shared space",
		IsPublic:    false,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func (app *application) sharedNamespace(ctx context.Context) (Namespace, error) {
	var namespace Namespace
	err := app.database.Collection(namespaceCollectionName).FindOne(ctx, bson.M{"kind": sharedNamespaceKind}).Decode(&namespace)
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

func newOwnerMembership(namespaceID, userID bson.ObjectID, now time.Time) NamespaceMember {
	return NamespaceMember{
		ID:          bson.NewObjectID(),
		NamespaceID: namespaceID,
		UserID:      userID,
		Role:        namespaceOwnerRole,
		CreatedAt:   now,
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
		membership := newOwnerMembership(namespace.ID, user.ID, namespace.CreatedAt)
		if _, err := app.database.Collection(namespaceMembershipCollection).InsertOne(transactionContext, membership); err != nil {
			return nil, err
		}
		return nil, nil
	}, options.Transaction().SetWriteConcern(writeconcern.Majority()))
	return err
}

func ensureNamespaceIndexes(ctx context.Context, database *mongo.Database) error {
	if _, err := database.Collection(namespaceCollectionName).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "slug", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "ownerId", Value: 1}}, Options: options.Index().SetUnique(true).SetPartialFilterExpression(bson.M{"kind": personalNamespaceKind})},
		{Keys: bson.D{{Key: "kind", Value: 1}}, Options: options.Index().SetUnique(true).SetPartialFilterExpression(bson.M{"kind": sharedNamespaceKind})},
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
	}); err != nil {
		return err
	}
	return nil
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
