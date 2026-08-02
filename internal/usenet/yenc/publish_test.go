package yenc_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/usenet/nntp"
	"github.com/watzon/caravan/internal/usenet/nntptest"
	"github.com/watzon/caravan/internal/usenet/yenc"
)

func TestMessageIDIsStableAndLegal(t *testing.T) {
	id := yenc.MessageID("Some Release/2024 [1080p].mkv", 7)
	if strings.ContainsAny(id, "<> \t\r\n/") {
		t.Errorf("message-id %q contains a character an NZB or the wire cannot carry", id)
	}
	if !strings.HasSuffix(id, ".7@caravan.invalid") {
		t.Errorf("message-id = %q, want it to end with the part number and host", id)
	}
	if again := yenc.MessageID("Some Release/2024 [1080p].mkv", 7); again != id {
		t.Errorf("MessageID is not stable: %q then %q", id, again)
	}
}

// TestPublishThroughFakeServer is the end-to-end shape the pipeline track
// builds on: articles staged on a fake news server, fetched by message-id
// through the real NNTP client, decoded, and written at each part's offset.
// It is also the proof that yEnc output survives the wire — dot-stuffing,
// CRLF normalisation and all.
func TestPublishThroughFakeServer(t *testing.T) {
	server, err := nntptest.New(nntptest.Options{})
	if err != nil {
		t.Fatalf("nntptest.New: %v", err)
	}
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Errorf("close fake server: %v", err)
		}
	})

	// A payload full of the bytes that break naive framing: values that
	// encode to a leading dot, a bare CR, a bare LF and an '='.
	data := append(allBytes(4), randomBytes(21, 3000)...)
	ids, err := yenc.Publish(server.Add, "caravan.publish.bin", data, 700)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if got, want := len(ids), (len(data)+699)/700; got != want {
		t.Fatalf("published %d articles, want %d", got, want)
	}

	pool, err := nntp.NewMultiPool([]nntp.ServerConfig{{
		ID: 1, Name: "fake", Host: server.Host(), Port: server.Port(),
		MaxConnections: 2, Enabled: true,
	}}, nntp.Options{IdleTimeout: time.Minute})
	if err != nil {
		t.Fatalf("NewMultiPool: %v", err)
	}
	t.Cleanup(func() {
		if err := pool.Close(); err != nil {
			t.Errorf("close pool: %v", err)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	out := make([]byte, len(data))
	var fileCRC uint32
	var haveFileCRC bool
	for i, id := range ids {
		body, err := pool.FetchBody(ctx, id)
		if err != nil {
			t.Fatalf("FetchBody(%s): %v", id, err)
		}
		part, err := yenc.DecodeBytes(body)
		if err != nil {
			t.Fatalf("decode article %d: %v", i+1, err)
		}
		if !part.Verified {
			t.Errorf("article %d came back unverified", i+1)
		}
		copy(out[part.Begin:], part.Body)
		if part.HasFileCRC {
			fileCRC, haveFileCRC = part.FileCRC32, true
		}
	}

	if !bytes.Equal(out, data) {
		t.Fatal("the file that came off the wire is not the file that went on it")
	}
	if !haveFileCRC {
		t.Fatal("no article carried the whole-file crc32")
	}
	if err := yenc.CheckFileCRC("caravan.publish.bin", fileCRC, crcOf(out)); err != nil {
		t.Fatalf("CheckFileCRC: %v", err)
	}
}

// TestPublishSurvivesADamagedArticle is the failure path: an article the
// server hands back with a flipped byte must fail loudly, so the pipeline
// hands the segment to par2 instead of writing it.
func TestPublishSurvivesADamagedArticle(t *testing.T) {
	server, err := nntptest.New(nntptest.Options{})
	if err != nil {
		t.Fatalf("nntptest.New: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	data := randomBytes(22, 2000)
	ids, err := yenc.Publish(server.Add, "caravan.damaged.bin", data, 700)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// Re-register the second article with one payload character changed.
	articles, err := yenc.EncodeFile("caravan.damaged.bin", data, 700)
	if err != nil {
		t.Fatalf("EncodeFile: %v", err)
	}
	server.Add(ids[1], flipPayloadByte(t, articles[1]))

	pool, err := nntp.NewMultiPool([]nntp.ServerConfig{{
		ID: 1, Name: "fake", Host: server.Host(), Port: server.Port(),
		MaxConnections: 1, Enabled: true,
	}}, nntp.Options{IdleTimeout: time.Minute})
	if err != nil {
		t.Fatalf("NewMultiPool: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })

	body, err := pool.FetchBody(context.Background(), ids[1])
	if err != nil {
		t.Fatalf("FetchBody: %v", err)
	}
	part, err := yenc.DecodeBytes(body)
	if err == nil {
		t.Fatalf("decoded a damaged article: %d bytes at offset %d", len(part.Body), part.Begin)
	}
	if !errors.Is(err, yenc.ErrCorrupt) {
		t.Fatalf("error = %v, want ErrCorrupt", err)
	}
	var crcErr *yenc.CRCError
	if !errors.As(err, &crcErr) {
		t.Fatalf("error = %T (%v), want *yenc.CRCError", err, err)
	}
	if crcErr.Part != 2 {
		t.Errorf("part = %d, want 2", crcErr.Part)
	}
}
