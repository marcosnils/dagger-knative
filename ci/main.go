package main

import (
	"context"
	"log"
	"os"
)

func main() {
	var err error
	ctx := context.Background()

	if len(os.Args) < 2 {
		log.Fatalln("please specify command")
	}

	switch os.Args[1] {

	case "build":
		err = build(ctx)

	case "scan-local":
		err = scan(ctx, "dir:.")

	case "package":
		err = pkg(ctx, false)

	case "push":
		err = push(ctx)

	default:
		log.Fatalln("invalid command specified")
	}

	if err != nil {
		panic(err)
	}
}

