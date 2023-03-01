package main

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"

	"dagger.io/dagger"
)

func main() {
	var err error
	ctx := context.Background()

	if len(os.Args) < 2 {
		log.Fatalln("please specify command")
	}

	switch os.Args[1] {
	case "scan":

		err = scan(ctx)
	case "build":

		err = build(ctx)

	case "package":

		err = pkg(ctx)

	default:
		log.Fatalln("invalid command specified")
	}

	if err != nil {
		panic(err)
	}
}

func pkg(ctx context.Context) error {
	c := getDaggerClient(ctx)

	defer c.Close()

	appDir := c.Host().Directory(".", dagger.HostDirectoryOpts{})

	dockerSock := c.Host().UnixSocket("/var/run/docker.sock")

	_, err := c.Container().From("ghcr.io/knative/func/func").
		WithUnixSocket("/var/run/docker.sock", dockerSock).
		WithMountedDirectory("/app", appDir).
		WithWorkdir("/app").
		WithExec([]string{"build", "-v", "-b", "pack"}).ExitCode(ctx)
	if err != nil {
		return err
	}

	return nil
}

func scan(ctx context.Context) error {
	c := getDaggerClient(ctx)
	defer c.Close()

	scanCache := c.CacheVolume("grype")

	appDir := c.Host().Directory(".", dagger.HostDirectoryOpts{
		Exclude: []string{"ci"},
	})

	_, err := c.Container().From("anchore/grype").
		WithMountedCache("/.cache", scanCache).
		WithMountedDirectory("/app", appDir).
		WithWorkdir("/app").
		WithExec([]string{"dir:.", "--output", "sarif", "--file", "results.sarif"}).
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

func build(ctx context.Context) error {
	c := getDaggerClient(ctx)
	defer c.Close()

	appDir := c.Host().Directory(".", dagger.HostDirectoryOpts{
		Exclude: []string{"ci"},
	})

	pkgCache := c.CacheVolume("gopkg")
	buildCache := c.CacheVolume("gocache")

	_, err := c.Container().From("golang:1.20.1").
		WithMountedCache("/go/", pkgCache).
		WithMountedCache("/root/.cache/go-build", buildCache).
		WithMountedDirectory("/app", appDir).
		WithWorkdir("/app").
		WithExec([]string{"go", "build", "./..."}).
		ExitCode(ctx)
	if err != nil {
		return errors.New(err.Error())
	}

	return nil
}

func getDaggerClient(ctx context.Context) *dagger.Client {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		panic(err)
	}

	return c
}
