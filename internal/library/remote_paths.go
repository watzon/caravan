package library

import (
	"context"
	"path"
	"path/filepath"
	"strings"

	"github.com/watzon/caravan/internal/core"
)

// resolveDownloadPath applies the most specific configured remote-path mapping
// to an external client's save path. An unmatched path is returned unchanged.
func (m *Manager) resolveDownloadPath(ctx context.Context, savePath string) (string, error) {
	mappings, err := m.store.ListRemotePathMappings(ctx)
	if err != nil {
		return "", err
	}
	mapped, mappingID, matched := mapRemotePathWithID(savePath, mappings)
	if matched && mappingID > 0 {
		if err := m.store.RecordRemotePathMappingMatch(ctx, mappingID); err != nil {
			return "", err
		}
	}
	return mapped, nil
}

// mapRemotePath applies the longest matching remote component prefix. Client
// paths are normalized with slash separators so a Windows download client can
// map into a Linux or macOS Caravan host. Drive-letter paths compare without
// case because Windows paths are case-insensitive.
func mapRemotePath(savePath string, mappings []core.RemotePathMapping) (string, bool) {
	mapped, _, matched := mapRemotePathWithID(savePath, mappings)
	return mapped, matched
}

func mapRemotePathWithID(savePath string, mappings []core.RemotePathMapping) (string, int64, bool) {
	normalizedSave := normalizeClientPath(savePath)
	bestLength := -1
	var bestID int64
	bestLocal := ""
	bestSuffix := ""

	for _, mapping := range mappings {
		remote := normalizeClientPath(mapping.RemotePath)
		if remote == "." || remote == "" {
			continue
		}
		caseInsensitive := windowsClientPath(remote)
		candidate := normalizedSave
		prefix := remote
		if caseInsensitive {
			candidate = strings.ToLower(candidate)
			prefix = strings.ToLower(prefix)
		}
		if candidate != prefix && !strings.HasPrefix(candidate, strings.TrimSuffix(prefix, "/")+"/") {
			continue
		}
		if len(remote) <= bestLength {
			continue
		}
		bestLength = len(remote)
		bestLocal = mapping.LocalPath
		bestID = mapping.ID
		bestSuffix = strings.TrimPrefix(normalizedSave[len(remote):], "/")
	}

	if bestLength < 0 {
		return savePath, 0, false
	}
	if bestSuffix == "" {
		return filepath.Clean(bestLocal), bestID, true
	}
	return filepath.Join(filepath.Clean(bestLocal), filepath.FromSlash(bestSuffix)), bestID, true
}

func normalizeClientPath(value string) string {
	return path.Clean(strings.ReplaceAll(strings.TrimSpace(value), `\`, "/"))
}

func windowsClientPath(value string) bool {
	return len(value) >= 3 && value[1] == ':' && value[2] == '/'
}
