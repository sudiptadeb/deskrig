package main

// The Linux deskrig that Linux VMs run, built into this one by build.sh so that the two are
// always the same version and nothing is downloaded.

import "embed"

//go:embed all:guest
var guestFiles embed.FS

func linuxDeskrig() []byte {
	data, err := guestFiles.ReadFile("guest/deskrig-linux-arm64")
	if err != nil {
		fail(2, "this deskrig was built without its Linux half, which Linux VMs run: build it with ./build.sh")
	}
	return data
}
