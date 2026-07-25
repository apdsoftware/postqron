package main

import (
	"os"
	"reflect"
	"testing"
)

func TestDefaultFeatureRootsIncludeFoundationAndAutonomousSlices(t *testing.T) {
	t.Setenv("POSTQRON_FEATURE_ROOTS", "")
	got := featureRoots("POSTQRON_FEATURE_ROOTS", defaultFeatureRoots())
	want := []string{"services/api/features", "features"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("featureRoots() = %v, want %v", got, want)
	}
	if os.PathListSeparator == 0 {
		t.Fatal("path list separator is unavailable")
	}
}
