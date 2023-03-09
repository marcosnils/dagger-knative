package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v2"
	"knative.dev/pkg/ptr"
)

// FunctionFile is the file used for the serialized form of a function.
const FunctionFile = "func.yaml"

const (
	// DefaultRegistry through which containers of functions will be shuttled.
	DefaultRegistry = "docker.io"

	// DefaultTemplate is the default function signature / environmental context
	// of the resultant function.  All runtimes are expected to have at least
	// one implementation of each supported function signature.  Currently that
	// includes an HTTP Handler ("http") and Cloud Events handler ("events")
	DefaultTemplate = "http"
)

// ErrNotBuilt indicates the function has not yet been built.
var ErrNotBuilt = errors.New("not built")

// ErrNameRequired indicates the operation requires a name to complete.
var ErrNameRequired = errors.New("name required")

// ErrRegistryRequired indicates the operation requires a registry to complete.
var ErrRegistryRequired = errors.New("registry required to build function, please set with `--registry` or the FUNC_REGISTRY environment variable")

// Function
type Function struct {
	// SpecVersion at which this function is known to be compatible.
	// More specifically, it is the highest migration which has been applied.
	// For details see the .Migrated() and .Migrate() methods.
	SpecVersion string `yaml:"specVersion"` // semver format

	// Root on disk at which to find/create source and config files.
	Root string `yaml:"-"`

	// Name of the function.
	Name string `yaml:"name" jsonschema:"pattern=^[a-z0-9]([-a-z0-9]*[a-z0-9])?$"`

	// Runtime is the language plus context.  nodejs|go|quarkus|rust etc.
	Runtime string `yaml:"runtime"`

	// Template for the function.
	Template string `yaml:"-"`

	// Registry at which to store interstitial containers, in the form
	// [registry]/[user].
	Registry string `yaml:"registry"`

	// Optional full OCI image tag in form:
	//   [registry]/[namespace]/[name]:[tag]
	// example:
	//   quay.io/alice/my.function.name
	// Registry is optional and is defaulted to DefaultRegistry
	// example:
	//   alice/my.function.name
	// If Image is provided, it overrides the default of concatenating
	// "Registry+Name:latest" to derive the Image.
	Image string `yaml:"image"`

	// SHA256 hash of the latest image that has been built
	ImageDigest string `yaml:"imageDigest"`

	// Created time is the moment that creation was successfully completed
	// according to the client which is in charge of what constitutes being
	// fully "Created" (aka initialized)
	Created time.Time `yaml:"created"`

	// Invoke defines hints for use when invoking this function.
	// See Client.Invoke for usage.
	Invoke string `yaml:"invoke,omitempty"`

	// BuildSpec define the build properties for a function
	Build BuildSpec `yaml:"build"`

	// RunSpec define the runtime properties for a function
	Run RunSpec `yaml:"run"`

	// DeploySpec define the deployment properties for a function
	Deploy DeploySpec `yaml:"deploy"`
}

// BuildSpec
type BuildSpec struct {
	// Git stores information about an optionally associated git repository.
	Git Git `yaml:"git,omitempty"`

	// BuilderImages define optional explicit builder images to use by
	// builder implementations in leau of the in-code defaults.  They key
	// is the builder's short name.  For example:
	// builderImages:
	//   pack: example.com/user/my-pack-node-builder
	//   s2i: example.com/user/my-s2i-node-builder
	BuilderImages map[string]string `yaml:"builderImages,omitempty"`

	// Optional list of buildpacks to use when building the function
	Buildpacks []string `yaml:"buildpacks"`

	// Builder is the name of the subsystem that will complete the underlying
	// build (pack, s2i, etc)
	Builder string `yaml:"builder" jsonschema:"enum=pack,enum=s2i"`

	// Build Env variables to be set
	BuildEnvs []Env `yaml:"buildEnvs"`
}

// RunSpec
type RunSpec struct {
	// List of volumes to be mounted to the function
	Volumes []Volume `yaml:"volumes"`

	// Env variables to be set
	Envs []Env `yaml:"envs"`
}

// DeploySpec
type DeploySpec struct {
	// Namespace into which the function is deployed on supported platforms.
	Namespace string `yaml:"namespace"`

	// Remote indicates the deployment (and possibly build) process are to
	// be triggered in a remote environment rather than run locally.
	Remote bool `yaml:"remote"`

	// Map containing user-supplied annotations
	// Example: { "division": "finance" }
	Annotations map[string]string `yaml:"annotations"`

	// Options to be set on deployed function (scaling, etc.)
	Options Options `yaml:"options"`

	// Map of user-supplied labels
	Labels []Label `yaml:"labels"`

	// Health endpoints specified by the language pack
	HealthEndpoints HealthEndpoints `yaml:"healthEndpoints"`
}

// HealthEndpoints specify the liveness and readiness endpoints for a Runtime
type HealthEndpoints struct {
	Liveness  string `yaml:"liveness,omitempty"`
	Readiness string `yaml:"readiness,omitempty"`
}

// BuildConfig defines builders and buildpacks
type BuildConfig struct {
	Buildpacks    []string          `yaml:"buildpacks,omitempty"`
	BuilderImages map[string]string `yaml:"builderImages,omitempty"`
}

// NewFunctionWith defaults as provided.
func NewFunctionWith(defaults Function) Function {
	if defaults.SpecVersion == "" {
		defaults.SpecVersion = LastSpecVersion()
	}
	if defaults.Template == "" {
		defaults.Template = DefaultTemplate
	}
	if defaults.Build.BuilderImages == nil {
		defaults.Build.BuilderImages = make(map[string]string)
	}
	if defaults.Deploy.Annotations == nil {
		defaults.Deploy.Annotations = make(map[string]string)
	}
	return defaults
}

// NewFunction from a given path.
// Invalid paths, or no function at path are errors.
// Syntactic errors are returned immediately (yaml unmarshal errors).
// Functions which are syntactically valid are also then logically validated.
// Functions from earlier versions are brought up to current using migrations.
// Migrations are run prior to validators such that validation can omit
// concerning itself with backwards compatibility. Migrators must therefore
// selectively consider the minimal validation necessary to enable migration.
func NewFunction(path string) (f Function, err error) {
	f.Build.BuilderImages = make(map[string]string)
	f.Deploy.Annotations = make(map[string]string)

	// Path defaults to current working directory, and if provided explicitly
	// Path must exist and be a directory
	if path == "" {
		if path, err = os.Getwd(); err != nil {
			return
		}
	}
	f.Root = path // path is not persisted, as this is the purview of the FS

	// Path must exist and be a directory
	fd, err := os.Stat(path)
	if err != nil {
		return f, err
	}
	if !fd.IsDir() {
		return f, fmt.Errorf("function path must be a directory")
	}

	// If no func.yaml in directory, return the default function which will
	// have f.Initialized() == false
	filename := filepath.Join(path, FunctionFile)
	if _, err = os.Stat(filename); err != nil {
		if os.IsNotExist(err) {
			err = nil
		}
		return
	}

	// Path is valid and func.yaml exists: load it
	bb, err := os.ReadFile(filename)
	if err != nil {
		return
	}
	var functionMarshallingError error
	var functionMigrationError error
	if marshallingErr := yaml.Unmarshal(bb, &f); marshallingErr != nil {
		functionMarshallingError = formatUnmarshalError(marshallingErr) // human-friendly unmarshalling errors
	}
	if f, err = f.Migrate(); err != nil {
		functionMigrationError = err
	}
	// Only if migration fail return errors to the user. include marshalling error if present
	if functionMigrationError != nil {
		// returning both  migrations and marshalling errors to the user
		errorText := "Error: \n"
		if functionMarshallingError != nil {
			errorText += "Marshalling: " + functionMarshallingError.Error()
		}
		errorText += "\n" + "Migration: " + functionMigrationError.Error()
		return Function{}, errors.New(errorText)
	}
	return
}

// Validate function is logically correct, returning a bundled, and quite
// verbose, formatted error detailing any issues.
func (f Function) Validate() error {
	if f.Root == "" {
		return errors.New("function root path is required")
	}

	var ctr int
	errs := [][]string{
		validateVolumes(f.Run.Volumes),
		ValidateBuildEnvs(f.Build.BuildEnvs),
		ValidateEnvs(f.Run.Envs),
		validateOptions(f.Deploy.Options),
		ValidateLabels(f.Deploy.Labels),
		validateGit(f.Build.Git),
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("'%v' contains errors:", FunctionFile))

	for _, ee := range errs {
		if len(ee) > 0 {
			b.WriteString("\n") // Precede each group of errors with a linebreak
		}
		for _, e := range ee {
			ctr++
			b.WriteString("\t" + e)
		}
	}

	if ctr == 0 {
		return nil // Return nil if there were no validation errors.
	}

	return errors.New(b.String())
}

var envPattern = regexp.MustCompile(`^{{\s*(\w+)\s*:(\w+)\s*}}$`)

// Interpolate Env slice
// Values with no special format are preserved as simple values.
// Values which do include the interpolation format (begin with {{) but are not
// keyed as "env" are also preserved as is.
// Values properly formatted as {{ env:NAME }} are interpolated (substituted)
// with the value of the local environment variable "NAME", and an error is
// returned if that environment variable does not exist.
func Interpolate(ee []Env) (map[string]string, error) {
	envs := make(map[string]string, len(ee))
	for _, e := range ee {
		// Assert non-nil name.
		if e.Name == nil {
			return envs, errors.New("env name may not be nil")
		}
		// Nil value indicates the resultant map should not include this env var.
		if e.Value == nil {
			continue
		}
		k, v := *e.Name, *e.Value

		// Simple Values are preserved.
		// If not prefixed by {{, no interpolation required (simple value)
		if !strings.HasPrefix(v, "{{") {
			envs[k] = v // no interpolation required.
			continue
		}

		// Values not matching the interpolation pattern are preserved.
		// If not in the form "{{ env:XYZ }}" then return the value as-is for
		//                     0  1   2   3
		// possible match and interpolation in different ways.
		parts := envPattern.FindStringSubmatch(v)
		if len(parts) <= 2 || parts[1] != "env" {
			envs[k] = v
			continue
		}

		// Properly formatted local env var references are interpolated.
		localName := parts[2]
		localValue, ok := os.LookupEnv(localName)
		if !ok {
			return envs, fmt.Errorf("expected environment variable '%v' not found", localName)
		}
		envs[k] = localValue
	}
	return envs, nil
}

// nameFromPath returns the default name for a function derived from a path.
// This consists of the last directory in the given path, if derivable (empty
// paths, paths consisting of all slashes, etc. return the zero value "")
func nameFromPath(path string) string {
	pathParts := strings.Split(strings.TrimRight(path, string(os.PathSeparator)), string(os.PathSeparator))
	return pathParts[len(pathParts)-1]
	/* the above may have edge conditions as it assumes the trailing value
	 * is a directory name.  If errors are encountered, we _may_ need to use the
	 * inbuilt logic in the std lib and either check if the path indicated is a
	 * directory (appending slash) and then run:
					 base := filepath.Base(filepath.Dir(path))
					 if base == string(os.PathSeparator) || base == "." {
									 return "" // Consider it underivable: string zero value
					 }
					 return base
	*/
}

// Write aka (save, serialize, marshal) the function to disk at its path.
// Only valid functions can be written.
// In order to retain built status (staleness checks), the file is only
// modified if the structure actually changes.
func (f Function) Write() (err error) {
	// Skip writing (and dirtying the work tree) if there were no modifications.
	f1, _ := NewFunction(f.Root)
	if reflect.DeepEqual(f, f1) {
		return
	}

	if err = f.Validate(); err != nil {
		return
	}
	path := filepath.Join(f.Root, FunctionFile)
	var bb []byte
	if bb, err = yaml.Marshal(&f); err != nil {
		return
	}
	// TODO: open existing file for writing, such that existing permissions
	// are preserved.
	return os.WriteFile(path, bb, 0o644)
}

// Initialized returns if the function has been initialized.
// Any errors are considered failure (invalid or inaccessible root, config file, etc).
func (f Function) Initialized() bool {
	return !f.Created.IsZero()
}

// ImageWithDigest returns the full reference to the image including SHA256 Digest.
// If Digest is empty, image:tag is returned.
// TODO: Populate this only on a successful deploy, as this results on a dirty
// git tree on every build.
func (f Function) ImageWithDigest() string {
	// Return image, if Digest is empty
	if f.ImageDigest == "" {
		return f.Image
	}

	lastSlashIdx := strings.LastIndexAny(f.Image, "/")
	imageAsBytes := []byte(f.Image)

	part1 := string(imageAsBytes[:lastSlashIdx+1])
	part2 := string(imageAsBytes[lastSlashIdx+1:])

	// Remove tag from the image name and append SHA256 hash instead
	return part1 + strings.Split(part2, ":")[0] + "@" + f.ImageDigest
}

// LabelsMap combines default labels with the labels slice provided.
// It will the resulting slice with ValidateLabels and return a key/value map.
//   - key: EXAMPLE1                            # Label directly from a value
//     value: value1
//   - key: EXAMPLE2                            # Label from the local ENV var
//     value: {{ env:MY_ENV }}
func (f Function) LabelsMap() (map[string]string, error) {
	defaultLabels := []Label{
		{
			Key:   ptr.String(FunctionKey),
			Value: ptr.String(FunctionValue),
		},
		{
			Key:   ptr.String(FunctionNameKey),
			Value: ptr.String(f.Name),
		},
		{
			Key:   ptr.String(FunctionRuntimeKey),
			Value: ptr.String(f.Runtime),
		},
		// --- handle usage of deprecated labels (`boson.dev/function`, `boson.dev/runtime`)
		{
			Key:   ptr.String(DeprecatedFunctionKey),
			Value: ptr.String(FunctionValue),
		},
		{
			Key:   ptr.String(DeprecatedFunctionRuntimeKey),
			Value: ptr.String(f.Runtime),
		},
		// --- end of handling usage of deprecated runtime labels
	}

	labels := append(defaultLabels, f.Deploy.Labels...)
	if err := ValidateLabels(labels); len(err) != 0 {
		return nil, errors.New(strings.Join(err, " "))
	}

	l := map[string]string{}
	for _, label := range labels {
		if label.Value == nil {
			l[*label.Key] = ""
		} else {
			if strings.HasPrefix(*label.Value, "{{") {
				// env variable format is validated above in ValidateLabels
				match := regLocalEnv.FindStringSubmatch(*label.Value)
				l[*label.Key] = os.Getenv(match[1])
			} else {
				l[*label.Key] = *label.Value
			}
		}
	}

	return l, nil
}

// assertEmptyRoot ensures that the directory is empty enough to be used for
// initializing a new function.
func assertEmptyRoot(path string) (err error) {
	// If there exists contentious files (congig files for instance), this function may have already been initialized.
	files, err := contentiousFilesIn(path)
	if err != nil {
		return
	} else if len(files) > 0 {
		return fmt.Errorf("the chosen directory '%v' contains contentious files: %v.  Has the Service function already been created?  Try either using a different directory, deleting the function if it exists, or manually removing the files", path, files)
	}

	// Ensure there are no non-hidden files, and again none of the aforementioned contentious files.
	empty, err := isEffectivelyEmpty(path)
	if err != nil {
		return
	} else if !empty {
		err = errors.New("the directory must be empty of visible files and recognized config files before it can be initialized")
		return
	}
	return
}

// ImageName returns a full image name (OCI container tag) for the
// Function based off of the Function's `Registry` member plus `Name`.
// Used to calculate the final value for .Image when none is provided
// explicitly.
//
// form:    [registry]/[user]/[function]:latest
// example: quay.io/alice/my.function.name:latest
//
// Also nested namespaces should be supported:
// form:    [registry]/[parent]/[user]/[function]:latest
// example: quay.io/project/alice/my.function.name:latest
//
// Registry values which only contain a single token are presumed to
// indicate the namespace at the default registry.
func (f Function) ImageName() (image string, err error) {
	if f.Registry == "" {
		return "", ErrRegistryRequired
	}
	if f.Name == "" {
		return "", ErrNameRequired
	}

	f.Registry = strings.Trim(f.Registry, "/") // too defensive?

	registryTokens := strings.Split(f.Registry, "/")
	if len(registryTokens) == 1 { // only namespace provided: ex. 'alice'
		image = DefaultRegistry + "/" + f.Registry + "/" + f.Name
	} else if len(registryTokens) == 2 || len(registryTokens) == 3 {
		// registry/namespace ('quay.io/alice') or
		// registry/parent-namespace/namespace ('quay.io/project/alice') provided
		image = f.Registry + "/" + f.Name
	} else if len(registryTokens) > 3 { // the name of the image is also provided `quay.io/alice/my.function.name`
		return "", fmt.Errorf("registry should be either 'namespace', 'registry/namespace' or 'registry/parent/namespace', the name of the image will be derived from the function name.")
	}

	// Explicitly append :latest tag.  We expect source control to drive
	// versioning, rather than rely on image tags with explicitly pinned version
	// numbers, as is seen in many serverless solutions.  This will be updated
	// to branch name when we add source-driven canary/ bluegreen deployments.

	// For pinning to an exact container image, see ImageWithDigest
	return image + ":latest", nil
}

// contentiousFiles are files which, if extant, preclude the creation of a
// function rooted in the given directory.
var contentiousFiles = []string{
	FunctionFile,
	".gitignore",
}

// contentiousFilesIn the given directory
func contentiousFilesIn(dir string) (contentious []string, err error) {
	files, err := os.ReadDir(dir)
	for _, file := range files {
		for _, name := range contentiousFiles {
			if file.Name() == name {
				contentious = append(contentious, name)
			}
		}
	}
	return
}

// effectivelyEmpty directories are those which have no visible files
func isEffectivelyEmpty(dir string) (bool, error) {
	// Check for any non-hidden files
	files, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	for _, file := range files {
		if !strings.HasPrefix(file.Name(), ".") {
			return false, nil
		}
	}
	return true, nil
}

// returns true if the given path contains an initialized function.
func hasInitializedFunction(path string) (bool, error) {
	var err error
	filename := filepath.Join(path, FunctionFile)

	if _, err = os.Stat(filename); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err // invalid path or access error
	}
	bb, err := os.ReadFile(filename)
	if err != nil {
		return false, err
	}
	f := Function{}
	if err = yaml.Unmarshal(bb, &f); err != nil {
		return false, err
	}
	if f, err = f.Migrate(); err != nil {
		return false, err
	}
	return f.Initialized(), nil
}

// Format yaml unmarshall error to be more human friendly.
func formatUnmarshalError(err error) error {
	var (
		e      = err.Error()
		rxp    = regexp.MustCompile("not found in type .*")
		header = fmt.Sprintf("'%v' is not valid:\n", FunctionFile)
	)

	if strings.HasPrefix(e, "yaml: unmarshal errors:") {
		e = rxp.ReplaceAllString(e, "is not valid")
		e = strings.Replace(e, "yaml: unmarshal errors:\n", header, 1)
	} else if strings.HasPrefix(e, "yaml:") {
		e = rxp.ReplaceAllString(e, "is not valid")
		e = strings.Replace(e, "yaml: ", header+"  ", 1)
	}

	return errors.New(e)
}

// Regex used during instantiation and validation of various function fields
// by labels, envs, options, etc.
var (
	regWholeSecret      = regexp.MustCompile(`^{{\s*secret:((?:\w|['-]\w)+)\s*}}$`)
	regKeyFromSecret    = regexp.MustCompile(`^{{\s*secret:((?:\w|['-]\w)+):([-._a-zA-Z0-9]+)\s*}}$`)
	regWholeConfigMap   = regexp.MustCompile(`^{{\s*configMap:((?:\w|['-]\w)+)\s*}}$`)
	regKeyFromConfigMap = regexp.MustCompile(`^{{\s*configMap:((?:\w|['-]\w)+):([-._a-zA-Z0-9]+)\s*}}$`)
	regLocalEnv         = regexp.MustCompile(`^{{\s*env:(\w+)\s*}}$`)
)
