package main

import "testing"

func TestValidateBootstrapAdminCredentials(t *testing.T) {
	if email, err := validateBootstrapAdminCredentials("", ""); err != nil || email != "" {
		t.Fatalf("empty admin credentials = %q, %v", email, err)
	}
	if _, err := validateBootstrapAdminCredentials("admin@example.com", ""); err == nil {
		t.Fatal("partial admin credentials were accepted")
	}
	email, err := validateBootstrapAdminCredentials(" Admin@Example.com ", "this-is-a-long-admin-password")
	if err != nil {
		t.Fatalf("valid admin credentials rejected: %v", err)
	}
	if email != "admin@example.com" {
		t.Fatalf("admin email = %q, want normalized email", email)
	}
}
