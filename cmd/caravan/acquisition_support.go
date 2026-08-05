package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/watzon/caravan/internal/clients/nzbget"
	"github.com/watzon/caravan/internal/clients/qbittorrent"
	"github.com/watzon/caravan/internal/clients/sabnzbd"
	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/download"
	"github.com/watzon/caravan/internal/store"
	"github.com/watzon/caravan/internal/usenet"
)

// newDownloadClientEngine builds the engine for a configured external client.
//
// It is a switch here rather than another registry in internal/clients
// because that package must not import the backends that register into it —
// this is the composition root, and the only place that may know all three.
func newDownloadClientEngine(cfg core.DownloadClientConfig) (core.Engine, error) {
	switch cfg.Type {
	case core.DownloadClientQBittorrent:
		return qbittorrent.NewEngine(cfg, nil)
	case core.DownloadClientSABnzbd:
		return sabnzbd.NewEngine(cfg, nil)
	case core.DownloadClientNZBGet:
		return nzbget.NewEngine(cfg, nil)
	default:
		return nil, fmt.Errorf("unsupported download client type %q", cfg.Type)
	}
}

// clientFingerprint hashes everything an engine is constructed from, so a
// changed row is detected without keeping the credentials it changed in a
// comparable field.
func clientFingerprint(cfg core.DownloadClientConfig) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		cfg.Type, cfg.URL, cfg.Username, cfg.Password, cfg.APIKey, cfg.Category,
	}, "\x00")))
	return hex.EncodeToString(sum[:])
}

// clientMethod is one external client's key in the concurrency ledger.
func clientMethod(id int64) string { return "client:" + strconv.FormatInt(id, 10) }

// clientIDPrefix namespaces one external client's download handles by the
// `download_clients` row that configured it.
//
// The row id is what makes the namespace stable: it survives restarts, edits
// and renames, so a handle stored today still resolves to the same client
// tomorrow. Two NZBGet clients would otherwise both hand out a download "5",
// and Caravan stores handles bare.
//
// The trailing "." both separates the id from the handle and keeps "c1." from
// prefix-matching "c11."; it is not a character any backend's handles contain
// (info hashes are hex, nzo_ids are word characters, NZBIDs are integers), so a
// namespaced handle can never be mistaken for a bare one.
func clientIDPrefix(id int64) string { return "c" + strconv.FormatInt(id, 10) + "." }

// routePick reads a routing value as a `download_clients.id`. The embedded
// engine and an unset or malformed value are both 0, which matches no row.
func routePick(value string) int64 {
	id, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || id <= 0 {
		return 0
	}
	return id
}
func capsFrom(settings map[string]string) download.Caps {
	global, _ := engineSettingInt(settings, store.SettingMaxConcurrentDownloads)
	torrent, _ := engineSettingInt(settings, store.SettingEmbeddedTorrentMaxConcurrent)
	news, _ := engineSettingInt(settings, store.SettingEmbeddedUsenetMaxConcurrent)
	return download.Caps{
		Global: global,
		Method: map[string]int{
			download.EngineName: torrent,
			usenet.EngineName:   news,
		},
	}
}

func engineOptions(settings map[string]string, paused bool, log *slog.Logger) (download.EmbeddedOpts, error) {
	listenPort, err := engineSettingInt(settings, store.SettingEngineListenPort)
	if err != nil {
		return download.EmbeddedOpts{}, err
	}
	maxConnections, err := engineSettingInt(settings, store.SettingEngineMaxConnections)
	if err != nil {
		return download.EmbeddedOpts{}, err
	}
	maxDownKBps, err := engineSettingInt64(settings, store.SettingEngineMaxDownKBps)
	if err != nil {
		return download.EmbeddedOpts{}, err
	}
	maxUpKBps, err := engineSettingInt64(settings, store.SettingEngineMaxUpKBps)
	if err != nil {
		return download.EmbeddedOpts{}, err
	}
	seedRatio, err := engineSettingFloat(settings, store.SettingEngineSeedRatio)
	if err != nil {
		return download.EmbeddedOpts{}, err
	}
	seedDays, err := engineSettingInt(settings, store.SettingEngineSeedDays)
	if err != nil {
		return download.EmbeddedOpts{}, err
	}
	return download.EmbeddedOpts{
		ListenPort:     listenPort,
		MaxConnections: maxConnections,
		MaxDownKBps:    maxDownKBps,
		MaxUpKBps:      maxUpKBps,
		SeedRatio:      seedRatio,
		SeedDays:       seedDays,
		Paused:         paused,
		Logger:         log,
	}, nil
}

func engineSettingInt(settings map[string]string, key string) (int, error) {
	value := strings.TrimSpace(settings[key])
	if value == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid %s", key)
	}
	return n, nil
}

func engineSettingInt64(settings map[string]string, key string) (int64, error) {
	value := strings.TrimSpace(settings[key])
	if value == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid %s", key)
	}
	return n, nil
}

func engineSettingFloat(settings map[string]string, key string) (float64, error) {
	value := strings.TrimSpace(settings[key])
	if value == "" {
		return 0, nil
	}
	n, err := strconv.ParseFloat(value, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid %s", key)
	}
	return n, nil
}
