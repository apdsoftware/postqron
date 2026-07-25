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
	"strings"

	"gopkg.in/yaml.v3"
)

const FeatureSchemaVersion = 1

var (
	featureIDPattern  = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[.-][a-z0-9]+)*$`)
	semverPattern     = regexp.MustCompile(`^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$`)
	validKinds        = map[string]struct{}{"api": {}, "web": {}, "worker": {}}
	validVisibility   = map[string]struct{}{"public": {}, "private": {}}
	httpMethodPattern = regexp.MustCompile(`^(?:GET|HEAD|POST|PUT|PATCH|DELETE|OPTIONS)$`)
	handlerPattern    = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]*$`)
	layoutNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
)

type Entrypoints struct {
	Server string `yaml:"server"`
	Web    string `yaml:"web"`
}

type ServerRoute struct {
	Path       string   `yaml:"path"`
	Handler    string   `yaml:"handler"`
	Methods    []string `yaml:"methods"`
	Visibility string   `yaml:"visibility"`
}

type ServerModule struct {
	Routes []ServerRoute `yaml:"routes"`
}

type WebRoute struct {
	Name       string   `yaml:"name"`
	Path       string   `yaml:"path"`
	File       string   `yaml:"file"`
	Visibility string   `yaml:"visibility"`
	Middleware []string `yaml:"middleware"`
}

type WebLayout struct {
	Name string `yaml:"name"`
	File string `yaml:"file"`
}

type WebMiddleware struct {
	Name   string `yaml:"name"`
	File   string `yaml:"file"`
	Global bool   `yaml:"global"`
}

type WebModule struct {
	Routes     []WebRoute      `yaml:"routes"`
	Layouts    []WebLayout     `yaml:"layouts"`
	Components []string        `yaml:"components"`
	Plugins    []string        `yaml:"plugins"`
	Middleware []WebMiddleware `yaml:"middleware"`
}

type Manifest struct {
	SchemaVersion int          `yaml:"schema_version"`
	ID            string       `yaml:"id"`
	Kind          string       `yaml:"kind"`
	Version       string       `yaml:"version"`
	Entrypoint    string       `yaml:"entrypoint"`
	Entrypoints   Entrypoints  `yaml:"entrypoints"`
	Dependencies  []string     `yaml:"dependencies"`
	Migrations    []string     `yaml:"migrations"`
	Required      *bool        `yaml:"required"`
	Server        ServerModule `yaml:"server"`
	Web           WebModule    `yaml:"web"`
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

	return ResolveOrder(features)
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
	for host, entrypoint := range map[string]string{
		"server": manifest.ServerEntrypoint(),
		"web":    manifest.WebEntrypoint(),
	} {
		if entrypoint == "" {
			continue
		}
		if err := assertLocalFile(directory, entrypoint); err != nil {
			return Feature{}, fmt.Errorf("%s %s entrypoint: %w", manifest.ID, host, err)
		}
	}
	if manifest.Kind == "worker" && manifest.Entrypoint != "" {
		if err := assertLocalFile(directory, manifest.Entrypoint); err != nil {
			return Feature{}, fmt.Errorf("%s worker entrypoint: %w", manifest.ID, err)
		}
	}
	for _, migration := range manifest.Migrations {
		if err := assertLocalFile(directory, migration); err != nil {
			return Feature{}, fmt.Errorf("%s migration: %w", manifest.ID, err)
		}
	}
	for _, route := range manifest.Web.Routes {
		if err := assertLocalFile(directory, route.File); err != nil {
			return Feature{}, fmt.Errorf("%s web route %q: %w", manifest.ID, route.Path, err)
		}
	}
	for _, layout := range manifest.Web.Layouts {
		if err := assertLocalFile(directory, layout.File); err != nil {
			return Feature{}, fmt.Errorf("%s web layout %q: %w", manifest.ID, layout.Name, err)
		}
	}
	for _, componentDirectory := range manifest.Web.Components {
		if err := assertLocalDirectory(directory, componentDirectory); err != nil {
			return Feature{}, fmt.Errorf("%s web components: %w", manifest.ID, err)
		}
	}
	for _, plugin := range manifest.Web.Plugins {
		if err := assertLocalFile(directory, plugin); err != nil {
			return Feature{}, fmt.Errorf("%s web plugin: %w", manifest.ID, err)
		}
	}
	for _, middleware := range manifest.Web.Middleware {
		if err := assertLocalFile(directory, middleware.File); err != nil {
			return Feature{}, fmt.Errorf("%s web middleware %q: %w", manifest.ID, middleware.Name, err)
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
	if manifest.Entrypoint == "" &&
		manifest.Entrypoints.Server == "" &&
		manifest.Entrypoints.Web == "" {
		return errors.New("entrypoint or entrypoints.server/web must be a non-empty path")
	}
	if manifest.Entrypoint != "" &&
		(manifest.Entrypoints.Server != "" || manifest.Entrypoints.Web != "") {
		return errors.New("entrypoint cannot be combined with entrypoints")
	}
	for _, dependency := range manifest.Dependencies {
		if !featureIDPattern.MatchString(dependency) {
			return fmt.Errorf("dependency %q is not a valid feature identifier", dependency)
		}
	}
	if len(manifest.Server.Routes) > 0 && manifest.ServerEntrypoint() == "" {
		return errors.New("server routes require entrypoints.server")
	}
	for index, route := range manifest.Server.Routes {
		if err := validateServerRoute(route); err != nil {
			return fmt.Errorf("server.routes[%d]: %w", index, err)
		}
	}
	if hasWebComposition(manifest.Web) && manifest.WebEntrypoint() == "" {
		return errors.New("web composition requires entrypoints.web")
	}
	for index, route := range manifest.Web.Routes {
		if err := validateWebRoute(route); err != nil {
			return fmt.Errorf("web.routes[%d]: %w", index, err)
		}
	}
	for index, layout := range manifest.Web.Layouts {
		if !layoutNamePattern.MatchString(layout.Name) {
			return fmt.Errorf("web.layouts[%d].name is not valid", index)
		}
		if layout.File == "" {
			return fmt.Errorf("web.layouts[%d].file must be a non-empty path", index)
		}
	}
	for index, componentDirectory := range manifest.Web.Components {
		if componentDirectory == "" {
			return fmt.Errorf("web.components[%d] must be a non-empty path", index)
		}
	}
	for index, plugin := range manifest.Web.Plugins {
		if plugin == "" {
			return fmt.Errorf("web.plugins[%d] must be a non-empty path", index)
		}
	}
	for index, middleware := range manifest.Web.Middleware {
		if !layoutNamePattern.MatchString(middleware.Name) {
			return fmt.Errorf("web.middleware[%d].name is not valid", index)
		}
		if middleware.File == "" {
			return fmt.Errorf("web.middleware[%d].file must be a non-empty path", index)
		}
	}
	return nil
}

func validateServerRoute(route ServerRoute) error {
	if err := validateRoutePath(route.Path); err != nil {
		return err
	}
	if strings.HasPrefix(route.Path, "/api/v1") {
		return errors.New("path must be relative to the /api/v1 mount")
	}
	if !handlerPattern.MatchString(route.Handler) {
		return errors.New("handler is not a valid handler identifier")
	}
	if len(route.Methods) == 0 {
		return errors.New("methods must contain at least one HTTP method")
	}
	seenMethods := make(map[string]struct{}, len(route.Methods))
	for _, method := range route.Methods {
		if !httpMethodPattern.MatchString(method) {
			return fmt.Errorf("method %q is not supported", method)
		}
		if _, duplicate := seenMethods[method]; duplicate {
			return fmt.Errorf("method %q is declared more than once", method)
		}
		seenMethods[method] = struct{}{}
	}
	if _, ok := validVisibility[route.Visibility]; !ok {
		return errors.New("visibility must be public or private")
	}
	return nil
}

func validateWebRoute(route WebRoute) error {
	if err := validateRoutePath(route.Path); err != nil {
		return err
	}
	if route.Name != "" && !handlerPattern.MatchString(route.Name) {
		return errors.New("name is not a valid route identifier")
	}
	if route.File == "" {
		return errors.New("file must be a non-empty path")
	}
	if _, ok := validVisibility[route.Visibility]; !ok {
		return errors.New("visibility must be public or private")
	}
	if route.Visibility == "private" && len(route.Middleware) == 0 {
		return errors.New("private routes require explicit middleware")
	}
	for _, middleware := range route.Middleware {
		if !layoutNamePattern.MatchString(middleware) {
			return fmt.Errorf("middleware %q is not a valid middleware name", middleware)
		}
	}
	return nil
}

func validateRoutePath(path string) error {
	if path == "" || path[0] != '/' {
		return errors.New("path must start with /")
	}
	if strings.ContainsAny(path, "?#") {
		return errors.New("path cannot contain a query or fragment")
	}
	return nil
}

func hasWebComposition(module WebModule) bool {
	return len(module.Routes) > 0 ||
		len(module.Layouts) > 0 ||
		len(module.Components) > 0 ||
		len(module.Plugins) > 0 ||
		len(module.Middleware) > 0
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

func assertLocalDirectory(directory, configuredPath string) error {
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
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", configuredPath)
	}
	return nil
}

func (manifest Manifest) ServerEntrypoint() string {
	if manifest.Entrypoints.Server != "" {
		return manifest.Entrypoints.Server
	}
	if manifest.Kind == "api" {
		return manifest.Entrypoint
	}
	return ""
}

func (manifest Manifest) WebEntrypoint() string {
	if manifest.Entrypoints.Web != "" {
		return manifest.Entrypoints.Web
	}
	if manifest.Kind == "web" {
		return manifest.Entrypoint
	}
	return ""
}

func (manifest Manifest) IsRequired() bool {
	return manifest.Required == nil || *manifest.Required
}

func (manifest Manifest) SupportsKind(kind string) bool {
	switch kind {
	case "api":
		return manifest.ServerEntrypoint() != ""
	case "web":
		return manifest.WebEntrypoint() != ""
	case "worker":
		return manifest.Kind == "worker"
	default:
		return false
	}
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

// FilterKind selects executable features for a host while preserving the
// dependency order produced by Discover. Discovery must happen before
// filtering so dependencies owned by another host kind are still validated.
func FilterKind(features []Feature, kinds ...string) ([]Feature, error) {
	requested := make(map[string]struct{}, len(kinds))
	for _, kind := range kinds {
		if _, ok := validKinds[kind]; !ok {
			return nil, fmt.Errorf("unknown feature kind %q", kind)
		}
		requested[kind] = struct{}{}
	}

	filtered := make([]Feature, 0, len(features))
	for _, feature := range features {
		for kind := range requested {
			if feature.Manifest.SupportsKind(kind) {
				filtered = append(filtered, feature)
				break
			}
		}
	}
	return filtered, nil
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
