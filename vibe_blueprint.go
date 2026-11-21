package main

import "strings"

// Blueprints are full app specs chosen by keyword. They are the deterministic
// path: the lab answers with a complete, seeded app even when the model is rate
// limited, over budget, or disabled. Both paths produce a vibeSpec and run
// through validateVibeSpec, so the output quality does not depend on the model.

type vibeTemplateRule struct {
	keywords []string
	build    func() vibeSpec
}

var vibeTemplateRules = []vibeTemplateRule{
	{
		keywords: []string{"store", "shop", "music", "record", "vinyl", "guitar", "instrument", "menu", "bakery", "cafe", "coffee", "inventory", "retail", "boutique", "merch", "catalog", "product", "market", "goods"},
		build:    storefrontTemplate,
	},
	{
		keywords: []string{"habit", "streak", "routine", "workout", "fitness", "exercise", "track", "log", "water", "meditation"},
		build:    trackerTemplate,
	},
	{
		keywords: []string{"note", "journal", "diary", "writing", "memo", "idea", "thought"},
		build:    notesTemplate,
	},
	{
		keywords: []string{"game", "score", "leaderboard", "rank", "high score", "tournament", "arcade", "puzzle", "player"},
		build:    leaderboardTemplate,
	},
	{
		keywords: []string{"event", "gig", "concert", "meetup", "calendar", "schedule", "class", "booking", "rsvp", "workshop"},
		build:    eventsTemplate,
	},
	{
		keywords: []string{"collection", "bookmark", "library", "gallery", "photo", "playlist", "saved", "wishlist", "recipe", "cook"},
		build:    collectionTemplate,
	},
	{
		keywords: []string{"profile", "portfolio", "bio", "creator", "link in bio", "social", "resume"},
		build:    profileTemplate,
	},
}

// buildVibeBlueprintSpec picks the closest template for an idea, with a generic
// fallback, and returns a validated spec marked as blueprint-sourced.
func titleFromIdea(idea, fallback string) string {
	idea = strings.TrimSpace(idea)
	if len(idea) == 0 {
		return fallback
	}
	words := strings.Fields(idea)
	if len(words) > 4 {
		return fallback
	}
	return strings.Title(strings.ToLower(idea))
}

func buildVibeBlueprintSpec(idea string) vibeSpec {
	lowered := strings.ToLower(idea)
	for _, rule := range vibeTemplateRules {
		for _, keyword := range rule.keywords {
			if strings.Contains(lowered, keyword) {
				return finalizeTemplate(rule.build(), idea)
			}
		}
	}
	return finalizeTemplate(defaultTemplate(), idea)
}

func finalizeTemplate(spec vibeSpec, idea string) vibeSpec {
	spec.Source = vibeSourceBlueprint
	spec.Name = titleFromIdea(idea, spec.Name)
	return validateVibeSpec(spec)
}

func storefrontTemplate() vibeSpec {
	return vibeSpec{
		Name:    "Storefront",
		Tagline: "Your shop, online in minutes",
		Accent:  "amber",
		Icon:    "cart",
		Sections: []vibeSection{
			{
				Type: "catalog", Title: "Catalog", Audience: vibeAudienceOwner, View: "grid",
				Fields: []vibeField{
					{Key: "name", Label: "Name", Kind: "text"},
					{Key: "price", Label: "Price", Kind: "price"},
					{Key: "category", Label: "Category", Kind: "tag"},
					{Key: "description", Label: "Description", Kind: "longtext"},
				},
				Samples: []map[string]any{
					{"name": "1965 Fender Stratocaster", "price": 2400.0, "category": []string{"guitars", "vintage"}, "description": "Sunburst finish, freshly set up. A real collector's piece."},
					{"name": "Pink Floyd - The Wall (Vinyl)", "price": 34.99, "category": []string{"vinyl", "rock"}, "description": "180g reissue, gatefold sleeve, near mint."},
					{"name": "Shure SM58 Microphone", "price": 99.0, "category": []string{"gear"}, "description": "The live-vocal workhorse, brand new in box."},
				},
			},
			{
				Type: "events", Title: "In-store gigs", Audience: vibeAudienceOwner, View: "timeline",
				Fields: []vibeField{
					{Key: "act", Label: "Act", Kind: "text"},
					{Key: "date", Label: "Date", Kind: "date"},
					{Key: "details", Label: "Details", Kind: "longtext"},
				},
				Samples: []map[string]any{
					{"act": "Open Mic Night", "date": "2025-02-14", "details": "Sign-ups at 7pm, music at 8. All levels welcome."},
					{"act": "The Basement Trio (Live)", "date": "2025-02-21", "details": "Local jazz, free entry, limited seating."},
				},
			},
			{
				Type: "wishlist", Title: "My wishlist", Audience: vibeAudienceMember, View: "list",
				Fields: []vibeField{
					{Key: "item", Label: "Item", Kind: "text"},
					{Key: "note", Label: "Note", Kind: "longtext"},
				},
			},
		},
	}
}

func trackerTemplate() vibeSpec {
	return vibeSpec{
		Name:    "Streaks",
		Tagline: "Build habits that stick",
		Accent:  "emerald",
		Icon:    "dumbbell",
		Sections: []vibeSection{
			{
				Type: "habit", Title: "Habits", Audience: vibeAudienceMember, View: "checklist",
				Fields: []vibeField{
					{Key: "name", Label: "Habit", Kind: "text"},
					{Key: "streak", Label: "Streak", Kind: "number"},
					{Key: "done_today", Label: "Done today", Kind: "toggle"},
					{Key: "notes", Label: "Notes", Kind: "longtext"},
				},
			},
		},
	}
}

func notesTemplate() vibeSpec {
	return vibeSpec{
		Name:    "Notes",
		Tagline: "A private home for your thoughts",
		Accent:  "slate",
		Icon:    "note",
		Sections: []vibeSection{
			{
				Type: "note", Title: "Notes", Audience: vibeAudienceMember, View: "list",
				Fields: []vibeField{
					{Key: "title", Label: "Title", Kind: "text"},
					{Key: "body", Label: "Body", Kind: "longtext"},
					{Key: "tags", Label: "Tags", Kind: "tag"},
				},
			},
		},
	}
}

func leaderboardTemplate() vibeSpec {
	return vibeSpec{
		Name:    "Arcade",
		Tagline: "Play, save, and climb the ranks",
		Accent:  "rose",
		Icon:    "trophy",
		Sections: []vibeSection{
			{
				Type: "save", Title: "My save slots", Audience: vibeAudienceMember, View: "list",
				Fields: []vibeField{
					{Key: "slot", Label: "Slot", Kind: "text"},
					{Key: "level", Label: "Level", Kind: "number"},
					{Key: "score", Label: "Score", Kind: "number"},
				},
			},
			{
				Type: "leaderboard", Title: "Leaderboard", Audience: vibeAudienceOwner, View: "ranked",
				Fields: []vibeField{
					{Key: "player", Label: "Player", Kind: "text"},
					{Key: "score", Label: "Score", Kind: "number"},
				},
				Samples: []map[string]any{
					{"player": "ace_of_space", "score": 98400.0},
					{"player": "pixelpirate", "score": 87150.0},
					{"player": "retro_rhonda", "score": 76020.0},
				},
			},
		},
	}
}

func eventsTemplate() vibeSpec {
	return vibeSpec{
		Name:    "Events",
		Tagline: "Plan it, share it, fill the room",
		Accent:  "teal",
		Icon:    "calendar",
		Sections: []vibeSection{
			{
				Type: "events", Title: "Upcoming events", Audience: vibeAudienceOwner, View: "timeline",
				Fields: []vibeField{
					{Key: "title", Label: "Title", Kind: "text"},
					{Key: "date", Label: "Date", Kind: "date"},
					{Key: "location", Label: "Location", Kind: "text"},
					{Key: "details", Label: "Details", Kind: "longtext"},
				},
				Samples: []map[string]any{
					{"title": "Launch Party", "date": "2025-03-01", "location": "The Warehouse", "details": "Doors at 6pm. Drinks, demos, and a raffle."},
					{"title": "Community Workshop", "date": "2025-03-12", "location": "Room 204", "details": "Hands-on session, bring a laptop."},
				},
			},
			{
				Type: "rsvp", Title: "My RSVPs", Audience: vibeAudienceMember, View: "list",
				Fields: []vibeField{
					{Key: "event", Label: "Event", Kind: "text"},
					{Key: "guests", Label: "Guests", Kind: "number"},
				},
			},
		},
	}
}

func collectionTemplate() vibeSpec {
	return vibeSpec{
		Name:    "Collection",
		Tagline: "Curate and share what you love",
		Accent:  "indigo",
		Icon:    "book",
		Sections: []vibeSection{
			{
				Type: "collection", Title: "My collection", Audience: vibeAudienceMember, View: "grid",
				Fields: []vibeField{
					{Key: "name", Label: "Name", Kind: "text"},
					{Key: "note", Label: "Note", Kind: "longtext"},
					{Key: "tags", Label: "Tags", Kind: "tag"},
				},
			},
			{
				Type: "profile", Title: "Public profile", Audience: vibeAudienceMemberPublic, View: "list",
				Fields: []vibeField{
					{Key: "bio", Label: "Bio", Kind: "longtext"},
				},
			},
		},
	}
}

func profileTemplate() vibeSpec {
	return vibeSpec{
		Name:    "Profile",
		Tagline: "Your corner of the internet",
		Accent:  "indigo",
		Icon:    "user",
		Sections: []vibeSection{
			{
				Type: "profile", Title: "Public profile", Audience: vibeAudienceMemberPublic, View: "list",
				Fields: []vibeField{
					{Key: "display_name", Label: "Display name", Kind: "text"},
					{Key: "bio", Label: "Bio", Kind: "longtext"},
					{Key: "links", Label: "Links", Kind: "tag"},
				},
			},
		},
	}
}

func defaultTemplate() vibeSpec {
	return vibeSpec{
		Name:    "Your app",
		Tagline: "Private data and public pages, handled",
		Accent:  "teal",
		Icon:    "sparkles",
		Sections: []vibeSection{
			{
				Type: "item", Title: "My items", Audience: vibeAudienceMember, View: "list",
				Fields: []vibeField{
					{Key: "title", Label: "Title", Kind: "text"},
					{Key: "notes", Label: "Notes", Kind: "longtext"},
				},
			},
			{
				Type: "announcement", Title: "Announcements", Audience: vibeAudienceOwner, View: "list",
				Fields: []vibeField{
					{Key: "title", Label: "Title", Kind: "text"},
					{Key: "body", Label: "Body", Kind: "longtext"},
				},
				Samples: []map[string]any{
					{"title": "Welcome!", "body": "This is a shared announcement anyone can read. Only the owner can post here."},
				},
			},
		},
	}
}
