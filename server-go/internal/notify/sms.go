package notify

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/config"
)

// smsClient is HttpSmsClient: the message is posted to sms.http.url in sms.http.template,
// as a form, JSON or XML by the template's first character.
type smsClient struct {
	url, header, authorization, template, contentType string
	client                                            *http.Client
}

func newSMSClient(cfg *config.Config) (*smsClient, error) {
	url, ok := cfg.Lookup("sms.http.url")
	if !ok {
		if cfg.Has("sms.aws.region") {
			return nil, fmt.Errorf("sms.aws.region: Amazon SNS is not ported")
		}
		return nil, nil
	}
	c := &smsClient{url: url, header: cfg.String("sms.http.authorizationHeader", "Authorization"), client: &http.Client{Timeout: 30 * time.Second}}
	if v, ok := cfg.Lookup("sms.http.authorization"); ok {
		c.authorization = v
	} else {
		user, okU := cfg.Lookup("sms.http.user")
		pass, okP := cfg.Lookup("sms.http.password")
		if okU && okP {
			c.authorization = "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
		}
	}
	c.template = strings.TrimFunc(cfg.String("sms.http.template", ""), func(r rune) bool { return r <= ' ' })
	if c.template == "" {
		return nil, fmt.Errorf("sms.http.template is empty") // Java's charAt(0) fails at startup
	}
	switch c.template[0] {
	case '<':
		c.contentType = "application/xml;charset=UTF-8"
	case '{', '[':
		c.contentType = "application/json;charset=UTF-8"
	default:
		c.contentType = "application/x-www-form-urlencoded;charset=UTF-8"
	}
	return c, nil
}

func (c *smsClient) prepare(v string) string {
	switch {
	case strings.HasPrefix(c.contentType, "application/x-www-form-urlencoded"):
		return javaURLEncode(v)
	case strings.HasPrefix(c.contentType, "application/json"):
		return jsonQuote(v)
	}
	return v
}

func (c *smsClient) send(phone, message string) error {
	payload := strings.ReplaceAll(c.template, "{phone}", c.prepare(phone))
	payload = strings.ReplaceAll(payload, "{message}", c.prepare(message))
	req, err := http.NewRequest(http.MethodPost, c.url, strings.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", c.contentType)
	if c.authorization != "" {
		req.Header.Set(c.header, c.authorization)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s", body)
	}
	return nil
}

// javaURLEncode is URLEncoder.encode(s, UTF_8): letters, digits and .-*_ kept, spaces as +,
// everything else %XX of its UTF-8 bytes.
func javaURLEncode(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '-', c == '*', c == '_':
			b.WriteByte(c)
		case c == ' ':
			b.WriteByte('+')
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// jsonQuote is Jackson's JsonStringEncoder.quoteAsString: quotes, backslashes and control
// characters escaped, nothing else.
func jsonQuote(s string) string {
	var b bytes.Buffer
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}
