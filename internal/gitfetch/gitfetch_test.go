package gitfetch

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
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
