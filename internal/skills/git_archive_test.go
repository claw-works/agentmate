package skills

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type testArchiveEntry struct {
	name     string
	content  string
	typeflag byte
	linkname string
}

func TestGitProviderClientFetchPackage(t *testing.T) {
	archive := buildTestGitArchive(t, []testArchiveEntry{
		{name: "repo-root/README.md", content: "outside"},
		{name: "repo-root/skills/demo/SKILL.md", content: "---\nname: demo\ndescription: Demo\n---\n"},
		{name: "repo-root/skills/demo/resources/data.txt", content: "resource"},
		{name: "repo-root/skills/other/SKILL.md", content: "other"},
	})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		// GitHub's tarball endpoint answers HTTP 415 for a strict binary Accept
		// header, so assert the permissive header the real provider requires.
		if accept := request.Header.Get("Accept"); accept != "*/*" {
			writer.WriteHeader(http.StatusUnsupportedMediaType)
			_, _ = writer.Write([]byte(`{"message":"Unsupported 'Accept' header"}`))
			return
		}
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write(archive)
	}))
	defer server.Close()

	client := newGitProviderClient(server.Client())
	files, err := client.FetchPackage(context.Background(), resolvedGitRevision{Provider: "github", ArchiveURL: server.URL}, "skills/demo")
	if err != nil {
		t.Fatalf("FetchPackage() error: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("files = %#v, want 2", files)
	}
	if files[0].Path != "SKILL.md" || files[1].Path != "resources/data.txt" {
		t.Fatalf("file paths = %q, %q", files[0].Path, files[1].Path)
	}
	if files[0].Content == "" || files[1].Content != "resource" {
		t.Fatalf("text content was not retained: %#v", files)
	}
}

func TestExtractGitPackageRejectsPathTraversal(t *testing.T) {
	archive := buildTestGitArchive(t, []testArchiveEntry{{
		name:    "repo-root/skills/demo/../../escape.txt",
		content: "escape",
	}})
	_, err := extractGitPackage(bytes.NewReader(archive), "skills/demo", defaultGitArchiveLimits())
	if err == nil || !strings.Contains(err.Error(), "invalid Git archive path") {
		t.Fatalf("extractGitPackage() error = %v", err)
	}
}

func TestExtractGitPackageRejectsLinks(t *testing.T) {
	archive := buildTestGitArchive(t, []testArchiveEntry{
		{name: "repo-root/skills/demo/SKILL.md", content: "# Demo"},
		{name: "repo-root/skills/demo/link", typeflag: tar.TypeSymlink, linkname: "../../secret"},
	})
	_, err := extractGitPackage(bytes.NewReader(archive), "skills/demo", defaultGitArchiveLimits())
	if err == nil || !strings.Contains(err.Error(), "unsupported archive entry type") {
		t.Fatalf("extractGitPackage() error = %v", err)
	}
}

func TestExtractGitPackageEnforcesFileLimit(t *testing.T) {
	archive := buildTestGitArchive(t, []testArchiveEntry{
		{name: "repo-root/skill/SKILL.md", content: "# Demo"},
		{name: "repo-root/skill/extra.txt", content: "extra"},
	})
	limits := defaultGitArchiveLimits()
	limits.MaxFiles = 1
	_, err := extractGitPackage(bytes.NewReader(archive), "skill", limits)
	if err == nil || !strings.Contains(err.Error(), "more than 1 files") {
		t.Fatalf("extractGitPackage() error = %v", err)
	}
}

func TestExtractGitPackageRejectsMultipleRoots(t *testing.T) {
	archive := buildTestGitArchive(t, []testArchiveEntry{
		{name: "root-one/skill/SKILL.md", content: "# Demo"},
		{name: "root-two/skill/extra.txt", content: "extra"},
	})
	_, err := extractGitPackage(bytes.NewReader(archive), "skill", defaultGitArchiveLimits())
	if err == nil || !strings.Contains(err.Error(), "multiple root directories") {
		t.Fatalf("extractGitPackage() error = %v", err)
	}
}

func buildTestGitArchive(t *testing.T, entries []testArchiveEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, entry := range entries {
		typeflag := entry.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		header := &tar.Header{
			Name:     entry.name,
			Mode:     0o644,
			Size:     int64(len(entry.content)),
			Typeflag: typeflag,
			Linkname: entry.linkname,
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatalf("WriteHeader(%s): %v", entry.name, err)
		}
		if typeflag == tar.TypeReg {
			if _, err := tarWriter.Write([]byte(entry.content)); err != nil {
				t.Fatalf("Write(%s): %v", entry.name, err)
			}
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buffer.Bytes()
}

func TestExtractGitPackageLimitsExcludedArchiveBytes(t *testing.T) {
	archive := buildTestGitArchive(t, []testArchiveEntry{
		{name: "repo-root/outside.txt", content: strings.Repeat("x", 11)},
		{name: "repo-root/skill/SKILL.md", content: "# Demo"},
	})
	limits := defaultGitArchiveLimits()
	limits.MaxUncompressedBytes = 10
	_, err := extractGitPackage(bytes.NewReader(archive), "skill", limits)
	if err == nil || !strings.Contains(err.Error(), "expands beyond 10 bytes") {
		t.Fatalf("extractGitPackage() error = %v", err)
	}
}

func TestExtractGitPackageLimitsExcludedArchiveEntries(t *testing.T) {
	archive := buildTestGitArchive(t, []testArchiveEntry{
		{name: "repo-root/outside-a.txt", content: "a"},
		{name: "repo-root/outside-b.txt", content: "b"},
		{name: "repo-root/skill/SKILL.md", content: "# Demo"},
	})
	limits := defaultGitArchiveLimits()
	limits.MaxArchiveEntries = 2
	_, err := extractGitPackage(bytes.NewReader(archive), "skill", limits)
	if err == nil || !strings.Contains(err.Error(), "more than 2 entries") {
		t.Fatalf("extractGitPackage() error = %v", err)
	}
}

func TestExtractGitPackageLimitsPAXMetadataEntries(t *testing.T) {
	archive := buildTestPAXGitArchive(t, "value")
	limits := defaultGitArchiveLimits()
	limits.MaxArchiveEntries = 1
	_, err := extractGitPackage(bytes.NewReader(archive), "skill", limits)
	if err == nil || !strings.Contains(err.Error(), "more than 1 entries") {
		t.Fatalf("extractGitPackage() error = %v", err)
	}
}

func TestExtractGitPackageLimitsPAXMetadataBytes(t *testing.T) {
	archive := buildTestPAXGitArchive(t, strings.Repeat("x", 64))
	limits := defaultGitArchiveLimits()
	limits.MaxUncompressedBytes = 32
	_, err := extractGitPackage(bytes.NewReader(archive), "skill", limits)
	if err == nil || !strings.Contains(err.Error(), "expands beyond 32 bytes") {
		t.Fatalf("extractGitPackage() error = %v", err)
	}
}

func buildTestPAXGitArchive(t *testing.T, paxValue string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	content := "# Demo"
	header := &tar.Header{
		Name:       "repo-root/skill/SKILL.md",
		Mode:       0o644,
		Size:       int64(len(content)),
		Typeflag:   tar.TypeReg,
		PAXRecords: map[string]string{"comment": paxValue},
	}
	if err := tarWriter.WriteHeader(header); err != nil {
		t.Fatalf("WriteHeader(PAX): %v", err)
	}
	if _, err := tarWriter.Write([]byte(content)); err != nil {
		t.Fatalf("Write(PAX): %v", err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buffer.Bytes()
}

// TestExtractGitPackageSkipsPaxGlobalHeader guards against a real GitHub
// tarball layout: the archive opens with a "pax_global_header" entry carrying
// no package path. If it is not skipped before the archive root is derived,
// its name becomes the root and every real entry is rejected as a second root.
func TestExtractGitPackageSkipsPaxGlobalHeader(t *testing.T) {
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
		"claw-works-demo-abc1234/demo/SKILL.md":            "---\nname: demo\ndescription: Demo\n---\n",
		"claw-works-demo-abc1234/demo/templates/output.md": "template",
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

	files, err := extractGitPackage(bytes.NewReader(buffer.Bytes()), "demo", defaultGitArchiveLimits())
	if err != nil {
		t.Fatalf("extractGitPackage() error: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("files = %#v, want 2", files)
	}
}
