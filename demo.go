package main

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// demoRecord is a storytelling public record seeded into the default app's
// shared namespace so the live demo at /public is never empty — every record
// is a real API response that sells the product while it demonstrates it.
type demoRecord struct {
	Type    string
	Slug    string
	Title   string
	Summary string
	Content string
	Data    bson.M
}

// demoRecords returns the canonical demo set. They are re-asserted on every boot
// while DEMO_DATA is on, and wiped on the first boot after it is turned off.
func demoRecords() []demoRecord {
	return []demoRecord{
		{
			Type:    "config",
			Slug:    "release",
			Title:   "Live deployment config",
			Summary: "The public config this demo serves to every client — the exact JSON you'd fetch, no key required.",
			Content: "# This is a real API response\n\n" +
				"You're reading a **public `config` record**, served straight from the m-stash API. " +
				"No database wiring, no security rules — the deployment published it, and anyone can read it:\n\n" +
				"```bash\ncurl \"$API_URL/v1/public/shared/resources/config/release\"\n```\n\n" +
				"Point the same call at *your* app and this is how clients read your public settings.",
			Data: bson.M{
				"product":               "m-stash",
				"tagline":               "Stash it. ’Stache it.",
				"pitch":                 "Auth + private per-user data for AI-built apps.",
				"database":              "MongoDB, kept behind the service",
				"wiringRequired":        false,
				"securityRulesRequired": false,
			},
		},
		{
			Type:    "feature-flag",
			Slug:    "public-demo",
			Title:   "This demo is a feature flag",
			Summary: "The whole demo is one flag. Set DEMO_DATA=off, redeploy, and every record here clears itself.",
			Content: "# A flag, flipped on\n\n" +
				"Everything you see in this demo exists because one environment variable is on:\n\n" +
				"```bash\nDEMO_DATA=on    # default — the storytelling demo is visible\nDEMO_DATA=off   # next boot wipes it clean\n```\n\n" +
				"Ship your real app with `DEMO_DATA=off` and `/public` starts empty, waiting for the records *you* choose to publish.",
			Data: bson.M{
				"key":      "public-demo",
				"enabled":  true,
				"default":  true,
				"env":      "DEMO_DATA",
				"audience": "everyone",
			},
		},
		{
			Type:    "catalog",
			Slug:    "what-you-get",
			Title:   "What m-stash hands your app",
			Summary: "The backend you skip building — shaped like the catalog records you'd use for plans, menus, or inventory.",
			Content: "# The boring backend, already done\n\n" +
				"Point an AI-built app at m-stash and this is what shows up for free — " +
				"the risky, repetitive parts of a backend, handled server-side.",
			Data: bson.M{
				"items": bson.A{
					bson.M{"name": "Auth", "detail": "Sign-up, login, sessions — your UI, our identity."},
					bson.M{"name": "Per-user data", "detail": "The server scopes every record to its owner, so a browser can't read someone else's."},
					bson.M{"name": "Public buckets", "detail": "Deployment-owned data any client can read — like this record."},
					bson.M{"name": "MongoDB", "detail": "Underneath it all, never exposed to the frontend."},
				},
			},
		},
		{
			Type:    "leaderboard",
			Slug:    "chores-eliminated",
			Title:   "Backend chores you'll never do again",
			Summary: "A server-computed leaderboard record — the ranked work m-stash does so you don't have to.",
			Content: "# Ranked by how much you'll miss them\n\n" +
				"Spoiler: you won't. This is the per-user, security-sensitive plumbing m-stash owns on your behalf.",
			Data: bson.M{
				"entries": bson.A{
					bson.M{"rank": 1, "chore": "Hashing and verifying passwords"},
					bson.M{"rank": 2, "chore": "Scoping every query to the signed-in user"},
					bson.M{"rank": 3, "chore": "Writing and debugging access-control rules"},
					bson.M{"rank": 4, "chore": "Handing database credentials to a frontend"},
				},
			},
		},
		{
			Type:    "metrics",
			Slug:    "outcomes",
			Title:   "m-stash by the numbers",
			Summary: "The outcomes a buyer actually measures — quantified, and served as live JSON.",
			Content: "# The metrics that matter\n\n" +
				"Not a feature list — outcomes. This is the record a decision-maker screenshots.",
			Data: bson.M{
				"timeToFirstAuth":              "~12 minutes",
				"securityRulesWritten":         0,
				"databaseCredentialsInBrowser": 0,
				"backendFilesYouMaintain":      0,
				"tenantsIsolatedByDefault":     true,
				"escapeHatch":                  "self-host + direct MongoDB access",
			},
		},
	}
}

// syncDemoData reconciles the demo set for the default app's shared namespace.
// When DEMO_DATA is on it upserts the canonical records (public, deployment
// owned); when off it removes every record it previously seeded. It is
// idempotent and safe to run on every boot.
func (app *application) syncDemoData(ctx context.Context, appID bson.ObjectID) error {
	var shared Namespace
	err := app.database.Collection(namespaceCollectionName).
		FindOne(ctx, bson.M{"appId": appID, "kind": sharedNamespaceKind}).Decode(&shared)
	if err != nil {
		return err
	}

	resources := app.database.Collection(resourceCollectionName)
	if !app.config.DemoSeed {
		_, err := resources.DeleteMany(ctx, bson.M{"namespaceId": shared.ID, "demo": true})
		return err
	}

	now := time.Now().UTC()
	for _, record := range demoRecords() {
		filter := bson.M{"namespaceId": shared.ID, "type": record.Type, "slug": record.Slug}
		update := bson.M{
			"$set": bson.M{
				"appId":      appID,
				"creatorId":  shared.ID, // deployment-owned demo content, no end user
				"title":      record.Title,
				"summary":    record.Summary,
				"content":    record.Content,
				"data":       record.Data,
				"visibility": publicVisibility,
				"demo":       true,
				"updatedAt":  now,
			},
			"$setOnInsert": bson.M{"createdAt": now},
		}
		if _, err := resources.UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(true)); err != nil {
			return err
		}
	}
	return nil
}
