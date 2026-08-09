// Command upstreamfaultproxy is an opaque qualification proxy that discards
// Agent-to-Gateway TLS records only after an external arm signal. Gateway-to-
// Agent Command delivery remains intact. It never interprets TLS or authority.
package main

import (
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"
)

const maxTLSRecord = (1 << 14) + 256

func main() {
	listenAddress := flag.String("listen", "", "proxy listen address")
	targetAddress := flag.String("target", "", "Gateway target address")
	armPath := flag.String("arm-file", "", "file whose presence arms upstream loss")
	activatedPath := flag.String("activated-file", "", "evidence file written after the first discarded TLS record")
	flag.Parse()
	if *listenAddress == "" || *targetAddress == "" || *armPath == "" || *activatedPath == "" {
		fmt.Fprintln(os.Stderr, "upstreamfaultproxy: listen, target, arm-file, and activated-file are required")
		os.Exit(2)
	}
	listener, err := net.Listen("tcp", *listenAddress)
	if err != nil {
		fmt.Fprintf(os.Stderr, "upstreamfaultproxy listen: %v\n", err)
		os.Exit(1)
	}
	defer listener.Close()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() { <-stop; _ = listener.Close() }()
	var activated atomic.Bool
	for {
		agent, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			fmt.Fprintf(os.Stderr, "upstreamfaultproxy accept: %v\n", err)
			os.Exit(1)
		}
		gateway, err := net.DialTimeout("tcp", *targetAddress, 3*time.Second)
		if err != nil {
			_ = agent.Close()
			continue
		}
		go bridge(agent, gateway, *armPath, *activatedPath, &activated)
	}
}

func bridge(agent, gateway net.Conn, armPath, activatedPath string, activated *atomic.Bool) {
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(agent, gateway); done <- struct{}{} }()
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			record, err := readTLSRecord(agent)
			if err != nil {
				return
			}
			if _, err := os.Stat(armPath); err == nil {
				if activated.CompareAndSwap(false, true) {
					_ = os.WriteFile(activatedPath, []byte("agent_to_gateway_result_path_blocked\n"), 0o600)
				}
				continue
			}
			if _, err := gateway.Write(record); err != nil {
				return
			}
		}
	}()
	<-done
	_ = agent.Close()
	_ = gateway.Close()
}

func readTLSRecord(reader io.Reader) ([]byte, error) {
	header := make([]byte, 5)
	if _, err := io.ReadFull(reader, header); err != nil {
		return nil, err
	}
	length := int(binary.BigEndian.Uint16(header[3:5]))
	if length < 1 || length > maxTLSRecord {
		return nil, errors.New("invalid TLS record length")
	}
	record := make([]byte, 5+length)
	copy(record, header)
	if _, err := io.ReadFull(reader, record[5:]); err != nil {
		return nil, err
	}
	return record, nil
}
