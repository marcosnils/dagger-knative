package main

import (
	"context"
	"errors"

	"dagger.io/dagger"
)

func build(ctx context.Context) error {
	c := getDaggerClient(ctx)
	defer c.Close()

	appDir := c.Host().Directory(".", dagger.HostDirectoryOpts{
		Exclude: []string{"ci"},
	})

	ctr := getGoContainer(c)

	_, err := ctr.WithMountedDirectory("/app", appDir).
		WithExec([]string{"go", "build", "./..."}).
		ExitCode(ctx)
	if err != nil {
		return errors.New(err.Error())
	}

	return nil
}

func getGoContainer(c *dagger.Client) *dagger.Container {
	pkgCache := c.CacheVolume("gopkg")
	buildCache := c.CacheVolume("gocache")
	return c.Container().From("golang:1.20.1").
		WithMountedCache("/go/", pkgCache).
		WithMountedCache("/root/.cache/go-build", buildCache).
		WithWorkdir("/app")
}
