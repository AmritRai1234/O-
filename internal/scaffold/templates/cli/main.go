package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	name := flag.String("name", "world", "who to greet")
	flag.Parse()
	if flag.NArg() > 0 {
		*name = flag.Arg(0)
	}
	fmt.Fprintf(os.Stdout, "hello, %s — from {{NAME}} (cli template)\n", *name)
}
