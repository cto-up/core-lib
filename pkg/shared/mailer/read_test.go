package mailer_test

import (
	"strings"
	"testing"

	"github.com/cto-up/core-lib/pkg/shared/mailer"
)

func TestReadBodyPrefersPlainTextAndFallsBackToHTML(t *testing.T) {
	const htmlOnly = "MIME-Version: 1.0\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n\r\n" +
		"<p>Invoice <b>4821</b> is due</p>"
	text, html := mailer.ReadBody(strings.NewReader(htmlOnly))
	if html == "" {
		t.Error("the HTML body was dropped")
	}
	if !strings.Contains(text, "Invoice") {
		t.Errorf("an HTML-only message left text empty: %q", text)
	}
}

func TestReadBodySurvivesNonsense(t *testing.T) {
	text, html := mailer.ReadBody(strings.NewReader("\x00\x01 not a message"))
	if text != "" || html != "" {
		t.Errorf("got %q / %q", text, html)
	}
}

// A message with an attachment nests the text inside multipart/mixed →
// multipart/alternative; the attachment itself is never read as text.
func TestReadBodyFindsTheTextUnderAnAttachment(t *testing.T) {
	raw := "MIME-Version: 1.0\r\nSubject: Devis\r\n" +
		"Content-Type: multipart/mixed; boundary=outer\r\n\r\n" +
		"--outer\r\n" +
		"Content-Type: multipart/alternative; boundary=inner\r\n\r\n" +
		"--inner\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nLe devis est en pièce jointe.\r\n" +
		"--inner\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p>Le devis est en pièce jointe.</p>\r\n" +
		"--inner--\r\n" +
		"--outer\r\nContent-Type: text/plain\r\nContent-Disposition: attachment; filename=notes.txt\r\n\r\nnot the body\r\n" +
		"--outer--\r\n"

	text, html := mailer.ReadBody(strings.NewReader(raw))
	if strings.TrimSpace(text) != "Le devis est en pièce jointe." {
		t.Errorf("text = %q", text)
	}
	if !strings.Contains(html, "<p>") {
		t.Errorf("html = %q", html)
	}
}

func TestReadBodyDecodesALatin1Message(t *testing.T) {
	raw := "MIME-Version: 1.0\r\nContent-Type: text/plain; charset=iso-8859-1\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n\r\nLe devis =E0 jour\r\n"
	text, _ := mailer.ReadBody(strings.NewReader(raw))
	if strings.TrimSpace(text) != "Le devis à jour" {
		t.Errorf("text = %q", text)
	}
}
