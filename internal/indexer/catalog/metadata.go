package catalog

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/watzon/caravan/internal/jsonpolicy"
)

const (
	InventoryStateMetadataOnly       = "metadata-only"
	InventoryStateSourceNotInstalled = "source-not-installed"
	InventoryStateUnsupported        = "unsupported"
	InventoryStateQuarantined        = "quarantined"
	InventoryStateRunnableUnverified = "runnable-unverified"
	InventoryStateVerified           = "verified"
)

//go:embed sites.json
var metadataFiles embed.FS

// SiteMetadata is research inventory only. MetadataURLs and InfoURL are display
// facts and must never be interpreted as Torznab/Newznab endpoints.
type SiteMetadata struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Privacy        string   `json:"privacy"`
	Language       string   `json:"language"`
	InfoURL        string   `json:"info_url"`
	MetadataURLs   []string `json:"metadata_urls"`
	RequiresAPIKey bool     `json:"requires_api_key"`
	Content        []string `json:"content"`
}

// ExecutionStatus is one explicit source variant reconciled against metadata.
// Addable is authority-bearing and may be true only for a verified executable
// definition resolved by the runtime.
type ExecutionStatus struct {
	MetadataID   string    `json:"-"`
	DefinitionID string    `json:"definition_id"`
	State        string    `json:"state"`
	Source       string    `json:"source"`
	Revision     string    `json:"revision,omitempty"`
	Digest       string    `json:"digest,omitempty"`
	BlockedCode  string    `json:"blocked_code,omitempty"`
	Unsupported  []string  `json:"unsupported"`
	Settings     []Setting `json:"settings,omitempty"`
	BaseURLs     []string  `json:"base_urls,omitempty"`
	Addable      bool      `json:"addable"`
}

// InventoryEntry keeps the 542-row metadata plane separate from the existing
// operational Definition picker. DefinitionID is populated only when Addable.
type InventoryEntry struct {
	SiteMetadata
	State        string            `json:"state"`
	Addable      bool              `json:"addable"`
	DefinitionID string            `json:"definition_id,omitempty"`
	Definitions  []ExecutionStatus `json:"definitions"`
}

type siteMetadataDocument struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Privacy        string   `json:"privacy"`
	Language       string   `json:"language"`
	InfoURL        string   `json:"info_url"`
	URLs           []string `json:"urls"`
	RequiresAPIKey bool     `json:"requires_api_key"`
	Content        []string `json:"content"`
}

var (
	metadataOnce sync.Once
	metadataRows []SiteMetadata
	metadataErr  error
)

// Inventory returns all 542 metadata rows with explicit source variants. It
// never adds rows to catalog.All and never invents an API endpoint from a URL.
func Inventory(statuses []ExecutionStatus) ([]InventoryEntry, error) {
	rows, err := loadSiteMetadata()
	if err != nil {
		return nil, err
	}
	statusByMetadata := make(map[string][]ExecutionStatus)
	known := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		known[strings.ToLower(row.ID)] = struct{}{}
	}
	seenStatus := make(map[string]struct{}, len(statuses))
	for _, status := range statuses {
		metadataID := strings.ToLower(strings.TrimSpace(status.MetadataID))
		if _, ok := known[metadataID]; !ok {
			return nil, fmt.Errorf("inventory status references unknown metadata id %q", status.MetadataID)
		}
		status.DefinitionID = strings.TrimSpace(status.DefinitionID)
		status.State = strings.TrimSpace(status.State)
		status.Source = strings.TrimSpace(status.Source)
		status.Revision = strings.TrimSpace(status.Revision)
		status.Digest = strings.TrimSpace(status.Digest)
		status.BlockedCode = strings.TrimSpace(status.BlockedCode)
		if status.DefinitionID == "" || status.Source == "" || !validInventoryState(status.State) {
			return nil, fmt.Errorf("inventory status for %q is incomplete", metadataID)
		}
		if status.Addable && status.State != InventoryStateVerified {
			return nil, fmt.Errorf("only verified inventory status may be addable")
		}
		for _, raw := range status.BaseURLs {
			if err := validateMetadataURL(raw); err != nil {
				return nil, fmt.Errorf("inventory status %q executable URL: %w", status.DefinitionID, err)
			}
		}
		if status.Addable && len(status.BaseURLs) == 0 {
			return nil, fmt.Errorf("addable inventory status %q requires an executable URL", status.DefinitionID)
		}
		key := metadataID + "\x00" + strings.ToLower(status.DefinitionID)
		if _, exists := seenStatus[key]; exists {
			return nil, fmt.Errorf("duplicate inventory definition status %q", status.DefinitionID)
		}
		seenStatus[key] = struct{}{}
		status.Unsupported = append([]string(nil), status.Unsupported...)
		status.BaseURLs = append([]string(nil), status.BaseURLs...)
		if status.Unsupported == nil {
			status.Unsupported = []string{}
		}
		sort.Strings(status.Unsupported)
		statusByMetadata[metadataID] = append(statusByMetadata[metadataID], status)
	}

	inventory := make([]InventoryEntry, 0, len(rows))
	for _, row := range rows {
		variants := statusByMetadata[strings.ToLower(row.ID)]
		sort.Slice(variants, func(i, j int) bool {
			return variants[i].DefinitionID < variants[j].DefinitionID
		})
		entry := InventoryEntry{
			SiteMetadata: row,
			State:        InventoryStateMetadataOnly,
			Definitions:  variants,
		}
		if entry.Definitions == nil {
			entry.Definitions = []ExecutionStatus{}
		}
		for _, status := range variants {
			if entry.State == InventoryStateMetadataOnly {
				entry.State = status.State
			}
			if status.Addable {
				entry.State = status.State
				entry.Addable = true
				entry.DefinitionID = status.DefinitionID
				break
			}
		}
		inventory = append(inventory, entry)
	}
	sort.SliceStable(inventory, func(i, j int) bool {
		left, right := strings.ToLower(inventory[i].Name), strings.ToLower(inventory[j].Name)
		if left != right {
			return left < right
		}
		return strings.ToLower(inventory[i].ID) < strings.ToLower(inventory[j].ID)
	})
	return inventory, nil
}

// HasMetadataID reports whether an operational preset can be reconciled to one
// of the 542 research rows. Extra operational presets (currently Nyaa) remain
// valid but do not change the inventory's exact row count.
func HasMetadataID(id string) bool {
	rows, err := loadSiteMetadata()
	if err != nil {
		return false
	}
	id = strings.ToLower(strings.TrimSpace(id))
	for _, row := range rows {
		if strings.ToLower(row.ID) == id {
			return true
		}
	}
	return false
}

func loadSiteMetadata() ([]SiteMetadata, error) {
	metadataOnce.Do(func() {
		data, err := metadataFiles.ReadFile("sites.json")
		if err != nil {
			metadataErr = fmt.Errorf("read sites metadata: %w", err)
			return
		}
		if err := jsonpolicy.ValidateNoDuplicateKeys(data); err != nil {
			metadataErr = fmt.Errorf("validate sites metadata JSON: %w", err)
			return
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		var documents []siteMetadataDocument
		if err := decoder.Decode(&documents); err != nil {
			metadataErr = fmt.Errorf("decode site metadata: %w", err)
			return
		}
		if err := ensureMetadataJSONEOF(decoder); err != nil {
			metadataErr = fmt.Errorf("decode site metadata: %w", err)
			return
		}
		if len(documents) != 542 {
			metadataErr = fmt.Errorf("site metadata has %d rows, want 542", len(documents))
			return
		}
		seen := make(map[string]struct{}, len(documents))
		rows := make([]SiteMetadata, 0, len(documents))
		for _, document := range documents {
			document.ID = strings.TrimSpace(document.ID)
			document.Name = strings.TrimSpace(document.Name)
			key := strings.ToLower(document.ID)
			if document.ID == "" || document.Name == "" {
				metadataErr = fmt.Errorf("site metadata contains an empty identity")
				return
			}
			if _, exists := seen[key]; exists {
				metadataErr = fmt.Errorf("site metadata repeats id %q", document.ID)
				return
			}
			seen[key] = struct{}{}
			if !validPrivacy(document.Privacy) {
				metadataErr = fmt.Errorf("site metadata %q has invalid privacy", document.ID)
				return
			}
			urls := make([]string, 0, len(document.URLs))
			for _, raw := range document.URLs {
				if err := validateMetadataURL(raw); err != nil {
					metadataErr = fmt.Errorf("site metadata %q URL: %w", document.ID, err)
					return
				}
				urls = append(urls, strings.TrimSpace(raw))
			}
			if document.InfoURL != "" {
				if err := validateMetadataURL(document.InfoURL); err != nil {
					metadataErr = fmt.Errorf("site metadata %q info URL: %w", document.ID, err)
					return
				}
			}
			rows = append(rows, SiteMetadata{
				ID:             document.ID,
				Name:           document.Name,
				Description:    strings.TrimSpace(document.Description),
				Privacy:        document.Privacy,
				Language:       strings.TrimSpace(document.Language),
				InfoURL:        strings.TrimSpace(document.InfoURL),
				MetadataURLs:   urls,
				RequiresAPIKey: document.RequiresAPIKey,
				Content:        normalizeContent(document.Content),
			})
		}
		metadataRows = rows
	})
	if metadataErr != nil {
		return nil, metadataErr
	}
	out := make([]SiteMetadata, len(metadataRows))
	for i, row := range metadataRows {
		row.MetadataURLs = append([]string(nil), row.MetadataURLs...)
		row.Content = append([]string(nil), row.Content...)
		if row.MetadataURLs == nil {
			row.MetadataURLs = []string{}
		}
		if row.Content == nil {
			row.Content = []string{}
		}
		out[i] = row
	}
	return out, nil
}

func validInventoryState(state string) bool {
	switch state {
	case InventoryStateSourceNotInstalled, InventoryStateUnsupported, InventoryStateQuarantined, InventoryStateRunnableUnverified, InventoryStateVerified:
		return true
	default:
		return false
	}
}

func validPrivacy(privacy string) bool {
	switch privacy {
	case PrivacyPublic, PrivacySemiPrivate, PrivacyPrivate:
		return true
	default:
		return false
	}
}

func validateMetadataURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.User != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("%q is not a displayable http(s) URL", raw)
	}
	return nil
}

func ensureMetadataJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("metadata must contain one JSON value")
		}
		return err
	}
	return nil
}
