package main

import (
	"flag"
	"fmt"
	"log"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/doclint"
)

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()

	report, err := doclint.Check(*root)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("documentation contracts valid: %d requirements, %d test contracts, %d links\n", report.Requirements, report.TestContracts, report.Links)
}
