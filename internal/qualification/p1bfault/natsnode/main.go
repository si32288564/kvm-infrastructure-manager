// Command natsnode runs one real NATS Server process for the P1-B fault
// qualification lane. It is not a product component or deployment artifact.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nats-io/nats-server/v2/server"
)

func main() {
	config := flag.String("config", "", "NATS Server configuration file")
	flag.Parse()
	if *config == "" {
		fmt.Fprintln(os.Stderr, "natsnode: -config is required")
		os.Exit(2)
	}
	options, err := server.ProcessConfigFile(*config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "natsnode configuration: %v\n", err)
		os.Exit(2)
	}
	options.NoSigs = true
	instance, err := server.NewServer(options)
	if err != nil {
		fmt.Fprintf(os.Stderr, "natsnode create: %v\n", err)
		os.Exit(1)
	}
	instance.ConfigureLogger()
	go instance.Start()
	if !instance.ReadyForConnections(15 * time.Second) {
		fmt.Fprintln(os.Stderr, "natsnode did not become ready")
		os.Exit(1)
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	instance.Shutdown()
	instance.WaitForShutdown()
}
