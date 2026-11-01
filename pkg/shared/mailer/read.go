package mailer

import (
	"io"
	"strings"

	// Registers the non-UTF-8 charsets: French mail still arrives as
	// ISO-8859-1 often enough to matter, and undecoded it reads as mojibake.
	_ "github.com/emersion/go-message/charset"
	gomessage "github.com/emersion/go-message/mail"
)

// ReadBody pulls the human-readable parts out of a MIME message: the first
// text/plain and the first text/html, wherever they nest. A body that cannot be
// parsed yields empty strings rather than an error: the headers are still
// useful, and one malformed message must not stop a mailbox being read.
func ReadBody(r io.Reader) (text, html string) {
	mr, err := gomessage.CreateReader(r)
	if err != nil {
		return "", ""
	}
	for {
		part, err := mr.NextPart()
		if err != nil {
			break
		}
		inline, ok := part.Header.(*gomessage.InlineHeader)
		if !ok {
			continue // an attachment, not something to read
		}
		body, err := io.ReadAll(part.Body)
		if err != nil {
			continue
		}
		contentType, _, _ := inline.ContentType()
		switch {
		case strings.EqualFold(contentType, "text/html") && html == "":
			html = string(body)
		case strings.EqualFold(contentType, "text/plain") && text == "":
			text = string(body)
		}
	}
	// A message sent only as HTML still has to answer with some text, or the
	// common case of "put the mail in a prompt" gets an empty string.
	if text == "" && html != "" {
		text = StripTags(html)
	}
	return text, html
}
