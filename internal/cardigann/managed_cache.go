package cardigann

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const (
	DefaultManagedDefinitionURL = "https://indexers.prowlarr.com/master/11"
	managedStorageRoot          = "managed-definitions"
	managedSnapshotsDir         = managedStorageRoot + "/snapshots"
	managedCurrentPointer       = managedStorageRoot + "/current"
	managedPreviousPointer      = managedStorageRoot + "/previous"
	managedUserAgent            = "Caravan/managed-indexer-definitions"
)

func FetchManagedSnapshot(ctx context.Context, client *http.Client, sourceURL string) (*ManagedSnapshot, error) {
	if client == nil {
		return nil, fmt.Errorf("managed definition fetch requires an HTTP client")
	}
	base, err := url.Parse(strings.TrimRight(strings.TrimSpace(sourceURL), "/"))
	if err != nil || base.Scheme == "" || base.Host == "" || base.User != nil {
		return nil, fmt.Errorf("managed definition source URL is invalid")
	}
	listing, err := fetchManagedObject(ctx, client, base, maxManagedListingBytes)
	if err != nil {
		return nil, fmt.Errorf("fetch managed definition listing: %w", err)
	}
	archiveURL := *base
	archiveURL.Path = strings.TrimRight(archiveURL.Path, "/") + "/package.zip"
	archiveURL.RawPath = ""
	archiveURL.RawQuery = ""
	archiveURL.Fragment = ""
	archive, err := fetchManagedObject(ctx, client, &archiveURL, maxManagedArchiveBytes)
	if err != nil {
		return nil, fmt.Errorf("fetch managed definition archive: %w", err)
	}
	return InspectManagedSnapshot(listing, archive)
}

func fetchManagedObject(ctx context.Context, client *http.Client, target *url.URL, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", managedUserAgent)

	bounded := *client
	previousCheck := client.CheckRedirect
	bounded.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many redirects")
		}
		if !sameManagedOrigin(target, next.URL) {
			return fmt.Errorf("managed definition redirect left the approved source origin")
		}
		if previousCheck != nil {
			return previousCheck(next, via)
		}
		return nil
	}
	response, err := bounded.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected HTTP status %d", response.StatusCode)
	}
	if response.ContentLength > limit {
		return nil, fmt.Errorf("response exceeds %d-byte limit", limit)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("response exceeds %d-byte limit", limit)
	}
	return data, nil
}

func sameManagedOrigin(left, right *url.URL) bool {
	return strings.EqualFold(left.Scheme, right.Scheme) && strings.EqualFold(left.Host, right.Host)
}

func InstallManagedSnapshot(dataDir string, snapshot *ManagedSnapshot) error {
	if snapshot == nil || snapshot.Revision == "" || len(snapshot.listing) == 0 || len(snapshot.archive) == 0 {
		return fmt.Errorf("managed definition install requires a verified snapshot")
	}
	verified, err := InspectManagedSnapshot(snapshot.listing, snapshot.archive)
	if err != nil || verified.Revision != snapshot.Revision {
		return fmt.Errorf("reverify managed definition snapshot before install: %w", err)
	}
	root, err := openPackStorageRoot(dataDir)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, dir := range []string{managedStorageRoot, managedSnapshotsDir} {
		if err := ensurePackStorageDirectory(root, dir); err != nil {
			return err
		}
	}

	listingName := managedSnapshotName(snapshot.Revision, ".json")
	archiveName := managedSnapshotName(snapshot.Revision, ".zip")
	if err := publishManagedObject(root, listingName, snapshot.listing); err != nil {
		return err
	}
	if err := publishManagedObject(root, archiveName, snapshot.archive); err != nil {
		return err
	}
	current, _ := readManagedPointer(root, managedCurrentPointer)
	if current != "" && current != snapshot.Revision {
		if err := writeManagedPointer(root, managedPreviousPointer, current); err != nil {
			return err
		}
	}
	return writeManagedPointer(root, managedCurrentPointer, snapshot.Revision)
}

func LoadManagedSnapshot(dataDir string) (*ManagedSnapshot, error) {
	root, err := openPackStorageRoot(dataDir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	var failures []error
	seen := map[string]struct{}{}
	for _, pointer := range []string{managedCurrentPointer, managedPreviousPointer} {
		revision, pointerErr := readManagedPointer(root, pointer)
		if pointerErr != nil {
			if !errors.Is(pointerErr, fs.ErrNotExist) {
				failures = append(failures, pointerErr)
			}
			continue
		}
		if revision == "" {
			continue
		}
		if _, duplicate := seen[revision]; duplicate {
			continue
		}
		seen[revision] = struct{}{}
		snapshot, loadErr := loadManagedRevision(root, revision)
		if loadErr == nil {
			return snapshot, nil
		}
		failures = append(failures, loadErr)
	}
	if len(failures) == 0 {
		return nil, fs.ErrNotExist
	}
	return nil, fmt.Errorf("load managed definition snapshot: %w", errors.Join(failures...))
}

func loadManagedRevision(root *os.Root, revision string) (*ManagedSnapshot, error) {
	if !validManagedRevision(revision) {
		return nil, fmt.Errorf("managed definition pointer is invalid")
	}
	listing, err := readManagedObject(root, managedSnapshotName(revision, ".json"), maxManagedListingBytes)
	if err != nil {
		return nil, err
	}
	archive, err := readManagedObject(root, managedSnapshotName(revision, ".zip"), maxManagedArchiveBytes)
	if err != nil {
		return nil, err
	}
	snapshot, err := InspectManagedSnapshot(listing, archive)
	if err != nil || snapshot.Revision != revision {
		return nil, fmt.Errorf("managed definition cached revision failed verification: %w", err)
	}
	return snapshot, nil
}

func managedSnapshotName(revision, extension string) string {
	return managedSnapshotsDir + "/" + revision + extension
}

func validManagedRevision(revision string) bool {
	if len(revision) != 64 {
		return false
	}
	for _, char := range revision {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return false
		}
	}
	return true
}

func publishManagedObject(root *os.Root, name string, data []byte) error {
	if existing, err := readManagedObject(root, name, int64(len(data))); err == nil {
		if bytes.Equal(existing, data) {
			return nil
		}
		return fmt.Errorf("managed definition content address already contains different bytes")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	tempSuffix, err := newPackStagingName()
	if err != nil {
		return err
	}
	tempName := managedSnapshotsDir + "/." + filepath.Base(tempSuffix)
	file, err := root.OpenFile(tempName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create managed definition staging object: %w", err)
	}
	written, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil || written != len(data) {
		_ = root.Remove(tempName)
		return fmt.Errorf("write managed definition staging object: %w", errors.Join(writeErr, syncErr, closeErr))
	}
	if err := root.Rename(tempName, name); err != nil {
		_ = root.Remove(tempName)
		if existing, readErr := readManagedObject(root, name, int64(len(data))); readErr == nil && bytes.Equal(existing, data) {
			return nil
		}
		return fmt.Errorf("publish managed definition object: %w", err)
	}
	return nil
}

func readManagedObject(root *os.Root, name string, limit int64) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > limit {
		return nil, fmt.Errorf("managed definition object has unsafe identity")
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	opened, statErr := file.Stat()
	data, readErr := io.ReadAll(io.LimitReader(file, limit+1))
	closeErr := file.Close()
	current, currentErr := root.Lstat(name)
	if statErr != nil || readErr != nil || closeErr != nil || currentErr != nil || !os.SameFile(info, opened) || !os.SameFile(opened, current) || int64(len(data)) > limit {
		return nil, fmt.Errorf("read managed definition object safely: %w", errors.Join(statErr, readErr, closeErr, currentErr))
	}
	return data, nil
}

func readManagedPointer(root *os.Root, name string) (string, error) {
	data, err := readManagedObject(root, name, 65)
	if err != nil {
		return "", err
	}
	revision := strings.TrimSpace(string(data))
	if !validManagedRevision(revision) {
		return "", fmt.Errorf("managed definition pointer %q is invalid", name)
	}
	return revision, nil
}

func writeManagedPointer(root *os.Root, name, revision string) error {
	if !validManagedRevision(revision) {
		return fmt.Errorf("managed definition revision is invalid")
	}
	tempSuffix, err := newPackStagingName()
	if err != nil {
		return err
	}
	tempName := managedStorageRoot + "/." + filepath.Base(tempSuffix)
	file, err := root.OpenFile(tempName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	data := []byte(revision + "\n")
	written, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil || written != len(data) {
		_ = root.Remove(tempName)
		return fmt.Errorf("write managed definition pointer: %w", errors.Join(writeErr, syncErr, closeErr))
	}
	if err := root.Rename(tempName, name); err != nil {
		_ = root.Remove(tempName)
		return fmt.Errorf("publish managed definition pointer: %w", err)
	}
	return nil
}
