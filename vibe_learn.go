package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// The vibe flywheel: every generation is measured, and what users actually keep
// feeds the next generation.
//
//   - appVibe persists the spec and its provenance on the app itself, so it
//     survives a claim long after the share link expires.
//   - The run ledger records how each spec was made (source, model, prompt
//     version, validator repairs) and what happened next (preview, download,
//     skill, refine, claim), so prompt and model changes are judged on outcomes.
//   - Exemplars feed reference designs back into the prompt: curated recipes
//     always, plus the structure of apps people claimed (when VIBE_LEARN is on).

// vibePromptVersion tags every run so the funnel compares prompt revisions.
// Bump it whenever vibeSystemInstruction or the prompt assembly changes.
const vibePromptVersion = "v2"

const (
	vibeRunsCollection = "_m_stash_vibe_runs"
	vibeRunRetention   = 180 * 24 * time.Hour

	vibeExemplarPoolSize   = 200
	vibeExemplarRefresh    = 5 * time.Minute
	vibeExemplarsPerPrompt = 2
	vibeMaxRefinements     = 10

	vibeModeIdea   = "idea"
	vibeModeRefine = "refine"
	vibeModeRecipe = "recipe"

	vibeEventPreview = "preview"
	vibeEventZip     = "zip"
	vibeEventSkill   = "skill"
	vibeEventRefine  = "refine"
	vibeEventClaim   = "claim"
)

// appVibe is the spec an app was generated from, stored on the app record. It
// is presentation metadata the server never enforces, so it cannot weaken
// ownership; it lets a claimed app keep its starter, skill, and lineage.
type appVibe struct {
	Spec          vibeSpec  `bson:"spec" json:"spec"`
	Idea          string    `bson:"idea" json:"idea"`
	Refinements   []string  `bson:"refinements,omitempty" json:"refinements,omitempty"`
	Source        string    `bson:"source" json:"source"`
	Model         string    `bson:"model,omitempty" json:"model,omitempty"`
	PromptVersion string    `bson:"promptVersion,omitempty" json:"promptVersion,omitempty"`
	CreatedAt     time.Time `bson:"createdAt" json:"createdAt"`
}

// vibeRun is one generation in the ledger, keyed by its share id. It stores no
// idea text or records — only how the spec was made and what happened next.
type vibeRun struct {
	ID            string               `bson:"_id"`
	AppID         string               `bson:"appId,omitempty"`
	ParentID      string               `bson:"parentId,omitempty"`
	Mode          string               `bson:"mode"`
	Source        string               `bson:"source"`
	Model         string               `bson:"model,omitempty"`
	PromptVersion string               `bson:"promptVersion"`
	Issues        []string             `bson:"issues,omitempty"`
	RepairTried   bool                 `bson:"repairTried,omitempty"`
	Repaired      bool                 `bson:"repaired,omitempty"`
	Fallback      bool                 `bson:"fallback,omitempty"`
	Exemplars     []string             `bson:"exemplars,omitempty"`
	Sections      int                  `bson:"sections"`
	Counts        map[string]int       `bson:"counts,omitempty"`
	FirstAt       map[string]time.Time `bson:"firstAt,omitempty"`
	CreatedAt     time.Time            `bson:"createdAt"`
	ExpiresAt     time.Time            `bson:"expiresAt"`
}

func ensureVibeRunIndexes(ctx context.Context, database *mongo.Database) error {
	_, err := database.Collection(vibeRunsCollection).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "expiresAt", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0)},
		{Keys: bson.D{{Key: "appId", Value: 1}}, Options: options.Index().SetSparse(true)},
		{Keys: bson.D{{Key: "createdAt", Value: -1}}},
	})
	if err != nil {
		return fmt.Errorf("create vibe run indexes: %w", err)
	}
	return nil
}

// recordVibeRun writes a run to the ledger. It is best effort: analytics must
// never fail the request that produced the app.
func (app *application) recordVibeRun(ctx context.Context, run vibeRun) {
	if app.database == nil || run.ID == "" {
		return
	}
	run.CreatedAt = time.Now().UTC()
	run.ExpiresAt = run.CreatedAt.Add(vibeRunRetention)
	if _, err := app.database.Collection(vibeRunsCollection).InsertOne(ctx, run); err != nil {
		app.logger.Error("vibe run record failed", "request_id", requestIDFromContext(ctx), "error", err)
	}
}

// markVibeRun counts a funnel event and stamps the first time it happened.
func (app *application) markVibeRun(ctx context.Context, filter bson.M, event string) {
	if app.database == nil {
		return
	}
	now := time.Now().UTC()
	update := bson.M{
		"$inc": bson.M{"counts." + event: 1},
		"$min": bson.M{"firstAt." + event: now},
	}
	if _, err := app.database.Collection(vibeRunsCollection).UpdateOne(ctx, filter, update); err != nil {
		app.logger.Error("vibe run mark failed", "event", event, "error", err)
	}
}

func (app *application) markVibeShare(ctx context.Context, shareID, event string) {
	if shareID == "" {
		return
	}
	app.markVibeRun(ctx, bson.M{"_id": shareID}, event)
}

// vibeFunnelRow is one cohort of runs (same mode, source, model, and prompt
// version) with how far its apps got.
type vibeFunnelRow struct {
	Mode          string  `bson:"mode" json:"mode"`
	Source        string  `bson:"source" json:"source"`
	Model         string  `bson:"model" json:"model"`
	PromptVersion string  `bson:"promptVersion" json:"promptVersion"`
	Runs          int     `bson:"runs" json:"runs"`
	WithIssues    int     `bson:"withIssues" json:"withIssues"`
	Repaired      int     `bson:"repaired" json:"repaired"`
	Fallbacks     int     `bson:"fallbacks" json:"fallbacks"`
	Exemplared    int     `bson:"exemplared" json:"exemplared"`
	Previewed     int     `bson:"previewed" json:"previewed"`
	Downloaded    int     `bson:"downloaded" json:"downloaded"`
	Skilled       int     `bson:"skilled" json:"skilled"`
	Refined       int     `bson:"refined" json:"refined"`
	Claimed       int     `bson:"claimed" json:"claimed"`
	ClaimRate     float64 `bson:"-" json:"claimRate"`
}

type vibeIssueCount struct {
	Code  string `bson:"_id" json:"code"`
	Count int    `bson:"count" json:"count"`
}

type vibeFunnel struct {
	WindowDays int              `json:"windowDays"`
	Cohorts    []vibeFunnelRow  `json:"cohorts"`
	TopIssues  []vibeIssueCount `json:"topIssues"`
}

const (
	vibeFunnelWindowDays = 30
	// vibeFunnelCacheTTL keeps the operator console's 5s overview poll from
	// re-running the funnel aggregations on every tick.
	vibeFunnelCacheTTL = 30 * time.Second
)

type vibeFunnelCache struct {
	mu       sync.Mutex
	report   vibeFunnel
	loadedAt time.Time
}

// cachedVibeFunnel serves the funnel from a short cache; a failed refresh
// returns the error and keeps the previous report for the next attempt.
func (app *application) cachedVibeFunnel(ctx context.Context) (vibeFunnel, error) {
	cache := &app.vibeFunnel
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if !cache.loadedAt.IsZero() && time.Since(cache.loadedAt) < vibeFunnelCacheTTL {
		return cache.report, nil
	}
	report, err := app.vibeFunnelReport(ctx)
	if err != nil {
		return report, err
	}
	cache.report = report
	cache.loadedAt = time.Now()
	return report, nil
}

// vibeFunnelReport aggregates the last 30 days of runs into per-cohort funnels
// and the most frequent validator repairs, for the operator console.
func (app *application) vibeFunnelReport(ctx context.Context) (vibeFunnel, error) {
	report := vibeFunnel{WindowDays: vibeFunnelWindowDays, Cohorts: []vibeFunnelRow{}, TopIssues: []vibeIssueCount{}}
	collection := app.database.Collection(vibeRunsCollection)
	since := bson.M{"createdAt": bson.M{"$gte": time.Now().UTC().Add(-vibeFunnelWindowDays * 24 * time.Hour)}}
	happened := func(event string) bson.M {
		return bson.M{"$sum": bson.M{"$cond": bson.A{bson.M{"$gt": bson.A{"$firstAt." + event, nil}}, 1, 0}}}
	}
	flag := func(field string) bson.M {
		return bson.M{"$sum": bson.M{"$cond": bson.A{bson.M{"$eq": bson.A{"$" + field, true}}, 1, 0}}}
	}
	cursor, err := collection.Aggregate(ctx, mongo.Pipeline{
		{{Key: "$match", Value: since}},
		{{Key: "$group", Value: bson.M{
			"_id":        bson.M{"mode": "$mode", "source": "$source", "model": "$model", "promptVersion": "$promptVersion"},
			"runs":       bson.M{"$sum": 1},
			"withIssues": bson.M{"$sum": bson.M{"$cond": bson.A{bson.M{"$gt": bson.A{bson.M{"$size": bson.M{"$ifNull": bson.A{"$issues", bson.A{}}}}, 0}}, 1, 0}}},
			"repaired":   flag("repaired"),
			"fallbacks":  flag("fallback"),
			"exemplared": bson.M{"$sum": bson.M{"$cond": bson.A{bson.M{"$gt": bson.A{bson.M{"$size": bson.M{"$ifNull": bson.A{"$exemplars", bson.A{}}}}, 0}}, 1, 0}}},
			"previewed":  happened(vibeEventPreview),
			"downloaded": happened(vibeEventZip),
			"skilled":    happened(vibeEventSkill),
			"refined":    happened(vibeEventRefine),
			"claimed":    happened(vibeEventClaim),
		}}},
		{{Key: "$replaceWith", Value: bson.M{"$mergeObjects": bson.A{"$_id", "$$ROOT"}}}},
		{{Key: "$sort", Value: bson.D{{Key: "runs", Value: -1}}}},
		{{Key: "$limit", Value: 24}},
	})
	if err != nil {
		return report, err
	}
	if err := cursor.All(ctx, &report.Cohorts); err != nil {
		return report, err
	}
	for index := range report.Cohorts {
		row := &report.Cohorts[index]
		if row.Runs > 0 {
			row.ClaimRate = math.Round(float64(row.Claimed)/float64(row.Runs)*1000) / 1000
		}
	}

	issues, err := collection.Aggregate(ctx, mongo.Pipeline{
		{{Key: "$match", Value: since}},
		{{Key: "$unwind", Value: "$issues"}},
		{{Key: "$group", Value: bson.M{"_id": "$issues", "count": bson.M{"$sum": 1}}}},
		{{Key: "$sort", Value: bson.D{{Key: "count", Value: -1}, {Key: "_id", Value: 1}}}},
		{{Key: "$limit", Value: 10}},
	})
	if err != nil {
		return report, err
	}
	if err := issues.All(ctx, &report.TopIssues); err != nil {
		return report, err
	}
	return report, nil
}

// ---------------------------------------------------------------------------
// Exemplars: reference designs retrieved for each new idea
// ---------------------------------------------------------------------------

// vibeExemplar is a reference design. Text is used only for matching and is
// never sent to the model; Spec is structural (no samples) and, for claimed
// apps, carries no name or tagline either.
type vibeExemplar struct {
	Key  string
	Text string
	Spec vibeSpec
}

type vibeExemplarCache struct {
	mu       sync.Mutex
	claimed  []vibeExemplar
	loadedAt time.Time
}

var vibeStopwords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "app": true, "that": true, "can": true,
	"their": true, "they": true, "where": true, "who": true, "each": true, "own": true, "your": true,
	"from": true, "into": true, "about": true, "this": true, "users": true, "user": true, "people": true,
	"lets": true, "let": true, "allow": true, "allows": true, "make": true, "simple": true, "small": true,
	"track": true, "manage": true, "list": true, "page": true, "website": true, "site": true, "build": true,
}

// vibeTokens reduces text to a set of lowercase content words with a crude
// plural fold, enough to match "barbershop haircuts" with "barber cut".
func vibeTokens(text string) map[string]bool {
	tokens := map[string]bool{}
	for _, word := range strings.FieldsFunc(strings.ToLower(text), func(character rune) bool {
		return !unicode.IsLetter(character) && !unicode.IsDigit(character)
	}) {
		if len(word) < 3 || vibeStopwords[word] {
			continue
		}
		if len(word) > 4 && strings.HasSuffix(word, "s") && !strings.HasSuffix(word, "ss") {
			word = strings.TrimSuffix(word, "s")
		}
		tokens[word] = true
	}
	return tokens
}

func vibeSimilarity(query, candidate map[string]bool) float64 {
	if len(query) == 0 || len(candidate) == 0 {
		return 0
	}
	shared := 0
	for token := range query {
		if candidate[token] {
			shared++
		}
	}
	return float64(shared) / math.Sqrt(float64(len(query)*len(candidate)))
}

// structuralSpec keeps only what a reference design needs: sections, fields,
// options, views, and audiences. Samples are always dropped; names and
// taglines are dropped unless keepIdentity (curated recipes only).
func structuralSpec(spec vibeSpec, keepIdentity bool) vibeSpec {
	out := vibeSpec{Accent: spec.Accent, Icon: spec.Icon}
	if keepIdentity {
		out.Name = spec.Name
		out.Tagline = spec.Tagline
	}
	for _, section := range spec.Sections {
		section.Samples = nil
		out.Sections = append(out.Sections, section)
	}
	return out
}

func recipeExemplars() []vibeExemplar {
	exemplars := make([]vibeExemplar, 0, len(vibeRecipes))
	for _, recipe := range vibeRecipes {
		text := strings.Join(append([]string{recipe.Name, recipe.Tagline, recipe.RemixIdea}, recipe.Keywords...), " ")
		exemplars = append(exemplars, vibeExemplar{
			Key:  "recipe:" + recipe.ID,
			Text: text,
			Spec: structuralSpec(validateVibeSpec(recipe.build()), true),
		})
	}
	return exemplars
}

// claimedExemplars returns the structure of recently claimed, model-generated
// apps, cached for a few minutes so generation never waits on this query. A
// failed refresh keeps the previous pool.
func (app *application) claimedExemplars(ctx context.Context) []vibeExemplar {
	cache := &app.vibeExemplars
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if app.database == nil || time.Since(cache.loadedAt) < vibeExemplarRefresh {
		return cache.claimed
	}
	cache.loadedAt = time.Now()

	queryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cursor, err := app.database.Collection(appsCollectionName).Find(queryCtx,
		bson.M{"sandbox": bson.M{"$ne": true}, "active": true, "vibe.source": vibeSourceModel},
		options.Find().
			SetProjection(bson.M{"_id": 1, "vibe": 1}).
			SetSort(bson.D{{Key: "claimedAt", Value: -1}}).
			SetLimit(vibeExemplarPoolSize),
	)
	if err != nil {
		app.logger.Error("vibe exemplar refresh failed", "error", err)
		return cache.claimed
	}
	var records []struct {
		ID   bson.ObjectID `bson:"_id"`
		Vibe *appVibe      `bson:"vibe"`
	}
	if err := cursor.All(queryCtx, &records); err != nil {
		app.logger.Error("vibe exemplar decode failed", "error", err)
		return cache.claimed
	}
	pool := make([]vibeExemplar, 0, len(records))
	for _, record := range records {
		if record.Vibe == nil || len(record.Vibe.Spec.Sections) == 0 {
			continue
		}
		pool = append(pool, vibeExemplar{
			Key:  "app:" + record.ID.Hex(),
			Text: strings.Join(append([]string{record.Vibe.Idea}, record.Vibe.Refinements...), " "),
			// Re-validate on read so a stored spec can never smuggle anything the
			// current validator would reject into a prompt.
			Spec: structuralSpec(validateVibeSpec(record.Vibe.Spec), false),
		})
	}
	cache.claimed = pool
	return pool
}

// selectVibeExemplars ranks candidates by word overlap with the idea and keeps
// the best few that share at least one meaningful word. Unrelated designs are
// worse than none, so an unmatched idea gets no references.
func selectVibeExemplars(idea string, candidates []vibeExemplar, limit int) []vibeExemplar {
	query := vibeTokens(idea)
	type scored struct {
		exemplar vibeExemplar
		score    float64
	}
	ranked := []scored{}
	for _, candidate := range candidates {
		if score := vibeSimilarity(query, vibeTokens(candidate.Text)); score > 0 {
			ranked = append(ranked, scored{candidate, score})
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].exemplar.Key < ranked[j].exemplar.Key
	})
	selected := []vibeExemplar{}
	seenStructure := map[string]bool{}
	for _, item := range ranked {
		if len(selected) >= limit {
			break
		}
		// A claimed app often is a launched recipe; two identical structures
		// would waste a reference slot, so keep only the best-ranked copy.
		structure := vibeSpecJSON(vibeSpec{Sections: item.exemplar.Spec.Sections})
		if seenStructure[structure] {
			continue
		}
		seenStructure[structure] = true
		selected = append(selected, item.exemplar)
	}
	return selected
}

// vibeExemplarsFor picks reference designs for an idea: claimed apps first
// (they are proof of what people keep), then curated recipes.
func (app *application) vibeExemplarsFor(ctx context.Context, idea string) []vibeExemplar {
	candidates := []vibeExemplar{}
	if app.effectiveSettings(ctx).VibeLearn {
		candidates = append(candidates, app.claimedExemplars(ctx)...)
	}
	candidates = append(candidates, recipeExemplars()...)
	return selectVibeExemplars(idea, candidates, vibeExemplarsPerPrompt)
}

func exemplarKeys(exemplars []vibeExemplar) []string {
	keys := make([]string, 0, len(exemplars))
	for _, exemplar := range exemplars {
		keys = append(keys, exemplar.Key)
	}
	return keys
}

// vibeSpecJSON renders a spec in the model's own wire shape, so references and
// the current spec on a refine look exactly like what the model must return.
func vibeSpecJSON(spec vibeSpec) string {
	encoded, err := json.Marshal(vibePayloadFromSpec(spec))
	if err != nil {
		return "{}"
	}
	return string(encoded)
}
