package main

import (
	"os"

	"github.com/kvm-infrastructure-manager/kvm-infrastructure-manager/internal/componentmain"
)

func main() {
	os.Exit(componentmain.Run("kim-api", os.Args[1:], os.Stdout, os.Stderr))
}
