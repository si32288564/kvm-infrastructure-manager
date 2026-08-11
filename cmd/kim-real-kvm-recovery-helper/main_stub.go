//go:build !libvirt || !cgo

package main

import "log"

func main() {
	log.Fatal("kim-real-kvm-recovery-helper requires -tags libvirt and cgo")
}
