package main

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestDefaultFeatureRootsIncludeAllDependencyRoots(t *testing.T) {
	got := filepath.SplitList(defaultFeatureRoots())
	want := []string{
		"services/worker/features",
		"services/api/features",
		"features",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("defaultFeatureRoots() = %v, want %v", got, want)
	}
}
