package main

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// The build kit is one .zip that sets a developer up to build on an m-stash app
// anywhere: a runnable starter UI, a zero-dependency client, an env template,
// a verify script, and agent rules. Vibe apps use their stored spec; any other
// app gets a spec inferred from the shape of its own records.

const (
	vibeSourceInferred = "inferred"
	// kitInferSampleSize bounds how many recent records shape an inferred spec.
	kitInferSampleSize = 500
)

var kitDatePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}`)

// kitPriceKeys hints that a numeric field is money rather than a count.
var kitPriceKeys = []string{"price", "cost", "amount", "fee", "total", "budget"}

type inferredField struct {
	key   string
	kind  string
	count int
}

type inferredSection struct {
	shared bool
	typ    string
	count  int
	public int
	first  int
	fields map[string]*inferredField
}

// kitRecordForApp returns the spec-bearing record a kit is built from: the
// stored vibe spec when there is one, otherwise a spec inferred from records.
func (app *application) kitRecordForApp(ctx context.Context, record AppRecord) (*vibeShareRecord, error) {
	if record.Vibe != nil {
		return vibeRecordFromApp(record), nil
	}
	spec, err := app.inferAppSpec(ctx, record)
	if err != nil {
		return nil, err
	}
	return &vibeShareRecord{
		Spec:           spec,
		PublishableKey: record.PublishableKey,
		CreatedAt:      time.Now().UTC(),
		ExpiresAt:      derefTime(record.ExpiresAt),
	}, nil
}

// inferAppSpec reads the types and data keys an app already uses and turns
// them into a spec the starter can render. Only shapes are read back: record
// values never leave the server, and the spec carries no samples.
func (app *application) inferAppSpec(ctx context.Context, record AppRecord) (vibeSpec, error) {
	var shared Namespace
	_ = app.database.Collection(namespaceCollectionName).
		FindOne(ctx, bson.M{"appId": record.ID, "kind": sharedNamespaceKind}).Decode(&shared)

	cursor, err := app.database.Collection(resourceCollectionName).Find(ctx,
		bson.M{"appId": record.ID},
		options.Find().
			SetProjection(bson.M{"type": 1, "namespaceId": 1, "visibility": 1, "data": 1}).
			SetSort(bson.D{{Key: "createdAt", Value: -1}}).
			SetLimit(kitInferSampleSize),
	)
	if err != nil {
		return vibeSpec{}, err
	}
	var resources []Resource
	if err := cursor.All(ctx, &resources); err != nil {
		return vibeSpec{}, err
	}
	spec := inferSpecFromResources(resources, shared.ID)
	spec.Name = record.DisplayName
	return validateVibeSpec(spec), nil
}

// inferSpecFromResources is the pure core of inferAppSpec.
func inferSpecFromResources(resources []Resource, sharedNamespaceID bson.ObjectID) vibeSpec {
	sections := map[string]*inferredSection{}
	for index, resource := range resources {
		isShared := !sharedNamespaceID.IsZero() && resource.NamespaceID == sharedNamespaceID
		key := "personal/" + resource.Type
		if isShared {
			key = "shared/" + resource.Type
		}
		section := sections[key]
		if section == nil {
			section = &inferredSection{shared: isShared, typ: resource.Type, first: index, fields: map[string]*inferredField{}}
			sections[key] = section
		}
		section.count++
		if resource.Visibility == publicVisibility {
			section.public++
		}
		for dataKey, value := range resource.Data {
			// The renderer reads data[key] verbatim, so only keys that are already
			// in the spec's snake_case form can be shown without renaming data.
			if normalizeVibeFieldKey(dataKey) != dataKey {
				continue
			}
			kind, ok := inferFieldKind(dataKey, value)
			if !ok {
				continue
			}
			field := section.fields[dataKey]
			if field == nil {
				field = &inferredField{key: dataKey, kind: kind}
				section.fields[dataKey] = field
			} else if field.kind != kind {
				field.kind = "text"
			}
			field.count++
		}
	}

	ordered := make([]*inferredSection, 0, len(sections))
	for _, section := range sections {
		ordered = append(ordered, section)
	}
	// Owner (shared) sections first, then the busiest, then first seen.
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].shared != ordered[j].shared {
			return ordered[i].shared
		}
		if ordered[i].count != ordered[j].count {
			return ordered[i].count > ordered[j].count
		}
		return ordered[i].first < ordered[j].first
	})

	spec := vibeSpec{Accent: "teal", Icon: "box", Source: vibeSourceInferred}
	for _, section := range ordered {
		spec.Sections = append(spec.Sections, section.toVibeSection())
	}
	return spec
}

func (section *inferredSection) toVibeSection() vibeSection {
	fields := make([]*inferredField, 0, len(section.fields))
	for _, field := range section.fields {
		fields = append(fields, field)
	}
	sort.Slice(fields, func(i, j int) bool {
		if fields[i].count != fields[j].count {
			return fields[i].count > fields[j].count
		}
		// Keys within one record arrive in map order, so break ties by name to
		// keep the kit deterministic.
		return fields[i].key < fields[j].key
	})

	out := vibeSection{
		Type:  section.typ,
		Title: strings.Title(strings.ReplaceAll(section.typ, "-", " ")), //nolint:staticcheck // ASCII titles only
	}
	hasDate, hasToggle := false, false
	for _, field := range fields {
		out.Fields = append(out.Fields, vibeField{Key: field.key, Kind: field.kind})
		hasDate = hasDate || field.kind == "date"
		hasToggle = hasToggle || field.kind == "toggle"
	}

	switch {
	case section.shared:
		out.Audience = vibeAudienceOwner
	case section.public*2 > section.count:
		out.Audience = vibeAudienceMemberPublic
	default:
		out.Audience = vibeAudienceMember
	}
	switch {
	case hasDate:
		out.View = "timeline"
	case section.shared:
		out.View = "grid"
	case hasToggle:
		out.View = "checklist"
	default:
		out.View = "list"
	}
	return out
}

// inferFieldKind maps a stored data value to the closest field kind. Nested
// objects have no renderer kind and are skipped.
func inferFieldKind(key string, value any) (string, bool) {
	switch typed := value.(type) {
	case bool:
		return "toggle", true
	case int32, int64, float64, int:
		for _, hint := range kitPriceKeys {
			if strings.Contains(key, hint) {
				return "price", true
			}
		}
		return "number", true
	case string:
		switch {
		case kitDatePattern.MatchString(typed):
			return "date", true
		case len(typed) > 120:
			return "longtext", true
		default:
			return "text", true
		}
	case bson.A, []any, []string:
		return "tag", true
	default:
		return "", false
	}
}
