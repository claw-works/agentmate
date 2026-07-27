package gitfetch

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func buildArchive(t *testing.T, entries map[string]string) *bytes.Reader {
	t.Helper()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	for name, content := range entries {
		if err := tarWriter.WriteHeader(&tar.Header{
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(content)),
			Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatalf("write header: %v", err)
		}
		if _, err := tarWriter.Write([]byte(content)); err != nil {
			t.Fatalf("write content: %v", err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return bytes.NewReader(buffer.Bytes())
}

func TestExtractPackageFiltersByPackagePath(t *testing.T) {
	archive := buildArchive(t, map[string]string{
		"repo-root/README.md":                "root readme",
		"repo-root/kb/KNOWLEDGE.yaml":        "name: kb\n",
		"repo-root/kb/raw/guide.md":          "# Guide\n",
		"repo-root/other/unrelated.md":       "ignored",
		"repo-root/kb/raw/nested/details.md": "details",
	})
	files, err := ExtractPackage(archive, "kb", DefaultArchiveLimits())
	if err != nil {
		t.Fatalf("ExtractPackage error = %v", err)
	}
	paths := map[string]string{}
	for _, file := range files {
		paths[file.Path] = string(file.Content)
	}
	if len(paths) != 3 {
		t.Fatalf("files = %#v", paths)
	}
	if paths["KNOWLEDGE.yaml"] != "name: kb\n" || paths["raw/guide.md"] != "# Guide\n" || paths["raw/nested/details.md"] != "details" {
		t.Fatalf("unexpected extraction: %#v", paths)
	}
}

func TestExtractPackageRejectsTraversalAndEmptyPackage(t *testing.T) {
	traversal := buildArchive(t, map[string]string{"repo-root/../escape.md": "x"})
	if _, err := ExtractPackage(traversal, "", DefaultArchiveLimits()); err == nil {
		t.Fatal("expected traversal rejection")
	}

	empty := buildArchive(t, map[string]string{"repo-root/README.md": "x"})
	if _, err := ExtractPackage(empty, "missing-dir", DefaultArchiveLimits()); err == nil || !strings.Contains(err.Error(), "contains no files") {
		t.Fatalf("empty package error = %v", err)
	}
}

func TestExtractPackageEnforcesFileLimit(t *testing.T) {
	entries := map[string]string{}
	for index := 0; index < 4; index++ {
		entries["root/kb/file-"+strings.Repeat("a", index+1)+".md"] = "content"
	}
	limits := DefaultArchiveLimits()
	limits.MaxFiles = 3
	if _, err := ExtractPackage(buildArchive(t, entries), "kb", limits); err == nil || !strings.Contains(err.Error(), "more than 3 files") {
		t.Fatalf("file limit error = %v", err)
	}
}

func TestParseRepositoryURL(t *testing.T) {
	repository, err := ParseRepositoryURL("https://github.com/acme/knowledge.git")
	if err != nil || repository.Provider != "github" || repository.ProjectPath != "acme/knowledge" {
		t.Fatalf("repository = %#v, err = %v", repository, err)
	}
	repository, err = ParseRepositoryURL("https://gitlab.com/acme/group/knowledge")
	if err != nil || repository.Provider != "gitlab" || repository.ProjectPath != "acme/group/knowledge" {
		t.Fatalf("repository = %#v, err = %v", repository, err)
	}
	for _, rejected := range []string{
		"http://github.com/acme/knowledge",
		"https://user@github.com/acme/knowledge",
		"https://github.com/acme/knowledge?ref=main",
		"https://git.example.com/acme/knowledge",
		"file:///tmp/knowledge",
	} {
		if _, err := ParseRepositoryURL(rejected); err == nil {
			t.Errorf("ParseRepositoryURL(%q) must be rejected", rejected)
		}
	}
}

// TestFetchPackageSendsPermissiveAccept guards the archive download request
// headers. GitHub's tarball endpoint answers HTTP 415 for a strict binary
// Accept header before it can redirect to codeload, so a mock that ignores
// request headers would let a real-world failure pass unnoticed.
func TestFetchPackageSendsPermissiveAccept(t *testing.T) {
	archive, err := io.ReadAll(buildArchive(t, map[string]string{
		"repo-root/kb/KNOWLEDGE.yaml": "name: kb\n",
		"repo-root/kb/raw/faq.md":     "# FAQ\n",
	}))
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}

	var gotAccept, gotUserAgent string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		gotAccept = request.Header.Get("Accept")
		gotUserAgent = request.Header.Get("User-Agent")
		if gotAccept == "application/octet-stream" {
			writer.WriteHeader(http.StatusUnsupportedMediaType)
			_, _ = writer.Write([]byte(`{"message":"Unsupported 'Accept' header"}`))
			return
		}
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write(archive)
	}))
	defer server.Close()

	client := NewClient(server.Client())
	files, err := client.FetchPackage(context.Background(),
		ResolvedRevision{Provider: "github", ArchiveURL: server.URL},
		"kb", DefaultArchiveLimits())
	if err != nil {
		t.Fatalf("FetchPackage() error: %v", err)
	}
	if gotAccept != "*/*" {
		t.Fatalf("Accept = %q, want */*", gotAccept)
	}
	if gotUserAgent == "" {
		t.Fatal("User-Agent must be set for provider requests")
	}
	if len(files) != 2 {
		t.Fatalf("files = %#v, want 2", files)
	}
}

// TestExtractPackageSkipsPaxGlobalHeader guards against a real GitHub tarball
// layout: the archive opens with a "pax_global_header" entry that carries no
// package path. If it is not skipped before the archive root is derived, its
// name becomes the root and every real entry is rejected as a second root.
func TestExtractPackageSkipsPaxGlobalHeader(t *testing.T) {
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)

	if err := tarWriter.WriteHeader(&tar.Header{
		Name:       "pax_global_header",
		Typeflag:   tar.TypeXGlobalHeader,
		PAXRecords: map[string]string{"comment": "0000000000000000000000000000000000000000"},
	}); err != nil {
		t.Fatalf("write pax header: %v", err)
	}
	for name, content := range map[string]string{
		"claw-works-demo-abc1234/kb/KNOWLEDGE.yaml": "name: kb\n",
		"claw-works-demo-abc1234/kb/raw/faq.md":     "# FAQ\n",
	} {
		if err := tarWriter.WriteHeader(&tar.Header{
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(content)),
			Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatalf("write header: %v", err)
		}
		if _, err := tarWriter.Write([]byte(content)); err != nil {
			t.Fatalf("write content: %v", err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}

	files, err := ExtractPackage(bytes.NewReader(buffer.Bytes()), "kb", DefaultArchiveLimits())
	if err != nil {
		t.Fatalf("ExtractPackage() error: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("files = %#v, want 2", files)
	}
	for _, file := range files {
		if file.Path != "KNOWLEDGE.yaml" && file.Path != "raw/faq.md" {
			t.Fatalf("unexpected package path %q", file.Path)
		}
	}
}
