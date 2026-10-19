package notify

import (
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/config"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
)

// smtpSettings are SmtpMailManager's properties, from the config or a user's attributes.
type smtpSettings struct {
	protocol, host                     string
	port                               int
	starttls, starttlsRequired, ssl    *bool
	sslTrust, username, password, from *string
	fromName                           *string
}

// mailer is MailManager: SmtpMailManager, or LogMailManager with mail.debug.
type mailer struct {
	debug            bool
	ignoreUserConfig bool // mail.smtp.ignoreUserConfig
	systemOnly       bool // mail.smtp.systemOnly
	system           *smtpSettings
	log              *slog.Logger
	timeout          time.Duration
}

func newMailer(cfg *config.Config, log *slog.Logger) (*mailer, error) {
	m := &mailer{
		debug:            cfg.Bool("mail.debug", false),
		ignoreUserConfig: cfg.Bool("mail.smtp.ignoreUserConfig", false),
		systemOnly:       cfg.Bool("mail.smtp.systemOnly", false),
		log:              log,
		timeout:          60 * time.Second,
	}
	str := func(k string) *string {
		if v, ok := cfg.Lookup(k); ok {
			return &v
		}
		return nil
	}
	boolean := func(k string) *bool {
		if !cfg.Has(k) {
			return nil
		}
		v := cfg.Bool(k, false)
		return &v
	}
	port, err := cfg.Int("mail.smtp.port", 25)
	if err != nil {
		return nil, err
	}
	m.system = settings(str, boolean, func(string) (int, error) { return port, nil })
	return m, nil
}

// settings is SmtpMailManager.getProperties: nil without mail.smtp.host.
func settings(str func(string) *string, boolean func(string) *bool, integer func(string) (int, error)) *smtpSettings {
	host := str("mail.smtp.host")
	if host == nil {
		return nil
	}
	s := &smtpSettings{host: *host, protocol: "smtp"}
	if p := str("mail.transport.protocol"); p != nil {
		s.protocol = *p
	}
	s.port, _ = integer("mail.smtp.port")
	s.starttls = boolean("mail.smtp.starttls.enable")
	s.starttlsRequired = boolean("mail.smtp.starttls.required")
	s.ssl = boolean("mail.smtp.ssl.enable")
	s.sslTrust = str("mail.smtp.ssl.trust")
	s.username = str("mail.smtp.username")
	s.password = str("mail.smtp.password")
	s.from = str("mail.smtp.from")
	s.fromName = str("mail.smtp.fromName")
	return s
}

// userSettings is PropertiesProvider over a user's attributes.
func userSettings(u *model.User) *smtpSettings {
	a := &u.Attributes
	str := func(k string) *string {
		s := a.JavaString(k)
		if s == nil && k == "mail.transport.protocol" {
			def := "smtp"
			return &def
		}
		return s
	}
	boolean := func(k string) *bool {
		v, ok := a.Get(k)
		if !ok || v == nil {
			return nil
		}
		b := false
		switch x := v.(type) {
		case bool:
			b = x
		case string:
			b = strings.EqualFold(x, "true")
		}
		return &b
	}
	integer := func(k string) (int, error) {
		v, ok := a.Get(k)
		if !ok || v == nil {
			return 25, nil
		}
		if s, ok := v.(string); ok {
			return strconv.Atoi(s)
		}
		return a.JavaInt(k)
	}
	return settings(str, boolean, integer)
}

func isTrue(b *bool) bool { return b != nil && *b }

// send is MailManager.sendMessage: the user's own SMTP settings unless ignored, else the
// server's (for system mail, or unless mail.smtp.systemOnly). counted is called before
// connecting (StatisticsManager.registerMail).
func (m *mailer) send(u *model.User, system bool, subject *string, body string, counted func()) error {
	if m.debug {
		s := "null"
		if subject != nil {
			s = *subject
		}
		m.log.Info(fmt.Sprintf("Email sent\nTo: %s\nSubject: %s\nAttachment: null\nBody:\n%s", u.Email, s, body))
		return nil
	}
	var s *smtpSettings
	if !m.ignoreUserConfig {
		s = userSettings(u)
	}
	if s == nil && (system || !m.systemOnly) {
		s = m.system
	}
	if s == nil {
		return errors.New("No SMTP configuration found")
	}
	msg, from, err := buildMessage(s, u.Email, subject, body)
	if err != nil {
		return err
	}
	if counted != nil {
		counted()
	}
	return m.deliver(s, from, u.Email, msg)
}

// buildMessage is the MimeMessage: From (with its name), To, Subject (when set), Date and
// an HTML body in UTF-8.
func buildMessage(s *smtpSettings, to string, subject *string, body string) ([]byte, string, error) {
	var from string
	var fromHeader string
	if s.from != nil {
		addr, err := mail.ParseAddress(*s.from)
		if err != nil {
			return nil, "", fmt.Errorf("mail.smtp.from: %w", err)
		}
		from = addr.Address
		if s.fromName != nil {
			addr.Name = *s.fromName
		}
		fromHeader = addr.String()
	} else {
		host, _ := os.Hostname()
		user := os.Getenv("USER")
		from = user + "@" + host
	}
	if _, err := mail.ParseAddress(to); err != nil {
		return nil, "", fmt.Errorf("recipient %q: %w", to, err)
	}
	var b bytes.Buffer
	if fromHeader != "" {
		fmt.Fprintf(&b, "From: %s\r\n", fromHeader)
	}
	fmt.Fprintf(&b, "To: %s\r\n", to)
	if subject != nil {
		fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("UTF-8", *subject))
	}
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\nContent-Type: text/html; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n")
	w := quotedprintable.NewWriter(&b)
	if _, err := w.Write([]byte(body)); err != nil {
		return nil, "", err
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return b.Bytes(), from, nil
}

// deliver talks SMTP: TLS from the start for smtps or mail.smtp.ssl.enable, STARTTLS when
// enabled (failing when required and not offered), and AUTH when a user name and
// password are set.
func (m *mailer) deliver(s *smtpSettings, from, to string, msg []byte) error {
	addr := net.JoinHostPort(s.host, strconv.Itoa(s.port))
	tlsConfig := &tls.Config{ServerName: s.host, InsecureSkipVerify: s.sslTrust != nil && *s.sslTrust == "*"}
	var conn net.Conn
	var err error
	dialer := &net.Dialer{Timeout: m.timeout}
	if s.protocol == "smtps" || isTrue(s.ssl) {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsConfig)
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return err
	}
	_ = conn.SetDeadline(time.Now().Add(m.timeout))
	c, err := smtp.NewClient(conn, s.host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if isTrue(s.starttls) {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(tlsConfig); err != nil {
				return err
			}
		} else if isTrue(s.starttlsRequired) {
			return errors.New("STARTTLS is required but host does not support STARTTLS")
		}
	}
	if s.username != nil && s.password != nil {
		if ok, mechs := c.Extension("AUTH"); ok {
			if err := c.Auth(&plainOrLogin{user: *s.username, pass: *s.password, login: strings.Contains(mechs, "LOGIN")}); err != nil {
				return err
			}
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// plainOrLogin authenticates as Jakarta Mail does: LOGIN when offered, else PLAIN (sent
// whether or not the connection is encrypted, as Java does).
type plainOrLogin struct {
	user, pass string
	login      bool
	step       int
}

func (a *plainOrLogin) Start(*smtp.ServerInfo) (string, []byte, error) {
	if a.login {
		return "LOGIN", nil, nil
	}
	return "PLAIN", []byte("\x00" + a.user + "\x00" + a.pass), nil
}

func (a *plainOrLogin) Next(_ []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	a.step++
	switch a.step {
	case 1:
		return []byte(a.user), nil
	case 2:
		return []byte(a.pass), nil
	}
	return nil, errors.New("unexpected AUTH challenge")
}
