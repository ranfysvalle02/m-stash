package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func issueCodes(issues vibeIssues) map[string]bool {
	codes := map[string]bool{}
	for _, issue := range issues {
		codes[issue.Code] = true
	}
	return codes
}

func TestValidateVibeSpecReportFlagsEveryRepair(t *testing.T) {
	spec := vibeSpec{
		Name: "Shop",
		Sections: []vibeSection{
			{Type: "!!", Title: "Broken", Audience: vibeAudienceOwner, Fields: []vibeField{{Key: "a", Label: "A", Kind: "text"}}},
			{Type: "catalog", Title: "Catalog", Audience: vibeAudienceOwner, View: "carousel", ActionLabel: "Buy",
				Fields: []vibeField{{Key: "name", Label: "Name", Kind: "emoji"}, {Key: "name", Label: "Dup", Kind: "text"}}},
		},
	}
	cleaned, issues := validateVibeSpecReport(spec)
	codes := issueCodes(issues)
	for _, want := range []string{"section.invalid-type", "section.view-defaulted", "field.kind-defaulted", "field.duplicate-key", "owner.no-samples", "action.no-target"} {
		if !codes[want] {
			t.Errorf("missing issue %q in %v", want, issues.codes())
		}
	}
	// The report never changes what ships: same cleaned spec as the plain validator.
	plain, _ := json.Marshal(validateVibeSpec(spec))
	reported, _ := json.Marshal(cleaned)
	if string(plain) != string(reported) {
		t.Fatalf("report changed the cleaned spec:\n%s\n%s", plain, reported)
	}
}

// Curated recipes double as few-shot references, so they must be exactly the
// kind of spec the validator never has to repair.
func TestCuratedRecipesValidateWithoutIssues(t *testing.T) {
	for _, recipe := range vibeRecipes {
		if _, issues := validateVibeSpecReport(recipe.build()); len(issues) > 0 {
			t.Errorf("recipe %s needs repairs: %v", recipe.ID, issues)
		}
	}
}

func TestSelectVibeExemplarsMatchesByMeaningfulWords(t *testing.T) {
	got := selectVibeExemplars("a barbershop where clients book haircuts with their favorite barber", recipeExemplars(), 2)
	if len(got) == 0 || got[0].Key != "recipe:barber-bookings" {
		t.Fatalf("want barber recipe first, got %v", exemplarKeys(got))
	}
	if got := selectVibeExemplars("quantum flux capacitor calibration", recipeExemplars(), 2); len(got) != 0 {
		t.Fatalf("an unrelated idea must get no references, got %v", exemplarKeys(got))
	}
	// A claimed app launched from a recipe has the recipe's structure; it must
	// not occupy both reference slots.
	recipe, _ := lookupVibeRecipe("barber-bookings")
	claimedCopy := vibeExemplar{Key: "app:copy", Text: "barbershop haircuts barber", Spec: structuralSpec(validateVibeSpec(recipe.build()), false)}
	got = selectVibeExemplars("a barbershop where clients book haircuts with their favorite barber", append([]vibeExemplar{claimedCopy}, recipeExemplars()...), 2)
	if len(got) == 2 && structuralSpecJSONEqual(got[0], got[1]) {
		t.Fatalf("duplicate structures selected: %v", exemplarKeys(got))
	}
}

func structuralSpecJSONEqual(a, b vibeExemplar) bool {
	return vibeSpecJSON(vibeSpec{Sections: a.Spec.Sections}) == vibeSpecJSON(vibeSpec{Sections: b.Spec.Sections})
}

func TestBuildVibeUserPromptCarriesStructureNotSamples(t *testing.T) {
	recipe, _ := lookupVibeRecipe("storefront")
	claimed := vibeExemplar{Key: "app:x", Spec: structuralSpec(validateVibeSpec(recipe.build()), false)}
	prompt := buildVibeUserPrompt(vibeAsk{Idea: "a record shop", Exemplars: []vibeExemplar{claimed}})
	if !strings.Contains(prompt, "Reference designs") || !strings.HasSuffix(strings.TrimSpace(prompt[strings.Index(prompt, "App idea:"):]), "sample records.") {
		t.Fatalf("prompt shape unexpected:\n%s", prompt)
	}
	if strings.Contains(prompt, `"samples"`) || strings.Contains(prompt, recipe.build().Name) {
		t.Fatalf("a claimed reference must carry neither samples nor its name:\n%s", prompt)
	}

	parent := validateVibeSpec(recipe.build())
	refine := buildVibeUserPrompt(vibeAsk{Parent: &parent, Instruction: "add reviews"})
	if !strings.Contains(refine, "Current app spec") || !strings.Contains(refine, "add reviews") || strings.Contains(refine, "App idea:") {
		t.Fatalf("refine prompt unexpected:\n%s", refine)
	}
	if !strings.Contains(refine, `"samples"`) {
		t.Fatalf("refine prompt must keep the parent's samples so they survive the change:\n%s", refine)
	}
}

// A refine replays the parent in the model's wire shape, so parsing it back
// must reproduce the same structure and seed data.
func TestVibePayloadRoundTripsThroughModelShape(t *testing.T) {
	for _, recipe := range vibeRecipes {
		original := validateVibeSpec(recipe.build())
		roundTripped, issues, err := parseVibeSpecText(vibeSpecJSON(original))
		if err != nil {
			t.Fatalf("%s: %v", recipe.ID, err)
		}
		if len(issues) > 0 {
			t.Errorf("%s: round trip introduced issues %v", recipe.ID, issues)
		}
		if sectionTypeKey(original) != sectionTypeKey(roundTripped) {
			t.Fatalf("%s: sections changed", recipe.ID)
		}
		for index, section := range original.Sections {
			if len(section.Samples) != len(roundTripped.Sections[index].Samples) || len(section.Fields) != len(roundTripped.Sections[index].Fields) {
				t.Fatalf("%s/%s: fields or samples lost", recipe.ID, section.Type)
			}
		}
	}
}

const flawedVibeSpecText = `{"name":"Gigs","sections":[{"type":"show","title":"Shows","audience":"owner","view":"grid","fields":[{"key":"name","label":"Name","kind":"emoji"}],"samples":[{"values":[{"key":"name","value":"Open mic"}]}]}]}`
const cleanVibeSpecText = `{"name":"Gigs","sections":[{"type":"show","title":"Shows","audience":"owner","view":"grid","fields":[{"key":"name","label":"Name","kind":"text"}],"samples":[{"values":[{"key":"name","value":"Open mic"}]}]}]}`

func TestGenerateVibeSpecRepairsWithValidatorFeedback(t *testing.T) {
	var bodies []string
	calls := withFakeGemini(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))
		if len(bodies) == 1 {
			_, _ = w.Write(geminiResponseWithText(flawedVibeSpecText))
			return
		}
		_, _ = w.Write(geminiResponseWithText(cleanVibeSpecText))
	})
	app := &application{config: Config{GeminiAPIKey: "k", GeminiModel: "gemini-3.5-flash-lite"}, logger: discardLogger()}
	generation, err := app.generateVibeSpec(context.Background(), vibeAsk{Idea: "a gig listing app"})
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 || !generation.RepairTried || !generation.Repaired || len(generation.Issues) != 0 {
		t.Fatalf("calls=%d generation=%+v", len(*calls), generation)
	}
	if generation.Spec.Sections[0].Fields[0].Kind != "text" {
		t.Fatalf("repaired spec not used: %+v", generation.Spec.Sections[0].Fields)
	}
	repair := bodies[1]
	if !strings.Contains(repair, `"role":"model"`) || !strings.Contains(repair, "emoji") || !strings.Contains(repair, "validator had to repair") {
		t.Fatalf("repair turn must replay the model's answer and the validator's findings:\n%s", repair)
	}
}

func TestGenerateVibeSpecKeepsFirstSpecWhenRepairIsNoBetter(t *testing.T) {
	calls := withFakeGemini(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(geminiResponseWithText(flawedVibeSpecText))
	})
	app := &application{config: Config{GeminiAPIKey: "k", GeminiModel: "gemini-3.5-flash-lite"}, logger: discardLogger()}
	generation, err := app.generateVibeSpec(context.Background(), vibeAsk{Idea: "a gig listing app"})
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 || !generation.RepairTried || generation.Repaired || len(generation.Issues) == 0 {
		t.Fatalf("calls=%d generation=%+v", len(*calls), generation)
	}
}

func TestGenerateVibeSpecSkipsRepairForCleanSpec(t *testing.T) {
	calls := withFakeGemini(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(geminiResponseWithText(cleanVibeSpecText))
	})
	app := &application{config: Config{GeminiAPIKey: "k", GeminiModel: "gemini-3.5-flash-lite"}, logger: discardLogger()}
	generation, err := app.generateVibeSpec(context.Background(), vibeAsk{Idea: "a gig listing app"})
	if err != nil || len(*calls) != 1 || generation.RepairTried {
		t.Fatalf("err=%v calls=%d generation=%+v", err, len(*calls), generation)
	}
}

func TestNewVibeRunRecordsProvenanceWithoutIdea(t *testing.T) {
	generation := &vibeGeneration{Model: "m", Issues: vibeIssues{{Code: "owner.no-samples"}}, RepairTried: true}
	run := newVibeRun("share123", "app1", vibeResult{
		Idea: "secret plans", Mode: vibeModeRefine, ParentID: "parent", Generation: generation,
		Spec: vibeSpec{Source: vibeSourceModel, Sections: []vibeSection{{}}}, Exemplars: []string{"recipe:storefront"},
	})
	if run.ID != "share123" || run.AppID != "app1" || run.ParentID != "parent" || run.Model != "m" ||
		run.PromptVersion != vibePromptVersion || !run.RepairTried || run.Issues[0] != "owner.no-samples" || run.Sections != 1 {
		t.Fatalf("run = %+v", run)
	}
	if encoded, _ := json.Marshal(run); strings.Contains(string(encoded), "secret plans") {
		t.Fatal("the run ledger must never store the idea text")
	}
}
