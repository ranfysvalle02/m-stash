package main

import "strings"

// Recipes are the canonical, handcrafted app specs that power both the one-click
// Recipe Launchpad (GET /v1/vibe/recipes) and the deterministic keyword fallback
// in buildVibeBlueprintSpec. Each recipe pairs public owner data (a catalog,
// roster, or leaderboard everyone can read) with server-isolated per-user data
// (private bookings, tickets, saves), so launching one is an instant tour of
// m-stash's ownership model. Both the recipe path and the model path produce a
// vibeSpec and run through validateVibeSpec, so output quality never depends on
// the model being configured or reachable.

type vibeRecipe struct {
	ID           string
	Name         string
	Tagline      string
	Accent       string
	Icon         string
	PublicBlurb  string
	PrivateBlurb string
	// RemixIdea is the concrete, keyword-rich prompt the "Remix" button drops into
	// the composer. Unlike the poetic Tagline, it always contains this recipe's own
	// keywords, so remixing maps back to the same recipe via buildVibeBlueprintSpec
	// even with no model key configured.
	RemixIdea string
	Keywords  []string
	build     func() vibeSpec
}

// vibeRecipeMeta is the API-facing summary of a recipe used by the launchpad
// cards and the /discover strip. It never carries seeded samples.
type vibeRecipeMeta struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Tagline      string `json:"tagline"`
	Accent       string `json:"accent"`
	Icon         string `json:"icon"`
	PublicBlurb  string `json:"publicBlurb"`
	PrivateBlurb string `json:"privateBlurb"`
	RemixIdea    string `json:"remixIdea"`
	ActionLabel  string `json:"actionLabel,omitempty"`
}

// vibeRecipes is the ordered, canonical roster. Order matters for the keyword
// fallback: buildVibeBlueprintSpec returns the first recipe with any matching
// keyword, so broadly-worded recipes (storefront) sit last. Keep "calendar",
// "gig", and "event" out of the appointments keywords so "in-store gigs
// calendar" still maps to the storefront via "store".
var vibeRecipes = []vibeRecipe{
	{
		ID:           "leaderboard",
		Name:         "Neon Arcade",
		Tagline:      "Chase the high score, save your run",
		Accent:       "rose",
		Icon:         "trophy",
		PublicBlurb:  "The owner seeds a global high-score board every visitor can read without logging in.",
		PrivateBlurb: "Each player keeps their own runs — private saves scoped to their account.",
		RemixIdea:    "an arcade leaderboard game where the owner posts global high scores and each player saves their own runs",
		Keywords:     []string{"leaderboard", "high score", "highscore", "arcade", "tournament", "game", "score", "rank", "player", "puzzle"},
		build:        leaderboardRecipe,
	},
	{
		ID:           "user-tickets",
		Name:         "Pulse Support Desk",
		Tagline:      "Status everyone sees, tickets only you see",
		Accent:       "indigo",
		Icon:         "note",
		PublicBlurb:  "The owner publishes a public system-status timeline anyone can check.",
		PrivateBlurb: "Every customer files private support tickets only they (and the owner) can read.",
		RemixIdea:    "a support desk with a public system status page and private customer support tickets",
		Keywords:     []string{"support", "ticket", "help desk", "helpdesk", "incident", "status page", "complaint", "bug report", "issue tracker"},
		build:        userTicketsRecipe,
	},
	{
		ID:           "appointments",
		Name:         "Harbor Health Clinic",
		Tagline:      "See who's open, keep your visits private",
		Accent:       "teal",
		Icon:         "calendar",
		PublicBlurb:  "The owner lists practitioners and open dates for anyone to browse.",
		PrivateBlurb: "Each patient's appointments and intake notes stay private to them.",
		RemixIdea:    "a medical clinic where patients book appointments with doctors and keep private visit notes",
		Keywords:     []string{"appointment", "clinic", "doctor", "dentist", "practitioner", "patient", "therapy", "consultation", "medical", "healthcare"},
		build:        appointmentsRecipe,
	},
	{
		ID:           "barber-bookings",
		Name:         "Fade & Blade Barber",
		Tagline:      "Browse the chair menu, book your cut",
		Accent:       "amber",
		Icon:         "sparkles",
		PublicBlurb:  "The owner publishes the priced service menu everyone can browse.",
		PrivateBlurb: "Each client's bookings stay private to their own account.",
		RemixIdea:    "a barbershop with a public chair menu where clients book a haircut, beard trim, or fade with their favorite stylist",
		Keywords:     []string{"barber", "haircut", "hair cut", "salon", "grooming", "stylist", "beard", "fade", "tattoo", "nail"},
		build:        barberRecipe,
	},
	{
		ID:           "real-estate-bookings",
		Name:         "Skyline Property Tours",
		Tagline:      "Explore listings, request private tours",
		Accent:       "emerald",
		Icon:         "box",
		PublicBlurb:  "The owner posts priced listings with neighborhood tags for anyone to explore.",
		PrivateBlurb: "Each buyer's tour requests and notes stay private to them.",
		RemixIdea:    "a real estate agency with public property listings where buyers request private home tours",
		Keywords:     []string{"real estate", "property", "properties", "listing", "realtor", "apartment", "rental", "condo", "open house", "house tour"},
		build:        realEstateRecipe,
	},
	{
		ID:           "storefront",
		Name:         "Riff & Groove Vinyl",
		Tagline:      "Browse the crates, build your wishlist",
		Accent:       "amber",
		Icon:         "cart",
		PublicBlurb:  "The owner runs a public catalog and in-store gig calendar anyone can read.",
		PrivateBlurb: "Each shopper keeps a private wishlist scoped to their account.",
		RemixIdea:    "a record store and vinyl shop with a public catalog of guitars and gear, plus private customer wishlists",
		Keywords:     []string{"store", "shop", "music", "record", "vinyl", "guitar", "instrument", "menu", "bakery", "cafe", "coffee", "inventory", "retail", "boutique", "merch", "catalog", "product", "market", "goods"},
		build:        storefrontRecipe,
	},
}

// lookupVibeRecipe finds a recipe by its ID.
func lookupVibeRecipe(id string) (vibeRecipe, bool) {
	id = strings.TrimSpace(strings.ToLower(id))
	for _, recipe := range vibeRecipes {
		if recipe.ID == id {
			return recipe, true
		}
	}
	return vibeRecipe{}, false
}

// buildVibeRecipeSpec builds the validated, recipe-sourced spec for an ID. The
// caller must have confirmed the ID via lookupVibeRecipe.
func buildVibeRecipeSpec(id string) vibeSpec {
	recipe, ok := lookupVibeRecipe(id)
	if !ok {
		return finalizeRecipe(defaultTemplate())
	}
	return finalizeRecipe(recipe.build())
}

// vibeRecipeMetas returns the launchpad summaries, deriving each recipe's action
// label from its validated owner section so the UI and the spec never drift.
func vibeRecipeMetas() []vibeRecipeMeta {
	metas := make([]vibeRecipeMeta, 0, len(vibeRecipes))
	for _, recipe := range vibeRecipes {
		metas = append(metas, vibeRecipeMeta{
			ID:           recipe.ID,
			Name:         recipe.Name,
			Tagline:      recipe.Tagline,
			Accent:       recipe.Accent,
			Icon:         recipe.Icon,
			PublicBlurb:  recipe.PublicBlurb,
			PrivateBlurb: recipe.PrivateBlurb,
			RemixIdea:    recipe.RemixIdea,
			ActionLabel:  recipeActionLabel(recipe),
		})
	}
	return metas
}

// recipeActionLabel returns the first owner section's sanitized action label.
func recipeActionLabel(recipe vibeRecipe) string {
	spec := validateVibeSpec(recipe.build())
	for _, section := range spec.Sections {
		if section.Audience == vibeAudienceOwner && section.ActionLabel != "" {
			return section.ActionLabel
		}
	}
	return ""
}

// titleFromIdea turns a short idea into a title, falling back to the template's
// own name for long or empty descriptions.
func titleFromIdea(idea, fallback string) string {
	idea = strings.TrimSpace(idea)
	if len(idea) == 0 {
		return fallback
	}
	words := strings.Fields(idea)
	if len(words) > 4 {
		return fallback
	}
	return strings.Title(strings.ToLower(idea)) //nolint:staticcheck // ASCII titles only
}

// buildVibeBlueprintSpec picks the closest recipe for an idea by keyword, with a
// generic fallback, and returns a validated spec marked as blueprint-sourced.
func buildVibeBlueprintSpec(idea string) vibeSpec {
	lowered := strings.ToLower(idea)
	for _, recipe := range vibeRecipes {
		for _, keyword := range recipe.Keywords {
			if strings.Contains(lowered, keyword) {
				return finalizeTemplate(recipe.build(), idea)
			}
		}
	}
	return finalizeTemplate(defaultTemplate(), idea)
}

// finalizeTemplate stamps a keyword-matched spec as a blueprint and names it
// after the idea, so a typed idea reads as the user's own app.
func finalizeTemplate(spec vibeSpec, idea string) vibeSpec {
	spec.Source = vibeSourceBlueprint
	spec.Name = titleFromIdea(idea, spec.Name)
	return validateVibeSpec(spec)
}

// finalizeRecipe stamps an explicitly-launched recipe as recipe-sourced and
// keeps the recipe's own curated name (no idea to rename it after).
func finalizeRecipe(spec vibeSpec) vibeSpec {
	spec.Source = vibeSourceRecipe
	return validateVibeSpec(spec)
}

func leaderboardRecipe() vibeSpec {
	return vibeSpec{
		Name:    "Neon Arcade",
		Tagline: "Chase the high score, save your run",
		Accent:  "rose",
		Icon:    "trophy",
		Sections: []vibeSection{
			{
				Type: "leaderboard", Title: "Global leaderboard", Audience: vibeAudienceOwner, View: "ranked",
				Fields: []vibeField{
					{Key: "player", Label: "Player", Kind: "text"},
					{Key: "score", Label: "Score", Kind: "number"},
					{Key: "world", Label: "World", Kind: "text"},
				},
				Samples: []map[string]any{
					{"player": "ace_of_space", "score": 98400.0, "world": "Neon City"},
					{"player": "pixelpirate", "score": 87150.0, "world": "Cyber Dunes"},
					{"player": "retro_rhonda", "score": 76020.0, "world": "Neon City"},
					{"player": "glitch_gwen", "score": 64890.0, "world": "Pixel Peaks"},
				},
			},
			{
				Type: "save", Title: "My runs", Audience: vibeAudienceMember, View: "list",
				Fields: []vibeField{
					{Key: "run", Label: "Run name", Kind: "text"},
					{Key: "world", Label: "World", Kind: "text", Options: []string{"Neon City", "Cyber Dunes", "Pixel Peaks"}},
					{Key: "score", Label: "Score", Kind: "number"},
					{Key: "hardcore", Label: "Hardcore", Kind: "toggle"},
				},
			},
		},
	}
}

func userTicketsRecipe() vibeSpec {
	return vibeSpec{
		Name:    "Pulse Support Desk",
		Tagline: "Status everyone sees, tickets only you see",
		Accent:  "indigo",
		Icon:    "note",
		Sections: []vibeSection{
			{
				Type: "status", Title: "System status", Audience: vibeAudienceOwner, View: "timeline",
				Fields: []vibeField{
					{Key: "service", Label: "Service", Kind: "text"},
					{Key: "severity", Label: "Severity", Kind: "text", Options: []string{"Operational", "Degraded", "Partial outage", "Major outage"}},
					{Key: "date", Label: "Updated", Kind: "date"},
					{Key: "details", Label: "Details", Kind: "longtext"},
				},
				Samples: []map[string]any{
					{"service": "API", "severity": "Operational", "date": "2026-01-14", "details": "All systems nominal across every region."},
					{"service": "Dashboard", "severity": "Degraded", "date": "2026-01-13", "details": "Chart loads are slow while we scale a cache tier."},
					{"service": "Webhooks", "severity": "Partial outage", "date": "2026-01-12", "details": "Delivery retries are delayed up to 10 minutes."},
				},
			},
			{
				Type: "ticket", Title: "My tickets", Audience: vibeAudienceMember, View: "list",
				Fields: []vibeField{
					{Key: "subject", Label: "Subject", Kind: "text"},
					{Key: "priority", Label: "Priority", Kind: "text", Options: []string{"Low", "Normal", "High", "Urgent"}},
					{Key: "category", Label: "Category", Kind: "text", Options: []string{"Billing", "Bug", "How-to", "Feature request"}},
					{Key: "details", Label: "Details", Kind: "longtext"},
				},
			},
		},
	}
}

func appointmentsRecipe() vibeSpec {
	return vibeSpec{
		Name:    "Harbor Health Clinic",
		Tagline: "See who's open, keep your visits private",
		Accent:  "teal",
		Icon:    "calendar",
		Sections: []vibeSection{
			{
				Type: "practitioner", Title: "Our practitioners", Audience: vibeAudienceOwner, View: "timeline",
				ActionLabel: "Book visit",
				Fields: []vibeField{
					{Key: "name", Label: "Practitioner", Kind: "text"},
					{Key: "specialty", Label: "Specialty", Kind: "text"},
					{Key: "next_available", Label: "Next available", Kind: "date"},
					{Key: "details", Label: "About", Kind: "longtext"},
				},
				Samples: []map[string]any{
					{"name": "Dr. Maya Okonkwo", "specialty": "Family medicine", "next_available": "2026-01-16", "details": "Same-day sick visits and annual checkups."},
					{"name": "Dr. Liam Alvarez", "specialty": "Dermatology", "next_available": "2026-01-18", "details": "Skin screenings and minor in-office procedures."},
					{"name": "Nurse Priya Shah", "specialty": "Vaccinations", "next_available": "2026-01-15", "details": "Flu shots and travel immunizations, walk-ins welcome."},
				},
			},
			{
				Type: "appointment", Title: "My appointments", Audience: vibeAudienceMember, View: "list",
				Fields: []vibeField{
					{Key: "practitioner", Label: "Practitioner", Kind: "text"},
					{Key: "visit_type", Label: "Visit type", Kind: "text", Options: []string{"New patient", "Follow-up", "Annual checkup", "Urgent care"}},
					{Key: "date", Label: "Date", Kind: "date"},
					{Key: "notes", Label: "Private notes", Kind: "longtext"},
				},
			},
		},
	}
}

func barberRecipe() vibeSpec {
	return vibeSpec{
		Name:    "Fade & Blade Barber",
		Tagline: "Browse the chair menu, book your cut",
		Accent:  "amber",
		Icon:    "sparkles",
		Sections: []vibeSection{
			{
				Type: "service", Title: "Cuts & chair menu", Audience: vibeAudienceOwner, View: "grid",
				ActionLabel: "Book this cut",
				Fields: []vibeField{
					{Key: "name", Label: "Service", Kind: "text"},
					{Key: "price", Label: "Price", Kind: "price"},
					{Key: "duration", Label: "Duration", Kind: "text"},
					{Key: "description", Label: "Description", Kind: "longtext"},
				},
				Samples: []map[string]any{
					{"name": "Skin Fade", "price": 42.0, "duration": "45 min", "description": "Sharp zero fade with a clean razor finish."},
					{"name": "Beard Sculpt", "price": 30.0, "duration": "30 min", "description": "Hot towel, precise line-up, and conditioning oil."},
					{"name": "Full Ritual", "price": 65.0, "duration": "75 min", "description": "Cut, beard, hot-towel shave, and scalp massage."},
				},
			},
			{
				Type: "booking", Title: "My bookings", Audience: vibeAudienceMember, View: "list",
				Fields: []vibeField{
					{Key: "service", Label: "Service", Kind: "text"},
					{Key: "barber", Label: "Barber", Kind: "text", Options: []string{"Tony", "Rosa", "Dion", "Any available"}},
					{Key: "date", Label: "Date", Kind: "date"},
					{Key: "notes", Label: "Notes", Kind: "longtext"},
				},
			},
		},
	}
}

func realEstateRecipe() vibeSpec {
	return vibeSpec{
		Name:    "Skyline Property Tours",
		Tagline: "Explore listings, request private tours",
		Accent:  "emerald",
		Icon:    "box",
		Sections: []vibeSection{
			{
				Type: "listing", Title: "Open listings", Audience: vibeAudienceOwner, View: "grid",
				ActionLabel: "Request tour",
				Fields: []vibeField{
					{Key: "address", Label: "Address", Kind: "text"},
					{Key: "price", Label: "Price", Kind: "price"},
					{Key: "neighborhood", Label: "Neighborhood", Kind: "tag"},
					{Key: "details", Label: "Details", Kind: "longtext"},
				},
				Samples: []map[string]any{
					{"address": "120 Harbor View Loft", "price": 865000.0, "neighborhood": []string{"waterfront", "2 bed"}, "details": "Floor-to-ceiling windows and a private balcony over the marina."},
					{"address": "48 Maple Row Townhome", "price": 540000.0, "neighborhood": []string{"old town", "3 bed"}, "details": "Renovated kitchen, fenced yard, walk to the farmers market."},
					{"address": "9 Summit Ridge Bungalow", "price": 410000.0, "neighborhood": []string{"hillside", "2 bed"}, "details": "Single level with a wraparound deck and mountain views."},
				},
			},
			{
				Type: "tour", Title: "My tours", Audience: vibeAudienceMember, View: "list",
				Fields: []vibeField{
					{Key: "address", Label: "Listing", Kind: "text"},
					{Key: "tour_format", Label: "Tour format", Kind: "text", Options: []string{"In person", "Video call", "Self-guided"}},
					{Key: "preapproved", Label: "Pre-approved", Kind: "toggle"},
					{Key: "notes", Label: "Buyer notes", Kind: "longtext"},
				},
			},
		},
	}
}

func storefrontRecipe() vibeSpec {
	return vibeSpec{
		Name:    "Riff & Groove Vinyl",
		Tagline: "Browse the crates, build your wishlist",
		Accent:  "amber",
		Icon:    "cart",
		Sections: []vibeSection{
			{
				Type: "catalog", Title: "In the crates", Audience: vibeAudienceOwner, View: "grid",
				ActionLabel: "Save to wishlist",
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
					{"act": "Open Mic Night", "date": "2026-02-14", "details": "Sign-ups at 7pm, music at 8. All levels welcome."},
					{"act": "The Basement Trio (Live)", "date": "2026-02-21", "details": "Local jazz, free entry, limited seating."},
				},
			},
			{
				Type: "wishlist", Title: "My wishlist", Audience: vibeAudienceMember, View: "list",
				Fields: []vibeField{
					{Key: "item", Label: "Item", Kind: "text"},
					{Key: "format", Label: "Format", Kind: "text", Options: []string{"Vinyl", "CD", "Digital", "Gear"}},
					{Key: "budget", Label: "Budget", Kind: "price"},
					{Key: "note", Label: "Note", Kind: "longtext"},
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
