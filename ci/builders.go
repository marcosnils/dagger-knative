package main

import "fmt"

// ErrRuntimeRequired
type ErrRuntimeRequired struct {
	Builder string
}

func (e ErrRuntimeRequired) Error() string {
	return fmt.Sprintf("runtime required to choose a default '%v' builder image", e.Builder)
}

// ErrNoDefaultImage
type ErrNoDefaultImage struct {
	Builder string
	Runtime string
}

func (e ErrNoDefaultImage) Error() string {
	return fmt.Sprintf("the '%v' runtime defines no default '%v' builder image", e.Runtime, e.Builder)
}

const (
	Pack    = "pack"
	S2I     = "s2i"
	Default = Pack
)

var DefaultBuilderImages = map[string]string{
	"node":       "gcr.io/paketo-buildpacks/builder:base",
	"nodejs":     "gcr.io/paketo-buildpacks/builder:base",
	"typescript": "gcr.io/paketo-buildpacks/builder:base",
	"go":         "gcr.io/paketo-buildpacks/builder:base",
	"python":     "gcr.io/paketo-buildpacks/builder:base",
	"quarkus":    "gcr.io/paketo-buildpacks/builder:base",
	"rust":       "gcr.io/paketo-buildpacks/builder:base",
	"springboot": "gcr.io/paketo-buildpacks/builder:base",
}

// Image is a convenience function for choosing the correct builder image
// given a function, a builder, and defaults grouped by runtime.
//   - ErrRuntimeRequired if no runtime was provided on the given function
//   - ErrNoDefaultImage if the function has no builder image already defined
//     for the given runtime and there is no default in the provided map.
func Image(f Function, builder string, defaults map[string]string) (string, error) {
	v, ok := f.Build.BuilderImages[builder]
	if ok {
		return v, nil // found value
	}
	if f.Runtime == "" {
		return "", ErrRuntimeRequired{Builder: builder}
	}
	v, ok = defaults[f.Runtime]
	if ok {
		return v, nil // Found default
	}
	return "", ErrNoDefaultImage{Builder: builder, Runtime: f.Runtime}
}
