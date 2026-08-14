package main

import (
	"context"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/kvm-infrastructure-manager/terraform-provider-kim/internal/provider"
)

var version = "0.1.0-experimental"

func main() {
	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/kvm-infrastructure-manager/kim",
	})
	if err != nil {
		log.Fatal(err)
	}
}
