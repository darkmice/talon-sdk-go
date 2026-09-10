package talon

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

type testNativeBundle struct {
	policy        NativePolicy
	privateKey    ed25519.PrivateKey
	manifestPath  string
	signaturePath string
}

func testNativePolicy(t *testing.T) NativePolicy {
	t.Helper()
	bundle, err := makeTestNativeBundle(t.TempDir())
	if err != nil {
		t.Fatalf("make test native bundle: %v", err)
	}
	return bundle.policy
}

func openTestDB(t *testing.T) *DB {
	t.Helper()
	if os.Getenv("TALON_TEST_PRODUCTION_NATIVE") != "1" {
		t.Skip("runtime E2E requires a released, signed, clean, self-attested Core bundle")
	}
	policy, err := NativePolicyFromEnvironment()
	if err != nil {
		t.Fatalf("production native policy: %v", err)
	}
	db, err := OpenWithOptions(t.TempDir(), OpenOptions{Native: policy})
	if err != nil {
		t.Fatalf("OpenWithOptions: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

func TestNativePlatformContractCoversEveryEnterpriseTarget(t *testing.T) {
	tests := []struct {
		goos     string
		goarch   string
		expected nativePlatform
	}{
		{"linux", "amd64", nativePlatform{"linux-amd64", "x86_64-unknown-linux-gnu", "ubuntu-24.04", "libtalon.a", "libtalon.so"}},
		{"linux", "arm64", nativePlatform{"linux-arm64", "aarch64-unknown-linux-gnu", "ubuntu-24.04", "libtalon.a", "libtalon.so"}},
		{"darwin", "amd64", nativePlatform{"macos-amd64", "x86_64-apple-darwin", "macos-15-intel", "libtalon.a", "libtalon.dylib"}},
		{"darwin", "arm64", nativePlatform{"macos-arm64", "aarch64-apple-darwin", "macos-15", "libtalon.a", "libtalon.dylib"}},
	}
	for _, test := range tests {
		t.Run(test.goos+"-"+test.goarch, func(t *testing.T) {
			actual, err := nativePlatformFor(test.goos, test.goarch)
			if err != nil {
				t.Fatal(err)
			}
			if actual != test.expected {
				t.Fatalf("platform = %#v, want %#v", actual, test.expected)
			}
		})
	}
	if _, err := nativePlatformFor("windows", "amd64"); err == nil {
		t.Fatal("unsupported platform was accepted")
	}
}

func makeTestNativeBundle(root string) (testNativeBundle, error) {
	if root == "" {
		var err error
		root, err = os.MkdirTemp("", "talon-sdk-native-test-")
		if err != nil {
			return testNativeBundle{}, err
		}
	}
	platform, err := currentNativePlatform()
	if err != nil {
		return testNativeBundle{}, err
	}
	files := map[string][]byte{
		platform.DynamicLibrary: []byte("test-only synthetic dynamic library; never dlopen\n"),
		platform.StaticLibrary:  []byte("test-only synthetic static library; never link\n"),
		"talon.h":               []byte("/* test-only synthetic talon-native-c header */\n"),
		"LICENSE.core":          []byte("test-only license fixture\n"),
	}
	files["NOTICE"] = []byte("test-only Talon native bundle\n")

	archiveName := "libtalon-core-" + platform.Name + ".tar.gz"
	archivePath := filepath.Join(root, archiveName)
	archiveFile, err := os.Create(archivePath)
	if err != nil {
		return testNativeBundle{}, err
	}
	gzipWriter := gzip.NewWriter(archiveFile)
	tarWriter := tar.NewWriter(gzipWriter)
	names := []string{platform.StaticLibrary, platform.DynamicLibrary, "talon.h", "LICENSE.core", "NOTICE"}
	for _, name := range names {
		value := files[name]
		if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o500, Size: int64(len(value)), Typeflag: tar.TypeReg}); err != nil {
			return testNativeBundle{}, err
		}
		if _, err := tarWriter.Write(value); err != nil {
			return testNativeBundle{}, err
		}
	}
	if err := tarWriter.Close(); err != nil {
		return testNativeBundle{}, err
	}
	if err := gzipWriter.Close(); err != nil {
		return testNativeBundle{}, err
	}
	if err := archiveFile.Close(); err != nil {
		return testNativeBundle{}, err
	}

	outerFiles := map[string][]byte{
		"libtalon-core-" + platform.Name + ".sbom.cdx.json": []byte(`{"bomFormat":"CycloneDX"}` + "\n"),
		"libtalon-core-" + platform.Name + ".licenses.json": []byte(`{"schema_version":"1.0","packages":[]}` + "\n"),
		"LICENSE.core": files["LICENSE.core"],
		"NOTICE":       files["NOTICE"],
	}
	for name, value := range outerFiles {
		if err := os.WriteFile(filepath.Join(root, name), value, 0o600); err != nil {
			return testNativeBundle{}, err
		}
	}

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return testNativeBundle{}, err
	}
	publicDER, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return testNativeBundle{}, err
	}
	keyHash := sha256.Sum256(publicDER)
	keyFingerprint := hex.EncodeToString(keyHash[:])
	headerHash := sha256.Sum256(files["talon.h"])
	headerFingerprint := hex.EncodeToString(headerHash[:])
	record := func(name string, value []byte) fileRecord {
		hash := sha256.Sum256(value)
		return fileRecord{Path: name, Size: int64(len(value)), SHA256: hex.EncodeToString(hash[:])}
	}
	archiveBytes, err := os.ReadFile(archivePath)
	if err != nil {
		return testNativeBundle{}, err
	}
	manifestName := "libtalon-core-" + platform.Name + ".manifest.json"
	manifest := nativeManifest{
		SchemaVersion: "1.0",
		Release:       nativeRelease{Tag: "v1.2.3", Channel: nativeReleaseChannel, TalonBinCommit: "1111111111111111111111111111111111111111"},
		Source:        nativeSource{Repository: "https://github.com/darkmice/talon-core", Tag: "v0.1.1", Commit: "18b96399610a9f8c2498d57f3a8ddcd44c2f57a0", CargoVersion: "0.1.1", CargoLockSHA256: "2222222222222222222222222222222222222222222222222222222222222222", SourceDateEpoch: 1, Clean: true},
		ABI:           nativeABI{Profile: "talon-native-c", Version: 1, HeaderSHA256: headerFingerprint, RequiredSymbols: append([]string(nil), sdkRequiredSymbols...)},
		Build:         nativeBuild{Source: "test", WorkflowRunID: "test-run", Runner: platform.Runner, Target: platform.Target, Rustc: "rustc 1.92.0", Cargo: "cargo 1.92.0", Command: []string{"build", "--locked", "--release", "--lib"}, Reproducibility: "test-only exact bytes"},
		Artifact:      nativeArtifact{Kind: "talon-core-native-library", Platform: platform.Name, Archive: record(archiveName, archiveBytes), Files: []fileRecord{record(platform.StaticLibrary, files[platform.StaticLibrary]), record(platform.DynamicLibrary, files[platform.DynamicLibrary]), record("talon.h", files["talon.h"]), record("LICENSE.core", files["LICENSE.core"]), record("NOTICE", files["NOTICE"])}},
		Materials:     nativeMaterials{SBOM: record("libtalon-core-"+platform.Name+".sbom.cdx.json", outerFiles["libtalon-core-"+platform.Name+".sbom.cdx.json"]), LicenseInventory: record("libtalon-core-"+platform.Name+".licenses.json", outerFiles["libtalon-core-"+platform.Name+".licenses.json"]), CoreLicense: record("LICENSE.core", outerFiles["LICENSE.core"]), Notice: record("NOTICE", outerFiles["NOTICE"])},
		Signing:       nativeSigning{Algorithm: "Ed25519", KeyID: "test-release-key", PublicKeySHA256: keyFingerprint, Signature: manifestName + ".sig"},
		Compatibility: nativeCompatibility{SDKContract: nativeManifestContract, RuntimeVerification: "ed25519-manifest+sha256-native+abi-identity", BinarySelfAttestation: true},
		Gates:         nativeGates{StorageConditionalBatchV1: CapabilityGate{Status: "gated", Reason: "test fixture mirrors the current Core ABI gate"}},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return testNativeBundle{}, err
	}
	manifestPath := filepath.Join(root, manifestName)
	if err := os.WriteFile(manifestPath, manifestBytes, 0o600); err != nil {
		return testNativeBundle{}, err
	}
	signaturePath := manifestPath + ".sig"
	if err := os.WriteFile(signaturePath, ed25519.Sign(privateKey, manifestBytes), 0o600); err != nil {
		return testNativeBundle{}, err
	}
	publicPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})
	return testNativeBundle{
		policy: NativePolicy{
			BundleDir: root, PublicKeyPEM: publicPEM, ExpectedKeyID: "test-release-key", ExpectedKeySHA256: keyFingerprint,
			ExpectedReleaseTag: "v1.2.3", ExpectedTalonBinCommit: "1111111111111111111111111111111111111111",
			ExpectedCoreRepository: "https://github.com/darkmice/talon-core", ExpectedCoreTag: "v0.1.1", ExpectedCoreCommit: "18b96399610a9f8c2498d57f3a8ddcd44c2f57a0", ExpectedCoreVersion: "0.1.1",
			ExpectedABIProfile: "talon-native-c", ExpectedABIVersion: 1, ExpectedHeaderSHA256: headerFingerprint,
		},
		privateKey: privateKey, manifestPath: manifestPath, signaturePath: signaturePath,
	}, nil
}

func TestOpenFailsClosedWithoutNativePolicy(t *testing.T) {
	for _, name := range []string{
		"TALON_NATIVE_BUNDLE_DIR", "TALON_NATIVE_PUBLIC_KEY_FILE", "TALON_NATIVE_EXPECTED_KEY_ID",
		"TALON_NATIVE_EXPECTED_KEY_SHA256", "TALON_NATIVE_EXPECTED_RELEASE_TAG", "TALON_NATIVE_EXPECTED_TALON_BIN_COMMIT",
		"TALON_NATIVE_EXPECTED_CORE_REPOSITORY", "TALON_NATIVE_EXPECTED_CORE_TAG", "TALON_NATIVE_EXPECTED_CORE_COMMIT",
		"TALON_NATIVE_EXPECTED_CORE_VERSION", "TALON_NATIVE_EXPECTED_ABI_PROFILE", "TALON_NATIVE_EXPECTED_ABI_VERSION",
		"TALON_NATIVE_EXPECTED_HEADER_SHA256", "TALON_NATIVE_REQUIRED_CAPABILITIES",
	} {
		t.Setenv(name, "")
	}
	_, err := Open(t.TempDir())
	if ErrorCodeOf(err) != CodeNativeVerification {
		t.Fatalf("Open() code = %q, error = %v", ErrorCodeOf(err), err)
	}
}

func TestRuntimeCapabilitiesMayBeRequiredButAreDeferredToRuntimeAttestation(t *testing.T) {
	policy := testNativePolicy(t)
	policy.RequiredCapabilities = []string{"storage_conditional_point_read", "revision_stream"}
	if err := validateNativePolicy(policy); err != nil {
		t.Fatalf("runtime capability policy was not recognized: %v", err)
	}
	verified, err := verifyNativeBundle(policy)
	if err != nil {
		t.Fatalf("external bundle verification rejected deferred runtime gate: %v", err)
	}
	t.Cleanup(func() { _ = removeVerifiedNative(verified) })
	if len(verified.requiredCapabilities) != 2 || verified.requiredCapabilities[0] != "storage_conditional_point_read" || verified.requiredCapabilities[1] != "revision_stream" {
		t.Fatalf("required runtime capabilities were not retained: %#v", verified.requiredCapabilities)
	}
}

func TestOpenRejectsInvalidPathBeforeNativeLoading(t *testing.T) {
	for _, path := range []string{"", "bad\x00path", string([]byte{0xff})} {
		_, err := OpenWithOptions(path, OpenOptions{})
		if ErrorCodeOf(err) != CodeInvalidArgument {
			t.Fatalf("OpenWithOptions(%q) code = %q, error = %v", path, ErrorCodeOf(err), err)
		}
	}
}

func TestVerifyNativeBundleRejectsManifestTampering(t *testing.T) {
	bundle, err := makeTestNativeBundle(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(bundle.manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Replace(manifest, []byte(`"workflow_run_id":"test-run"`), []byte(`"workflow_run_id":"best-run"`), 1)
	if bytes.Equal(tampered, manifest) {
		t.Fatal("test manifest did not contain the expected signed field")
	}
	if err := os.WriteFile(bundle.manifestPath, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyNativeBundle(bundle.policy); err == nil {
		t.Fatal("tampered manifest was accepted")
	}
}

func TestVerifyNativeBundleRejectsUnknownManifestField(t *testing.T) {
	bundle, err := makeTestNativeBundle(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(bundle.manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest = append([]byte(`{"unexpected":true,`), manifest[1:]...)
	if err := os.WriteFile(bundle.manifestPath, manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bundle.signaturePath, ed25519.Sign(bundle.privateKey, manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyNativeBundle(bundle.policy); err == nil {
		t.Fatal("unknown manifest field was accepted")
	}
}

func TestVerifyNativeBundleRejectsDuplicateManifestField(t *testing.T) {
	bundle, err := makeTestNativeBundle(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(bundle.manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest = append([]byte(`{"schema_version":"1.0",`), manifest[1:]...)
	if err := os.WriteFile(bundle.manifestPath, manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bundle.signaturePath, ed25519.Sign(bundle.privateKey, manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyNativeBundle(bundle.policy); err == nil {
		t.Fatal("duplicate manifest field was accepted")
	}
}

func TestVerifyNativeBundleRequiresBinarySelfAttestation(t *testing.T) {
	bundle, err := makeTestNativeBundle(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(bundle.manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest nativeManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Compatibility.BinarySelfAttestation = false
	data, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bundle.manifestPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bundle.signaturePath, ed25519.Sign(bundle.privateKey, data), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyNativeBundle(bundle.policy); err == nil {
		t.Fatal("bundle without binary self-attestation was accepted")
	}
}

func TestVerifiedNativeIdentityAndCapabilityGate(t *testing.T) {
	verified, err := verifyNativeBundle(testNativePolicy(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = removeVerifiedNative(verified) })
	db := &DB{nativeInfo: verified.Info}
	info := db.NativeInfo()
	if info.CoreCommit != "18b96399610a9f8c2498d57f3a8ddcd44c2f57a0" || info.ABIProfile != "talon-native-c" || info.LibrarySHA256 == "" {
		t.Fatalf("unexpected native identity: %#v", info)
	}
	if err := db.RequireCapability("storage_conditional_batch_v1"); ErrorCodeOf(err) != CodeCapabilityUnavailable {
		t.Fatalf("conditional gate error = %v", err)
	}
	db.nativeInfo.Features = []string{"native_error_codes_v1"}
	if err := db.RequireStableNativeErrorCodes(); err != nil {
		t.Fatalf("self-attested native error-code feature was rejected: %v", err)
	}
	db.nativeInfo.Capabilities = []NativeCapability{{Name: "example", Version: 1, Status: "gated"}}
	copy := db.NativeInfo()
	copy.Features[0] = "tampered"
	copy.Capabilities[0].Name = "tampered"
	if db.nativeInfo.Features[0] != "native_error_codes_v1" || db.nativeInfo.Capabilities[0].Name != "example" {
		t.Fatal("NativeInfo returned mutable internal slices")
	}
}

func TestCoreBuildIdentityCrossChecksSignedManifest(t *testing.T) {
	verified, err := verifyNativeBundle(testNativePolicy(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = removeVerifiedNative(verified) })
	dirty := false
	build := coreBuildManifest{
		ManifestVersion: 1,
		CoreSemver:      verified.Manifest.Source.CargoVersion,
		GitCommit:       verified.Manifest.Source.Commit,
		GitDirty:        &dirty,
		Target:          verified.Manifest.Build.Target,
		CargoLockSHA256: verified.Manifest.Source.CargoLockSHA256,
		HeaderSHA256:    verified.Manifest.ABI.HeaderSHA256,
		ABI: coreBuildABI{
			Profile:         verified.Manifest.ABI.Profile,
			Version:         verified.Manifest.ABI.Version,
			RequiredSymbols: append([]string(nil), sdkRequiredSymbols...),
		},
		Features:     []string{"native_build_manifest_v1", "native_error_codes_v1", "sql_tlv_v1", "native_conditional_transaction_v2", "conditional_transaction_command_digest_v1"},
		Capabilities: []NativeCapability{{Name: "native_conditional_transaction_v2", Version: 2, Status: "available"}},
	}
	build.BuildBindingSHA256 = computeBuildBinding(build)
	data, err := json.Marshal(build)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifyCoreBuildIdentity(data, verified); err != nil {
		t.Fatalf("matching Core identity was rejected: %v", err)
	}

	reason := "not available"
	build.Features = append(build.Features, "storage_conditional_point_read_v1")
	build.BuildBindingSHA256 = computeBuildBinding(build)
	data, _ = json.Marshal(build)
	if _, err := verifyCoreBuildIdentity(data, verified); err == nil {
		t.Fatal("point-read feature without matching capability was accepted")
	}
	build.Capabilities = append(build.Capabilities, NativeCapability{Name: "storage_conditional_point_read", Version: 1, Status: "gated", Reason: &reason})
	build.BuildBindingSHA256 = computeBuildBinding(build)
	data, _ = json.Marshal(build)
	if _, err := verifyCoreBuildIdentity(data, verified); err != nil {
		t.Fatalf("matching gated point-read identity was rejected: %v", err)
	}
	build.Features = build.Features[:len(build.Features)-1]
	build.BuildBindingSHA256 = computeBuildBinding(build)
	data, _ = json.Marshal(build)
	if _, err := verifyCoreBuildIdentity(data, verified); err == nil {
		t.Fatal("point-read capability without matching feature was accepted")
	}
	build.Capabilities = build.Capabilities[:1]

	build.Capabilities[0].Status = "gated"
	build.Capabilities[0].Reason = &reason
	build.BuildBindingSHA256 = computeBuildBinding(build)
	data, _ = json.Marshal(build)
	if _, err := verifyCoreBuildIdentity(data, verified); err == nil {
		t.Fatal("gated conditional transaction v2 capability was accepted")
	}

	build.Capabilities[0].Status = "available"
	build.Capabilities[0].Reason = nil
	build.GitCommit = "3333333333333333333333333333333333333333"
	build.BuildBindingSHA256 = computeBuildBinding(build)
	data, _ = json.Marshal(build)
	if _, err := verifyCoreBuildIdentity(data, verified); err == nil {
		t.Fatal("Core identity differing from the signed manifest was accepted")
	}
}

func TestNativeArchiveRejectsTraversalLinksAndDuplicates(t *testing.T) {
	platform, err := currentNativePlatform()
	if err != nil {
		t.Fatal(err)
	}
	baseManifest := nativeManifest{Artifact: nativeArtifact{Files: []fileRecord{
		{Path: platform.DynamicLibrary, Size: 1, SHA256: sha256Hex([]byte("x"))},
	}}}
	tests := []struct {
		name    string
		headers []tar.Header
		bodies  [][]byte
	}{
		{name: "traversal", headers: []tar.Header{{Name: "../" + platform.DynamicLibrary, Typeflag: tar.TypeReg, Size: 1}}, bodies: [][]byte{[]byte("x")}},
		{name: "absolute", headers: []tar.Header{{Name: "/" + platform.DynamicLibrary, Typeflag: tar.TypeReg, Size: 1}}, bodies: [][]byte{[]byte("x")}},
		{name: "symlink", headers: []tar.Header{{Name: platform.DynamicLibrary, Typeflag: tar.TypeSymlink, Linkname: "/tmp/evil"}}, bodies: [][]byte{nil}},
		{name: "duplicate", headers: []tar.Header{{Name: platform.DynamicLibrary, Typeflag: tar.TypeReg, Size: 1}, {Name: platform.DynamicLibrary, Typeflag: tar.TypeReg, Size: 1}}, bodies: [][]byte{[]byte("x"), []byte("x")}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			archivePath := filepath.Join(t.TempDir(), "malicious.tar.gz")
			writeTarFixture(t, archivePath, test.headers, test.bodies)
			archiveBytes, err := os.ReadFile(archivePath)
			if err != nil {
				t.Fatal(err)
			}
			manifest := baseManifest
			manifest.Artifact.Archive = fileRecord{Path: filepath.Base(archivePath), Size: int64(len(archiveBytes)), SHA256: sha256Hex(archiveBytes)}
			if _, _, err := verifyNativeArchive(archivePath, manifest, platform); err == nil {
				t.Fatal("unsafe archive was accepted")
			}
		})
	}
}

func writeTarFixture(t *testing.T, path string, headers []tar.Header, bodies [][]byte) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	archive := tar.NewWriter(gz)
	for index := range headers {
		header := headers[index]
		if err := archive.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		if len(bodies[index]) > 0 {
			if _, err := archive.Write(bodies[index]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func sha256Hex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
