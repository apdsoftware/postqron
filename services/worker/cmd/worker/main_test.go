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

func TestShouldSkipRunOnceDatabase(t *testing.T) {
	cases := []struct {
		name        string
		runOnce     bool
		databaseURL string
		want        bool
	}{
		{
			name:        "run once without database skips",
			runOnce:     true,
			databaseURL: "",
			want:        true,
		},
		{
			name:        "run once with whitespace database skips",
			runOnce:     true,
			databaseURL: " \t ",
			want:        true,
		},
		{
			name:        "run once with database keeps runtime execution",
			runOnce:     true,
			databaseURL: "postgres://postqron:test@127.0.0.1:5432/postqron?sslmode=disable",
			want:        false,
		},
		{
			name:        "continuous mode still requires database",
			runOnce:     false,
			databaseURL: "",
			want:        false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldSkipRunOnceDatabase(tc.runOnce, tc.databaseURL); got != tc.want {
				t.Fatalf(
					"shouldSkipRunOnceDatabase(%t, %q) = %t, want %t",
					tc.runOnce,
					tc.databaseURL,
					got,
					tc.want,
				)
			}
		})
	}
}
