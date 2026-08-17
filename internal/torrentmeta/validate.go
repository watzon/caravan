package torrentmeta

import (
	"bytes"
	"fmt"
	"math"
	"strings"

	"github.com/anacrolix/torrent/metainfo"
)

const maxFileTreeDepth = 64

// Parse decodes a bounded caller-supplied torrent document and validates the
// minimum BEP 3/BEP 52 invariants required before it is handed to a download
// engine. Bencode validity alone is insufficient: an empty info dictionary is
// syntactically valid but cannot identify or describe a torrent.
func Parse(payload []byte) (*metainfo.MetaInfo, metainfo.Info, error) {
	mi, err := metainfo.Load(bytes.NewReader(payload))
	if err != nil {
		return nil, metainfo.Info{}, err
	}
	if len(mi.InfoBytes) == 0 {
		return nil, metainfo.Info{}, fmt.Errorf("missing info dictionary")
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return nil, metainfo.Info{}, err
	}
	if err := validateInfo(info); err != nil {
		return nil, metainfo.Info{}, err
	}
	return mi, info, nil
}

func validateInfo(info metainfo.Info) error {
	if !validPathPart(info.BestName()) {
		return fmt.Errorf("info name is empty or unsafe")
	}
	if info.PieceLength <= 0 {
		return fmt.Errorf("piece length must be positive")
	}
	if info.MetaVersion != 0 && info.MetaVersion != 2 {
		return fmt.Errorf("unsupported meta version")
	}

	hasV2 := info.MetaVersion == 2
	hasV1 := info.MetaVersion == 0 || info.Files != nil || info.Length != 0 || len(info.Pieces) != 0
	if !hasV1 && !hasV2 {
		return fmt.Errorf("info dictionary has no torrent version")
	}
	if hasV1 {
		if err := validateV1(info); err != nil {
			return err
		}
	}
	if hasV2 {
		if err := validateV2(info); err != nil {
			return err
		}
	}
	return nil
}

func validateV1(info metainfo.Info) error {
	if len(info.Pieces)%metainfo.HashSize != 0 {
		return fmt.Errorf("piece hashes have invalid length")
	}
	if info.Files != nil && info.Length != 0 {
		return fmt.Errorf("single-file length and files are mutually exclusive")
	}

	var total int64
	if info.Files == nil {
		if info.Length <= 0 {
			return fmt.Errorf("torrent payload has no files")
		}
		total = info.Length
	} else {
		if len(info.Files) == 0 {
			return fmt.Errorf("torrent payload has no files")
		}
		for _, file := range info.Files {
			if file.Length < 0 || len(file.BestPath()) == 0 {
				return fmt.Errorf("torrent file is invalid")
			}
			for _, part := range file.BestPath() {
				if !validPathPart(part) {
					return fmt.Errorf("torrent file path is unsafe")
				}
			}
			if file.Length > math.MaxInt64-total {
				return fmt.Errorf("torrent length overflows")
			}
			total += file.Length
		}
		if total <= 0 {
			return fmt.Errorf("torrent payload has no data")
		}
	}
	expectedPieces := (total + info.PieceLength - 1) / info.PieceLength
	if int64(len(info.Pieces)/metainfo.HashSize) != expectedPieces {
		return fmt.Errorf("piece hash count does not match torrent length")
	}
	return nil
}

func validateV2(info metainfo.Info) error {
	if info.PieceLength < 16<<10 || info.PieceLength&(info.PieceLength-1) != 0 {
		return fmt.Errorf("v2 piece length must be a power of two of at least 16 KiB")
	}
	if info.FileTree.NumEntries() == 0 {
		return fmt.Errorf("v2 torrent has no file tree")
	}
	var total int64
	if err := validateFileTree(info.FileTree, 0, &total); err != nil {
		return err
	}
	if total <= 0 {
		return fmt.Errorf("v2 torrent payload has no data")
	}
	return nil
}

func validateFileTree(tree metainfo.FileTree, depth int, total *int64) error {
	if depth > maxFileTreeDepth {
		return fmt.Errorf("v2 file tree exceeds depth limit")
	}
	if tree.NumEntries() == 0 {
		if tree.File.Length < 0 {
			return fmt.Errorf("v2 torrent file has negative length")
		}
		if tree.File.Length > 0 && len(tree.File.PiecesRoot) != 32 {
			return fmt.Errorf("v2 torrent file has invalid pieces root")
		}
		if tree.File.Length > math.MaxInt64-*total {
			return fmt.Errorf("torrent length overflows")
		}
		*total += tree.File.Length
		return nil
	}
	for name, child := range tree.Dir {
		if !validPathPart(name) {
			return fmt.Errorf("v2 torrent file path is unsafe")
		}
		if err := validateFileTree(child, depth+1, total); err != nil {
			return err
		}
	}
	return nil
}

func validPathPart(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && value != "." && value != ".." && !strings.ContainsAny(value, "/\\\x00")
}
