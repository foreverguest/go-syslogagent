package main

import (
	"net"
	"time"
)

// SendSyslog sends a syslog message via UDP to addr (host:port)
func SendSyslog(msg, addr string) error {
	if debugMode {
		Debug("Sending syslog message to %s: %s", addr, msg)
	}
	conn, err := net.DialTimeout("udp", addr, 3*time.Second)
	if err != nil {
		LogError("failed to dial syslog server %s: %v", addr, err)
		return err
	}
	defer conn.Close()

	_, err = conn.Write([]byte(msg))
	if err != nil {
		LogError("failed to send syslog message to %s: %v", addr, err)
	}
	return err
}
