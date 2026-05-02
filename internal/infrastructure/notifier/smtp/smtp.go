// Package smtp implementa o adapter de e-mail via stdlib net/smtp.
//
// Mantemos zero deps externas — net/smtp.PlainAuth + Sendmail cobrem
// 99% dos servidores corporativos. TLS é controlado pelo Port (465=implicit,
// 587=STARTTLS). Para STARTTLS, usamos smtp.Dial → StartTLS → Auth.
package smtp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"

	domain "github.com/celinet/sentinel-acs/internal/domain/alerting"
)

type Config struct {
	Host        string // smtp.gmail.com
	Port        int    // 587 (STARTTLS), 465 (TLS), 25 (plain)
	Username    string
	Password    string
	FromAddress string // From: ...
	FromName    string // "SentinelACS Alerts"
}

func (c Config) Enabled() bool {
	return c.Host != "" && c.Port > 0 && c.FromAddress != ""
}

type Notifier struct{ cfg Config }

func New(cfg Config) *Notifier { return &Notifier{cfg: cfg} }

func (n *Notifier) Type() domain.ChannelType { return domain.ChannelSMTP }

// Send envia um e-mail simples (text/plain). target é o destinatário.
// Múltiplos destinos podem ser separados por vírgula.
func (n *Notifier) Send(ctx context.Context, target, subject, body string) error {
	if !n.cfg.Enabled() {
		return domain.ErrChannelDisabled
	}
	if target == "" {
		return errors.New("smtp: destinatário vazio")
	}
	to := splitRecipients(target)
	from := n.cfg.FromAddress
	if n.cfg.FromName != "" {
		from = fmt.Sprintf("%s <%s>", n.cfg.FromName, n.cfg.FromAddress)
	}
	msg := buildMessage(from, to, subject, body)

	addr := fmt.Sprintf("%s:%d", n.cfg.Host, n.cfg.Port)

	dialer := &net.Dialer{Timeout: 15 * time.Second}
	conn, err := dialContext(ctx, dialer, "tcp", addr)
	if err != nil {
		return fmt.Errorf("smtp: dial: %w", err)
	}
	defer conn.Close()

	// Port 465 = TLS implícito.
	if n.cfg.Port == 465 {
		conn = tls.Client(conn, &tls.Config{ServerName: n.cfg.Host, MinVersion: tls.VersionTLS12})
	}

	c, err := smtp.NewClient(conn, n.cfg.Host)
	if err != nil {
		return fmt.Errorf("smtp: client: %w", err)
	}
	defer func() { _ = c.Quit() }()

	// STARTTLS quando porta != 465 — só se servidor anuncia.
	if n.cfg.Port != 465 {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(&tls.Config{ServerName: n.cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
				return fmt.Errorf("smtp: starttls: %w", err)
			}
		}
	}

	if n.cfg.Username != "" {
		auth := smtp.PlainAuth("", n.cfg.Username, n.cfg.Password, n.cfg.Host)
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("smtp: auth: %w", err)
		}
	}

	if err := c.Mail(n.cfg.FromAddress); err != nil {
		return fmt.Errorf("smtp: mail from: %w", err)
	}
	for _, addr := range to {
		if err := c.Rcpt(addr); err != nil {
			return fmt.Errorf("smtp: rcpt %q: %w", addr, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp: data: %w", err)
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		return err
	}
	return w.Close()
}

func dialContext(ctx context.Context, d *net.Dialer, network, addr string) (net.Conn, error) {
	return d.DialContext(ctx, network, addr)
}

func splitRecipients(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// buildMessage monta um RFC 5322 mínimo: headers + corpo plain text.
func buildMessage(from string, to []string, subject, body string) string {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + strings.Join(to, ", ") + "\r\n")
	b.WriteString("Subject: " + subject + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)
	return b.String()
}
