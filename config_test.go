package main

import (
	"reflect"
	"testing"
)

func TestGetAllowedOriginsDefaultsBlankValuesToWildcard(t *testing.T) {
	for _, value := range []string{"", "   ", ",, "} {
		t.Run("value="+value, func(t *testing.T) {
			t.Setenv("ALLOWED_ORIGINS", value)
			if origins := getAllowedOrigins(); !reflect.DeepEqual(origins, []string{"*"}) {
				t.Fatalf("origins = %#v, want wildcard", origins)
			}
		})
	}
}

func TestGetAllowedOriginsNormalizesConfiguredOrigins(t *testing.T) {
	t.Setenv("ALLOWED_ORIGINS", " https://app.example.com, https://school.example.edu ")
	if origins := getAllowedOrigins(); !reflect.DeepEqual(origins, []string{"https://app.example.com", "https://school.example.edu"}) {
		t.Fatalf("origins = %#v", origins)
	}
}
