package main

import (
	"context"
	"fmt"
	"io/ioutil"

	"dagger.io/dagger"
)

func scan(ctx context.Context, source string) error {
	c := getDaggerClient(ctx)
	defer c.Close()

	scanCache := c.CacheVolume("grype")

	appDir := c.Host().Directory(".", dagger.HostDirectoryOpts{
		Exclude: []string{"ci"},
	})

	dockerSock := c.Host().UnixSocket("/var/run/docker.sock")

	_, err := c.Container().From("anchore/grype").
		WithUnixSocket("/var/run/docker.sock", dockerSock).
		WithMountedCache("/.cache", scanCache).
		WithMountedDirectory("/app", appDir).
		WithWorkdir("/app").
		WithExec([]string{source, "--output", "sarif", "--file", "results.sarif", "-vv"}).
		File("results.sarif").Export(ctx, "results.sarif")
	if err != nil {
		return err
	}

	scanResults, err := ioutil.ReadFile("results.sarif")
	if err != nil {
		return err
	}

	fmt.Println(string(scanResults))

	return nil
}
