package main

import (
	"context"
	"os"

	"dagger.io/dagger"
)

func getDaggerClient(ctx context.Context) *dagger.Client {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		panic(err)
	}

	return c
}
