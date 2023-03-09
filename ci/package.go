package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"dagger.io/dagger"
)

var (
	remote        bool
	kubeNamespace string
)

func pkg(ctx context.Context, push bool) error {
	fs := flag.NewFlagSet("package", flag.ExitOnError)
	fs.BoolVar(&remote, "remote", false, "Performs remote build")
	fs.StringVar(&kubeNamespace, "kube-namespace", "default", "Kube namespace to create the Dagger pod")
	fs.Parse(os.Args[2:])

	fn, err := NewFunction(".")
	if err != nil {
		return err
	}

	if remote {

		fmt.Println("Starting remote build")

		buildImage, err := Image(fn, fn.Build.Builder, DefaultBuilderImages)
		if err != nil {
			return err
		}

		err = setupRemoteEngine(ctx)
		if err != nil {
			return err
		}

		c := getDaggerClient(ctx)
		defer c.Close()

		appDir := c.Host().Directory(".", dagger.HostDirectoryOpts{})

		dockerConfig := c.Host().Directory("/home/marcos/.docker/", dagger.HostDirectoryOpts{}).File("config.json")
		layersCache := c.CacheVolume("packs_layers")
		platformCache := c.CacheVolume("packs_platform")
		cacheDir := c.CacheVolume("packs_cache")

		_, err = c.Container().WithUser("root").From(buildImage).
			WithMountedDirectory("/workspace", appDir).
			WithMountedFile("/workspace/config.json", dockerConfig).
			WithMountedCache("/layers", layersCache).
			WithMountedCache("/platform", platformCache).
			WithMountedCache("/workspace/cache", cacheDir).
			WithUser("root").
			WithExec([]string{"chown", "-R", "1000:1000", "/workspace", "/layers", "/platform"}).
			WithUser("cnb").
			WithEnvVariable("DOCKER_CONFIG", "/workspace").
			WithExec([]string{
				"/cnb/lifecycle/creator",
				"-cache-dir=/workspace/cache",
				fn.Image,
			}).ExitCode(ctx)
		if err != nil {
			return err
		}

	} else {
		c := getDaggerClient(ctx)

		defer c.Close()

		appDir := c.Host().Directory(".", dagger.HostDirectoryOpts{})

		dockerSock := c.Host().UnixSocket("/var/run/docker.sock")
		funcBinary := c.Host().Directory("/home/marcos/Projects/func").File("func")
		dockerConfig := c.Host().Directory("/home/marcos/.docker/", dagger.HostDirectoryOpts{}).File("config.json").Secret()

		buildCmd := []string{"build", "-v", "-b", "pack"}

		if push {
			buildCmd = append(buildCmd, "-u")
		}

		_, err = c.Container().From("alpine").WithMountedFile("/func", funcBinary).
			WithEntrypoint([]string{"/func"}).
			WithUnixSocket("/var/run/docker.sock", dockerSock).
			WithMountedSecret("/root/.docker/config.json", dockerConfig).
			WithMountedDirectory("/app", appDir).
			WithWorkdir("/app").
			WithExec(buildCmd).ExitCode(ctx)
		if err != nil {
			return err
		}

	}

	err = scan(ctx, fn.Image)
	if err != nil {
		return err
	}

	return nil
}
