package yenc

import (
	"fmt"
	"strings"
)

// Publish encodes data as yEnc articles of at most partSize bytes, hands each
// one to add, and returns their message-ids in part order.
//
// It exists so that a test can stage a real file on the fake news server in
// one line:
//
//	ids, err := yenc.Publish(server.Add, "movie.mkv", data, 200_000)
//
// The add callback is the shape of internal/usenet/nntptest.Server.Add, taken
// as a function rather than an interface so that neither the codec nor the
// fake server has to import the other — the fake stays a package about NNTP
// and knows nothing about yEnc (PLAN phase 7 task 7).
//
// Message-ids are derived from name and part number, so the same call in two
// tests produces the same ids and an NZB fixture can name them ahead of time.
func Publish(add func(messageID string, body []byte), name string, data []byte, partSize int) ([]string, error) {
	articles, err := EncodeFile(name, data, partSize)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(articles))
	for i, article := range articles {
		id := MessageID(name, i+1)
		add(id, article)
		ids = append(ids, id)
	}
	return ids, nil
}

// MessageID is the message-id Publish gives part n (1-based) of name. It is
// returned without angle brackets, the form NZB segments and the NNTP client
// both use.
func MessageID(name string, part int) string {
	return fmt.Sprintf("%s.%d@caravan.invalid", slug(name), part)
}

// slug reduces a filename to characters that are legal in a message-id.
func slug(name string) string {
	s := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '.' || r == '-' || r == '_':
			return r
		}
		return '-'
	}, name)
	if s == "" {
		return "article"
	}
	return s
}
