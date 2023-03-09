package main

import "context"

func push(ctx context.Context) error {
	return pkg(ctx, true)
}
