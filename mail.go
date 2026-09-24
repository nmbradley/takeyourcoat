package main

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// messageID returns a random Message-ID at the domain of fromAddr.
func messageID(fromAddr string) string {
	b := make([]byte, 16)
	rand.Read(b)
	return "<" + hex.EncodeToString(b) + "@" + fromAddr[strings.LastIndex(fromAddr, "@")+1:] + ">"
}

func composeMessage(from, to, link, msgID string, date time.Time) []byte {
	return []byte(fmt.Sprintf("From: %s\r\n"+
		"To: %s\r\n"+
		"Subject: Your Jellyfin access link\r\n"+
		"Date: %s\r\n"+
		"Message-ID: %s\r\n"+
		"MIME-Version: 1.0\r\n"+
		"Content-Type: text/plain; charset=utf-8\r\n"+
		"\r\n"+
		"Open this link on a device connected to the network you want to unlock:\r\n"+
		"\r\n"+
		"%s\r\n"+
		"\r\n"+
		"The link works once and expires soon. If you did not ask for it, ignore this email.\r\n",
		from, to, date.Format(time.RFC1123Z), msgID, link))
}

func sendMagicLink(cfg SMTPConfig, to, link string) error {
	from, err := mail.ParseAddress(cfg.From)
	if err != nil {
		return err
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)), 10*time.Second)
	if err != nil {
		return err
	}
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	c, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if err := c.StartTLS(&tls.Config{ServerName: cfg.Host}); err != nil {
		return err
	}
	if err := c.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)); err != nil {
		return err
	}
	if err := c.Mail(from.Address); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(composeMessage(cfg.From, to, link, messageID(from.Address), time.Now())); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}
