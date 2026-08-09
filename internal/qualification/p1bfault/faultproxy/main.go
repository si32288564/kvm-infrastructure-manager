// Command faultproxy is an opaque TCP qualification proxy. Once armed, it
// forwards one complete TLS record from Gateway to Agent, then discards later
// Gateway responses while preserving Agent-to-Gateway delivery. It is not a
// product component or deployment artifact.
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
	armPath := flag.String("arm-file", "", "file whose presence arms downstream loss")
	activatedPath := flag.String("activated-file", "", "evidence file written after the last forwarded TLS record")
	flag.Parse()
	if *listenAddress == "" || *targetAddress == "" || *armPath == "" || *activatedPath == "" {
		fmt.Fprintln(os.Stderr, "faultproxy: listen, target, arm-file, and activated-file are required")
		os.Exit(2)
	}
	listener, err := net.Listen("tcp", *listenAddress)
	if err != nil {
		fmt.Fprintf(os.Stderr, "faultproxy listen: %v\n", err)
		os.Exit(1)
	}
	defer listener.Close()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		_ = listener.Close()
	}()
	var activated atomic.Bool
	for {
		downstream, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			fmt.Fprintf(os.Stderr, "faultproxy accept: %v\n", err)
			os.Exit(1)
		}
		upstream, err := net.DialTimeout("tcp", *targetAddress, 3*time.Second)
		if err != nil {
			_ = downstream.Close()
			continue
		}
		go bridge(downstream, upstream, *armPath, *activatedPath, &activated)
	}
}

func bridge(agent, gateway net.Conn, armPath, activatedPath string, activated *atomic.Bool) {
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(gateway, agent)
		done <- struct{}{}
	}()
	go func() {
		for {
			record, err := readTLSRecord(gateway)
			if err != nil {
				break
			}
			if activated.Load() {
				continue
			}
			if _, err := agent.Write(record); err != nil {
				break
			}
			if _, err := os.Stat(armPath); err == nil && activated.CompareAndSwap(false, true) {
				_ = os.WriteFile(activatedPath, []byte("gateway_to_agent_receipt_path_blocked\n"), 0o600)
			}
		}
		done <- struct{}{}
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
