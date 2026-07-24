package runtime

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"

	"gopkg.in/yaml.v3"
)

const FeatureSchemaVersion = 1

var (
	featureIDPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[.-][a-z0-9]+)*$`)
	semverPattern    = regexp.MustCompile(`^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$`)
	validKinds       = map[string]struct{}{"api": {}, "web": {}, "worker": {}}
)

type Manifest struct {
	SchemaVersion int      `yaml:"schema_version"`
	ID            string   `yaml:"id"`
	Kind          string   `yaml:"kind"`
	Version       string   `yaml:"version"`
	Entrypoint    string   `yaml:"entrypoint"`
	Dependencies  []string `yaml:"dependencies"`
	Migrations    []string `yaml:"migrations"`
}

type Feature struct {
	Directory    string
	ManifestPath string
	Manifest     Manifest
}

func Discover(roots ...string) ([]Feature, error) {
	var features []Feature
	ids := make(map[string]struct{})

	for _, configuredRoot := range roots {
		root, err := filepath.Abs(configuredRoot)
		if err != nil {
			return nil, fmt.Errorf("resolve feature root %q: %w", configuredRoot, err)
		}

		var manifests []string
		err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !entry.IsDir() && entry.Name() == "feature.yaml" {
				manifests = append(manifests, path)
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("scan feature root %q: %w", configuredRoot, err)
		}
		slices.Sort(manifests)

		for _, manifestPath := range manifests {
			feature, err := readFeature(manifestPath)
			if err != nil {
				return nil, err
			}
			if _, duplicate := ids[feature.Manifest.ID]; duplicate {
				return nil, fmt.Errorf("duplicate feature id %q", feature.Manifest.ID)
			}
			ids[feature.Manifest.ID] = struct{}{}
			features = append(features, feature)
		}
	}

	if _, err := ResolveOrder(features); err != nil {
		return nil, err
	}
	slices.SortFunc(features, func(left, right Feature) int {
		return compare(left.Manifest.ID, right.Manifest.ID)
	})
	return features, nil
}

func readFeature(manifestPath string) (Feature, error) {
	source, err := os.ReadFile(manifestPath)
	if err != nil {
		return Feature{}, fmt.Errorf("read feature manifest %q: %w", manifestPath, err)
	}

	var manifest Manifest
	decoder := yaml.NewDecoder(bytes.NewReader(source))
	decoder.KnownFields(true)
	if err := decoder.Decode(&manifest); err != nil {
		return Feature{}, fmt.Errorf("decode feature manifest %q: %w", manifestPath, err)
	}
	if err := validateManifest(manifest); err != nil {
		return Feature{}, fmt.Errorf("validate feature manifest %q: %w", manifestPath, err)
	}

	directory := filepath.Dir(manifestPath)
	if err := assertLocalFile(directory, manifest.Entrypoint); err != nil {
		return Feature{}, fmt.Errorf("%s entrypoint: %w", manifest.ID, err)
	}
	for _, migration := range manifest.Migrations {
		if err := assertLocalFile(directory, migration); err != nil {
			return Feature{}, fmt.Errorf("%s migration: %w", manifest.ID, err)
		}
	}

	return Feature{
		Directory:    directory,
		ManifestPath: manifestPath,
		Manifest:     manifest,
	}, nil
}

func validateManifest(manifest Manifest) error {
	if manifest.SchemaVersion != FeatureSchemaVersion {
		return fmt.Errorf("schema_version must be %d", FeatureSchemaVersion)
	}
	if !featureIDPattern.MatchString(manifest.ID) {
		return errors.New("id is not a valid feature identifier")
	}
	if _, ok := validKinds[manifest.Kind]; !ok {
		return errors.New("kind must be api, web, or worker")
	}
	if !semverPattern.MatchString(manifest.Version) {
		return errors.New("version must use semantic versioning")
	}
	if manifest.Entrypoint == "" {
		return errors.New("entrypoint must be a non-empty path")
	}
	for _, dependency := range manifest.Dependencies {
		if !featureIDPattern.MatchString(dependency) {
			return fmt.Errorf("dependency %q is not a valid feature identifier", dependency)
		}
	}
	return nil
}

func assertLocalFile(directory, configuredPath string) error {
	if filepath.IsAbs(configuredPath) {
		return errors.New("path must be relative to its feature directory")
	}
	candidate := filepath.Clean(filepath.Join(directory, configuredPath))
	relativePath, err := filepath.Rel(directory, candidate)
	if err != nil {
		return fmt.Errorf("resolve local path: %w", err)
	}
	if relativePath == ".." || filepath.IsAbs(relativePath) ||
		len(relativePath) > 3 && relativePath[:3] == ".."+string(filepath.Separator) {
		return errors.New("path escapes its feature directory")
	}
	info, err := os.Lstat(candidate)
	if err != nil {
		return fmt.Errorf("%s: %w", configuredPath, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", configuredPath)
	}
	return nil
}

func ResolveOrder(features []Feature) ([]Feature, error) {
	byID := make(map[string]Feature, len(features))
	for _, feature := range features {
		byID[feature.Manifest.ID] = feature
	}

	const (
		unvisited = iota
		visiting
		visited
	)
	state := make(map[string]int, len(features))
	ordered := make([]Feature, 0, len(features))

	var visit func(Feature) error
	visit = func(feature Feature) error {
		id := feature.Manifest.ID
		switch state[id] {
		case visited:
			return nil
		case visiting:
			return fmt.Errorf("feature dependency cycle includes %q", id)
		}
		state[id] = visiting

		dependencies := slices.Clone(feature.Manifest.Dependencies)
		slices.Sort(dependencies)
		for _, dependency := range dependencies {
			target, ok := byID[dependency]
			if !ok {
				return fmt.Errorf("%s depends on missing feature %s", id, dependency)
			}
			if err := visit(target); err != nil {
				return err
			}
		}
		state[id] = visited
		ordered = append(ordered, feature)
		return nil
	}

	sorted := slices.Clone(features)
	slices.SortFunc(sorted, func(left, right Feature) int {
		return compare(left.Manifest.ID, right.Manifest.ID)
	})
	for _, feature := range sorted {
		if err := visit(feature); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}

func compare(left, right string) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}
