//go:build windows

package main

import (
	"fmt"
	"net"

	winio "github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func listen(address string) (net.Listener, error) {
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("PTY pipe ACL: %w", err)
	}
	sid := user.User.Sid.String()
	if sid == "" {
		return nil, fmt.Errorf("PTY pipe ACL has no current-user SID")
	}
	return winio.ListenPipe(address, &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;" + sid + ")"})
}
