package talon

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// This opt-in test signs a local clean Core library with an ephemeral test key
// and runs the normal SDK verification path. It is local interoperability
// evidence, not a talon-bin release or a production trust identity.
func TestLocalSignedCoreKVInterop(t *testing.T) {
	libraryPath := os.Getenv("TALON_TEST_LOCAL_CORE_LIBRARY")
	headerPath := os.Getenv("TALON_TEST_LOCAL_CORE_HEADER")
	buildPath := os.Getenv("TALON_TEST_LOCAL_CORE_BUILD_MANIFEST")
	if libraryPath == "" || headerPath == "" || buildPath == "" {
		t.Skip("set TALON_TEST_LOCAL_CORE_LIBRARY, TALON_TEST_LOCAL_CORE_HEADER, and TALON_TEST_LOCAL_CORE_BUILD_MANIFEST")
	}
	if os.Getenv("TALON_TEST_PRODUCTION_NATIVE") == "1" {
		t.Skip("local test artifact cannot share the process with a production native policy")
	}
	bundle, err := makeTestNativeBundle(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	platform, err := currentNativePlatform()
	if err != nil {
		t.Fatal(err)
	}
	library, err := os.ReadFile(libraryPath)
	if err != nil {
		t.Fatal(err)
	}
	header, err := os.ReadFile(headerPath)
	if err != nil {
		t.Fatal(err)
	}
	buildBytes, err := os.ReadFile(buildPath)
	if err != nil {
		t.Fatal(err)
	}
	var build coreBuildManifest
	if err := decodeStrictJSON(buildBytes, &build); err != nil {
		t.Fatal(err)
	}
	if build.GitDirty == nil || *build.GitDirty {
		t.Fatal("Core library is not self-attested clean")
	}
	headerHash := sha256.Sum256(header)
	if build.HeaderSHA256 != hex.EncodeToString(headerHash[:]) {
		t.Fatal("Core self-manifest does not match supplied C header")
	}
	manifestBytes, err := os.ReadFile(bundle.manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest nativeManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Source.Commit = build.GitCommit
	manifest.Source.CargoVersion = build.CoreSemver
	manifest.Source.CargoLockSHA256 = build.CargoLockSHA256
	manifest.ABI.Profile = build.ABI.Profile
	manifest.ABI.Version = build.ABI.Version
	manifest.ABI.HeaderSHA256 = build.HeaderSHA256
	manifest.ABI.RequiredSymbols = append([]string(nil), build.ABI.RequiredSymbols...)
	manifest.Build.Target = build.Target
	manifest.Build.Source = "local-interoperability-test"
	manifest.Build.WorkflowRunID = "local-test"
	manifest.Build.Rustc = "local test compiler not recorded"
	manifest.Build.Cargo = "local test compiler not recorded"
	manifest.Build.Reproducibility = "ephemeral local signed test artifact"
	archiveName := manifest.Artifact.Archive.Path
	staticPlaceholder := []byte("local test fixture: static library is not linked\n")
	license, err := os.ReadFile(filepath.Join(bundle.policy.BundleDir, "LICENSE.core"))
	if err != nil {
		t.Fatal(err)
	}
	notice, err := os.ReadFile(filepath.Join(bundle.policy.BundleDir, "NOTICE"))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		platform.StaticLibrary:  staticPlaceholder,
		platform.DynamicLibrary: library,
		"talon.h":               header,
		"LICENSE.core":          license,
		"NOTICE":                notice,
	}
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, name := range []string{platform.StaticLibrary, platform.DynamicLibrary, "talon.h", "LICENSE.core", "NOTICE"} {
		contents := files[name]
		if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o500, Size: int64(len(contents)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(contents); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	record := func(name string, contents []byte) fileRecord {
		hash := sha256.Sum256(contents)
		return fileRecord{Path: name, Size: int64(len(contents)), SHA256: hex.EncodeToString(hash[:])}
	}
	manifest.Artifact.Archive = record(archiveName, archive.Bytes())
	for i, file := range manifest.Artifact.Files {
		contents, ok := files[file.Path]
		if !ok {
			t.Fatalf("unexpected archive member %q", file.Path)
		}
		manifest.Artifact.Files[i] = record(file.Path, contents)
	}
	if err := os.WriteFile(filepath.Join(bundle.policy.BundleDir, archiveName), archive.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	manifestBytes, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bundle.manifestPath, manifestBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bundle.signaturePath, ed25519.Sign(bundle.privateKey, manifestBytes), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := bundle.policy
	policy.ExpectedCoreCommit = build.GitCommit
	policy.ExpectedCoreVersion = build.CoreSemver
	policy.ExpectedABIProfile = build.ABI.Profile
	policy.ExpectedABIVersion = build.ABI.Version
	policy.ExpectedHeaderSHA256 = build.HeaderSHA256
	policy.RequiredCapabilities = []string{"native_kv_read"}
	databasePath := t.TempDir()
	db, err := OpenWithOptions(databasePath, OpenOptions{Native: policy})
	if err != nil {
		t.Fatal(fmt.Errorf("open locally signed Core artifact: %w", err))
	}
	assertKVPublicResults(t, db)
	assertNativeKVBinaryRoundTrip(t, db)
	if _, err := db.QueryResult("CREATE TABLE sdk_sql_live (id INT PRIMARY KEY, name TEXT)"); err != nil {
		t.Fatal(err)
	}
	empty, err := db.QueryResult("SELECT id, name FROM sdk_sql_live WHERE id = ?", IntegerValue(99))
	if err != nil || len(empty.Columns) != 2 || empty.Columns[0] != "id" || empty.Columns[1] != "name" || len(empty.Rows) != 0 {
		t.Fatalf("empty SQL result = %#v, %v", empty, err)
	}
	firstName, err := TextValue("first")
	if err != nil {
		t.Fatal(err)
	}
	inserted, err := db.QueryResult("INSERT INTO sdk_sql_live VALUES (?, ?)", IntegerValue(1), firstName)
	if err != nil || inserted.AffectedRows == nil || *inserted.AffectedRows != 1 {
		t.Fatalf("insert SQL result = %#v, %v", inserted, err)
	}
	if _, err := db.QueryResult("BEGIN"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.QueryResult("INSERT INTO sdk_sql_live VALUES (2, 'second')"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.QueryResult("ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	rolledBack, err := db.QueryResult("SELECT id FROM sdk_sql_live WHERE id = 2")
	if err != nil || len(rolledBack.Rows) != 0 {
		t.Fatalf("rolled-back SQL result = %#v, %v", rolledBack, err)
	}
	if err := db.Persist(); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = OpenWithOptions(databasePath, OpenOptions{Native: policy})
	if err != nil {
		t.Fatalf("reopen locally signed Core artifact: %v", err)
	}
	defer db.Close()
	got, err := db.KVGetBytes([]byte{0, 'k', 0xff})
	if err != nil || !got.Present || !bytes.Equal(got.Value, []byte{0, 0xff, 0}) {
		t.Fatalf("binary value after restart = %#v, %v", got, err)
	}
}
