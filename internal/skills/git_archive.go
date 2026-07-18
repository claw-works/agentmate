package skills

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
)

type gitArchiveLimits struct {
	MaxDownloadBytes     int64
	MaxArchiveEntries    int
	MaxUncompressedBytes int64
	MaxFiles             int
	MaxFileBytes         int64
	MaxPackageBytes      int64
}

func defaultGitArchiveLimits() gitArchiveLimits {
	return gitArchiveLimits{
		MaxDownloadBytes:     32 * 1024 * 1024,
		MaxArchiveEntries:    10_000,
		MaxUncompressedBytes: 256 * 1024 * 1024,
		MaxFiles:             300,
		MaxFileBytes:         16 * 1024 * 1024,
		MaxPackageBytes:      32 * 1024 * 1024,
	}
}

func (c *gitProviderClient) FetchPackage(ctx context.Context, revision resolvedGitRevision, packagePath string) ([]SnapshotFile, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, revision.ArchiveURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/octet-stream")
	request.Header.Set("User-Agent", "agentmate-skill-registry")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("download %s archive: %w", revision.Provider, err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 301))
		return nil, fmt.Errorf("download %s archive: HTTP %d: %s", revision.Provider, response.StatusCode, strings.TrimSpace(string(detail)))
	}

	limits := defaultGitArchiveLimits()
	limited := &io.LimitedReader{R: response.Body, N: limits.MaxDownloadBytes + 1}
	archiveBytes, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read %s archive: %w", revision.Provider, err)
	}
	if int64(len(archiveBytes)) > limits.MaxDownloadBytes {
		return nil, fmt.Errorf("Git archive exceeds %d bytes", limits.MaxDownloadBytes)
	}
	return extractGitPackage(bytes.NewReader(archiveBytes), packagePath, limits)
}

func extractGitPackage(archive io.Reader, packagePath string, limits gitArchiveLimits) ([]SnapshotFile, error) {
	packagePath = normalizeOptionalRelativePath(packagePath)
	if packagePath == ".." || strings.HasPrefix(packagePath, "../") || path.IsAbs(packagePath) {
		return nil, fmt.Errorf("package_path must be relative")
	}
	if limits.MaxDownloadBytes <= 0 || limits.MaxArchiveEntries <= 0 || limits.MaxUncompressedBytes <= 0 || limits.MaxFiles <= 0 || limits.MaxFileBytes <= 0 || limits.MaxPackageBytes <= 0 {
		return nil, fmt.Errorf("invalid Git archive limits")
	}

	compressed, err := io.ReadAll(io.LimitReader(archive, limits.MaxDownloadBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read Git archive: %w", err)
	}
	if int64(len(compressed)) > limits.MaxDownloadBytes {
		return nil, fmt.Errorf("Git archive exceeds %d bytes", limits.MaxDownloadBytes)
	}
	if err := validateGitTarLimits(compressed, limits); err != nil {
		return nil, err
	}

	gzipReader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, fmt.Errorf("open Git archive: %w", err)
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)

	files := make([]SnapshotFile, 0)
	seen := make(map[string]struct{})
	var archiveRoot string
	var packageBytes int64

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read Git archive: %w", err)
		}
		entryPath, root, err := normalizeArchiveEntryPath(header.Name)
		if err != nil {
			return nil, err
		}
		if archiveRoot == "" {
			archiveRoot = root
		} else if root != archiveRoot {
			return nil, fmt.Errorf("Git archive contains multiple root directories")
		}
		if entryPath == "" {
			continue
		}

		packageFilePath, included := archivePackagePath(entryPath, packagePath)
		if !included {
			continue
		}
		if header.Typeflag == tar.TypeDir {
			continue
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return nil, fmt.Errorf("unsupported archive entry type for %s", packageFilePath)
		}
		if header.Size < 0 || header.Size > limits.MaxFileBytes {
			return nil, fmt.Errorf("archive file %s exceeds %d bytes", packageFilePath, limits.MaxFileBytes)
		}
		packageBytes += header.Size
		if packageBytes > limits.MaxPackageBytes {
			return nil, fmt.Errorf("Git package exceeds %d bytes", limits.MaxPackageBytes)
		}
		if len(files) >= limits.MaxFiles {
			return nil, fmt.Errorf("Git package contains more than %d files", limits.MaxFiles)
		}
		if _, exists := seen[packageFilePath]; exists {
			return nil, fmt.Errorf("duplicate archive file path: %s", packageFilePath)
		}
		seen[packageFilePath] = struct{}{}

		content, err := io.ReadAll(io.LimitReader(tarReader, limits.MaxFileBytes+1))
		if err != nil {
			return nil, fmt.Errorf("read archive file %s: %w", packageFilePath, err)
		}
		if int64(len(content)) != header.Size {
			return nil, fmt.Errorf("archive file %s size mismatch", packageFilePath)
		}
		mimeType := inferMimeType(packageFilePath)
		indexable := isIndexableText(packageFilePath, mimeType)
		textContent := ""
		if indexable {
			textContent = string(content)
		}
		files = append(files, SnapshotFile{
			Path:      packageFilePath,
			SHA256:    sha256HexString(string(content)),
			SizeBytes: header.Size,
			MimeType:  mimeType,
			Indexable: indexable,
			Content:   textContent,
		})
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("package_path %q contains no files", packagePath)
	}
	return files, nil
}

func normalizeArchiveEntryPath(value string) (entryPath, root string, err error) {
	if value == "" || strings.Contains(value, "\\") || strings.HasPrefix(value, "/") {
		return "", "", fmt.Errorf("invalid Git archive path: %s", value)
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == ".." {
			return "", "", fmt.Errorf("invalid Git archive path: %s", value)
		}
	}
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", "", fmt.Errorf("invalid Git archive path: %s", value)
	}
	parts := strings.SplitN(cleaned, "/", 2)
	root = parts[0]
	if root == "" || root == "." || root == ".." {
		return "", "", fmt.Errorf("invalid Git archive root")
	}
	if len(parts) == 1 {
		return "", root, nil
	}
	return parts[1], root, nil
}

func archivePackagePath(repositoryPath, packagePath string) (string, bool) {
	if packagePath == "" {
		return repositoryPath, true
	}
	if repositoryPath == packagePath {
		return "", true
	}
	prefix := packagePath + "/"
	if !strings.HasPrefix(repositoryPath, prefix) {
		return "", false
	}
	return strings.TrimPrefix(repositoryPath, prefix), true
}

func validateGitTarLimits(compressed []byte, limits gitArchiveLimits) error {
	gzipReader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return fmt.Errorf("open Git archive: %w", err)
	}
	defer gzipReader.Close()

	var header [512]byte
	var archiveEntries int
	var uncompressedBytes int64
	for {
		if _, err := io.ReadFull(gzipReader, header[:]); err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("read Git archive header: %w", err)
		}
		if isZeroTarBlock(header[:]) {
			return nil
		}

		archiveEntries++
		if archiveEntries > limits.MaxArchiveEntries {
			return fmt.Errorf("Git archive contains more than %d entries", limits.MaxArchiveEntries)
		}
		size, err := parseTarEntrySize(header[124:136])
		if err != nil {
			return fmt.Errorf("read Git archive entry size: %w", err)
		}
		if size > limits.MaxUncompressedBytes-uncompressedBytes {
			return fmt.Errorf("Git archive expands beyond %d bytes", limits.MaxUncompressedBytes)
		}
		uncompressedBytes += size

		paddedSize := size
		if remainder := size % 512; remainder != 0 {
			paddedSize += 512 - remainder
		}
		if _, err := io.CopyN(io.Discard, gzipReader, paddedSize); err != nil {
			return fmt.Errorf("read Git archive entry: %w", err)
		}
	}
}

func parseTarEntrySize(field []byte) (int64, error) {
	if len(field) == 0 {
		return 0, fmt.Errorf("missing size")
	}
	if field[0]&0x80 != 0 {
		if field[0]&0x40 != 0 {
			return 0, fmt.Errorf("negative size")
		}
		var value int64
		for index, current := range field {
			if index == 0 {
				current &= 0x7f
			}
			if value > ((1<<63-1)-int64(current))/256 {
				return 0, fmt.Errorf("size overflows int64")
			}
			value = value*256 + int64(current)
		}
		return value, nil
	}

	value := strings.Trim(string(field), " \x00")
	if value == "" {
		return 0, nil
	}
	var size int64
	for _, digit := range value {
		if digit < '0' || digit > '7' {
			return 0, fmt.Errorf("invalid octal size")
		}
		if size > ((1<<63-1)-int64(digit-'0'))/8 {
			return 0, fmt.Errorf("size overflows int64")
		}
		size = size*8 + int64(digit-'0')
	}
	return size, nil
}

func isZeroTarBlock(block []byte) bool {
	for _, value := range block {
		if value != 0 {
			return false
		}
	}
	return true
}
