package main

import (
	"crypto/tls"
	"net"
	"net/smtp"
)

func smtpStartTLS(conn net.Conn, host string, tc *tls.Config) error {
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer c.Close()
	return c.StartTLS(tc)
}
