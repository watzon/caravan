package torrentmeta

import (
	"bytes"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

func TestParseAcceptsValidV1Torrent(t *testing.T) {
	payload := encodeTorrent(t, metainfo.Info{
		Name: "payload.bin", Length: 1, PieceLength: 1, Pieces: make([]byte, metainfo.HashSize),
	})
	mi, info, err := Parse(payload)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if info.Name != "payload.bin" || mi.HashInfoBytes() == (metainfo.Hash{}) {
		t.Fatalf("parsed metainfo = %+v, info = %+v", mi, info)
	}
}

func TestParseRejectsSemanticallyInvalidV1Torrent(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
	}{
		{name: "empty info", payload: []byte("d4:infodee")},
		{name: "missing name", payload: encodeTorrent(t, metainfo.Info{Length: 1, PieceLength: 1, Pieces: make([]byte, metainfo.HashSize)})},
		{name: "wrong piece count", payload: encodeTorrent(t, metainfo.Info{Name: "bad.bin", Length: 2, PieceLength: 1, Pieces: make([]byte, metainfo.HashSize)})},
		{name: "unsafe file path", payload: encodeTorrent(t, metainfo.Info{Name: "files", PieceLength: 1, Pieces: make([]byte, metainfo.HashSize), Files: []metainfo.FileInfo{{Length: 1, Path: []string{"..", "bad.bin"}}}})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := Parse(tt.payload); err == nil {
				t.Fatal("Parse accepted semantically invalid torrent")
			}
		})
	}
}

func TestParseValidatesV2FileTree(t *testing.T) {
	if _, _, err := Parse(encodeV2Torrent(t, string(make([]byte, 32)))); err != nil {
		t.Fatalf("Parse valid v2 torrent: %v", err)
	}
	if _, _, err := Parse(encodeV2Torrent(t, "short")); err == nil {
		t.Fatal("Parse accepted v2 torrent with an invalid pieces root")
	}
}

func encodeV2Torrent(t *testing.T, piecesRoot string) []byte {
	t.Helper()
	infoBytes, err := bencode.Marshal(map[string]any{
		"file tree": map[string]any{
			"payload.bin": map[string]any{
				"": map[string]any{"length": int64(1), "pieces root": piecesRoot},
			},
		},
		"meta version": int64(2),
		"name":         "v2",
		"piece length": int64(16 << 10),
	})
	if err != nil {
		t.Fatalf("marshal v2 info: %v", err)
	}
	var payload bytes.Buffer
	if err := (&metainfo.MetaInfo{InfoBytes: infoBytes}).Write(&payload); err != nil {
		t.Fatalf("write v2 metainfo: %v", err)
	}
	return payload.Bytes()
}

func encodeTorrent(t *testing.T, info metainfo.Info) []byte {
	t.Helper()
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("marshal info: %v", err)
	}
	mi := metainfo.MetaInfo{InfoBytes: infoBytes}
	var payload bytes.Buffer
	if err := mi.Write(&payload); err != nil {
		t.Fatalf("write metainfo: %v", err)
	}
	return payload.Bytes()
}
