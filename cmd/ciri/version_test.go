package main

import "testing"

func TestVersion_DefaultIsDev(t *testing.T) {
	if Version != "dev" {
		t.Errorf("expected default Version to be \"dev\", got %q", Version)
	}
}

func TestVersion_NonEmpty(t *testing.T) {
	if Version == "" {
		t.Error("Version must not be empty")
	}
}
