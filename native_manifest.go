/*
 * Copyright (c) 2026 Talon Contributors
 * Author: dark.lijin@gmail.com
 * Licensed under the Talon Community Dual License Agreement.
 * See the LICENSE file in the project root for full license information.
 */

package talon

import (
	"archive/tar"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	serverprotocol "github.com/darkmice/talon-sdk-go/internal/serverprotocol"
)

const (
	nativeManifestContract = "talon-native-manifest-v1"
	nativeReleaseChannel   = "enterprise-core"
	maxManifestBytes       = 1 << 20
	maxPublicKeyBytes      = 64 << 10
	maxNativeArchiveBytes  = int64(2 << 30)
	maxNativeMemberBytes   = int64(1 << 30)
)

var (
	commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
	shaPattern    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	tagPattern    = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)
	semverPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$`)
	namePattern   = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	symbolPattern = regexp.MustCompile(`^talon_[a-z0-9_]+$`)
)

// NativePolicy is trusted, out-of-band configuration. None of these identity
// values are learned from a tag, bundle URL, or the manifest being verified.
type NativePolicy struct {
	BundleDir              string
	PublicKeyPEM           []byte
	ExpectedKeyID          string
	ExpectedKeySHA256      string
	ExpectedReleaseTag     string
	ExpectedTalonBinCommit string
	ExpectedCoreRepository string
	ExpectedCoreTag        string
	ExpectedCoreCommit     string
	ExpectedCoreVersion    string
	ExpectedABIProfile     string
	ExpectedABIVersion     int
	ExpectedHeaderSHA256   string
	RequiredCapabilities   []string
}

// NativeInfo is the signed native identity accepted for the current process.
type NativeInfo struct {
	Platform       string
	ReleaseTag     string
	TalonBinCommit string
	CoreRepository string
	CoreTag        string
	CoreCommit     string
	CoreVersion    string
	ABIProfile     string
	ABIVersion     int
	HeaderSHA256   string
	LibrarySHA256  string
	KeyID          string
	KeySHA256      string
	Features       []string
	Capabilities   []NativeCapability
	Gates          map[string]CapabilityGate
}

// NativeCapability is reported by the loaded Core build manifest.
type NativeCapability struct {
	Name    string                  `json:"name"`
	Version int                     `json:"version"`
	Status  string                  `json:"status"`
	Reason  *string                 `json:"reason,omitempty"`
	Limits  *NativeCapabilityLimits `json:"limits,omitempty"`
}

// NativeCapabilityLimits is the artifact-bound limit record introduced by
// the Core build manifest v2. It is retained even for capabilities the SDK
// does not expose so the runtime build binding can be verified exactly.
type NativeCapabilityLimits struct {
	MaxConditions             uint64   `json:"max_conditions"`
	MaxMutations              uint64   `json:"max_mutations"`
	MaxKeyBytes               uint64   `json:"max_key_bytes"`
	MaxReceiptBytes           uint64   `json:"max_receipt_bytes"`
	ServerHTTPMaxRequestBytes uint64   `json:"server_http_max_request_bytes"`
	AllowedConditionOperators []string `json:"allowed_condition_operators"`
	ReceiptAuthentication     string   `json:"receipt_authentication"`
}

// CapabilityGate is copied from the signed feature set.
type CapabilityGate struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type nativeManifest struct {
	SchemaVersion string              `json:"schema_version"`
	Release       nativeRelease       `json:"release"`
	Source        nativeSource        `json:"source"`
	ABI           nativeABI           `json:"abi"`
	Build         nativeBuild         `json:"build"`
	Artifact      nativeArtifact      `json:"artifact"`
	Materials     nativeMaterials     `json:"materials"`
	Signing       nativeSigning       `json:"signing"`
	Compatibility nativeCompatibility `json:"compatibility"`
	Gates         nativeGates         `json:"gates"`
}

type nativeRelease struct {
	Tag            string `json:"tag"`
	Channel        string `json:"channel"`
	TalonBinCommit string `json:"talon_bin_commit"`
}

type nativeSource struct {
	Repository      string `json:"repository"`
	Tag             string `json:"tag"`
	Commit          string `json:"commit"`
	CargoVersion    string `json:"cargo_version"`
	CargoLockSHA256 string `json:"cargo_lock_sha256"`
	SourceDateEpoch int64  `json:"source_date_epoch"`
	Clean           bool   `json:"clean"`
}

type nativeABI struct {
	Profile         string   `json:"profile"`
	Version         int      `json:"version"`
	HeaderSHA256    string   `json:"header_sha256"`
	RequiredSymbols []string `json:"required_symbols"`
}

type nativeBuild struct {
	Source          string   `json:"source"`
	WorkflowRunID   string   `json:"workflow_run_id"`
	Runner          string   `json:"runner"`
	Target          string   `json:"target"`
	Rustc           string   `json:"rustc"`
	Cargo           string   `json:"cargo"`
	Command         []string `json:"command"`
	Reproducibility string   `json:"reproducibility"`
}

type fileRecord struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type nativeArtifact struct {
	Kind     string       `json:"kind"`
	Platform string       `json:"platform"`
	Archive  fileRecord   `json:"archive"`
	Files    []fileRecord `json:"files"`
}

type nativeMaterials struct {
	SBOM             fileRecord `json:"sbom"`
	LicenseInventory fileRecord `json:"license_inventory"`
	CoreLicense      fileRecord `json:"core_license"`
	Notice           fileRecord `json:"notice"`
}

type nativeSigning struct {
	Algorithm       string `json:"algorithm"`
	KeyID           string `json:"key_id"`
	PublicKeySHA256 string `json:"public_key_sha256"`
	Signature       string `json:"signature"`
}

type nativeCompatibility struct {
	SDKContract           string `json:"sdk_contract"`
	RuntimeVerification   string `json:"runtime_verification"`
	BinarySelfAttestation bool   `json:"binary_self_attestation"`
}

type coreBuildManifest struct {
	ManifestVersion    int                `json:"manifest_version"`
	CoreSemver         string             `json:"core_semver"`
	GitCommit          string             `json:"git_commit"`
	GitDirty           *bool              `json:"git_dirty"`
	Target             string             `json:"target"`
	CargoLockSHA256    string             `json:"cargo_lock_sha256"`
	HeaderSHA256       string             `json:"header_sha256"`
	ABI                coreBuildABI       `json:"abi"`
	Features           []string           `json:"features"`
	Capabilities       []NativeCapability `json:"capabilities"`
	BuildBindingSHA256 string             `json:"build_binding_sha256"`
}

type coreBuildABI struct {
	Profile         string   `json:"profile"`
	Version         int      `json:"version"`
	RequiredSymbols []string `json:"required_symbols"`
}

type nativeGates struct {
	StorageConditionalBatchV1 CapabilityGate `json:"storage_conditional_batch_v1"`
}

type nativePlatform struct {
	Name           string
	Target         string
	Runner         string
	StaticLibrary  string
	DynamicLibrary string
}

var sdkRequiredSymbols = []string{
	"talon_open",
	"talon_close",
	"talon_run_sql",
	"talon_run_sql_bin",
	"talon_run_sql_param_bin",
	"talon_kv_set",
	"talon_kv_get",
	"talon_kv_del",
	"talon_kv_incrby",
	"talon_kv_setnx",
	"talon_vector_insert",
	"talon_vector_search",
	"talon_vector_search_bin",
	"talon_start_server",
	"talon_stop_server",
	"talon_persist",
	"talon_free_string",
	"talon_free_bytes",
	"talon_last_error",
	"talon_last_error_code",
	"talon_clear_last_error",
	"talon_build_manifest",
	"talon_execute",
}

type verifiedNative struct {
	Info                 NativeInfo
	Manifest             nativeManifest
	LibraryPath          string
	tempDir              string
	policyHash           [32]byte
	requiredCapabilities []string
}

// NativePolicyFromEnvironment reads the fail-closed default Open policy.
// The public key remains out of band: only its file path is carried in env.
func NativePolicyFromEnvironment() (NativePolicy, error) {
	get := func(name string) (string, error) {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			return "", fmt.Errorf("%s is required", name)
		}
		return value, nil
	}
	bundleDir, err := get("TALON_NATIVE_BUNDLE_DIR")
	if err != nil {
		return NativePolicy{}, err
	}
	keyPath, err := get("TALON_NATIVE_PUBLIC_KEY_FILE")
	if err != nil {
		return NativePolicy{}, err
	}
	publicKey, err := readBoundedRegularFile(keyPath, maxPublicKeyBytes)
	if err != nil {
		return NativePolicy{}, fmt.Errorf("TALON_NATIVE_PUBLIC_KEY_FILE is not a bounded regular file: %w", err)
	}
	fields := make(map[string]string)
	for _, name := range []string{
		"TALON_NATIVE_EXPECTED_KEY_ID",
		"TALON_NATIVE_EXPECTED_KEY_SHA256",
		"TALON_NATIVE_EXPECTED_RELEASE_TAG",
		"TALON_NATIVE_EXPECTED_TALON_BIN_COMMIT",
		"TALON_NATIVE_EXPECTED_CORE_REPOSITORY",
		"TALON_NATIVE_EXPECTED_CORE_TAG",
		"TALON_NATIVE_EXPECTED_CORE_COMMIT",
		"TALON_NATIVE_EXPECTED_CORE_VERSION",
		"TALON_NATIVE_EXPECTED_ABI_PROFILE",
		"TALON_NATIVE_EXPECTED_HEADER_SHA256",
	} {
		fields[name], err = get(name)
		if err != nil {
			return NativePolicy{}, err
		}
	}
	abiVersionText, err := get("TALON_NATIVE_EXPECTED_ABI_VERSION")
	if err != nil {
		return NativePolicy{}, err
	}
	var abiVersion int
	if _, err := fmt.Sscanf(abiVersionText, "%d", &abiVersion); err != nil || fmt.Sprintf("%d", abiVersion) != abiVersionText {
		return NativePolicy{}, fmt.Errorf("TALON_NATIVE_EXPECTED_ABI_VERSION must be a canonical positive integer")
	}
	capabilities := []string{}
	if raw := strings.TrimSpace(os.Getenv("TALON_NATIVE_REQUIRED_CAPABILITIES")); raw != "" {
		for _, value := range strings.Split(raw, ",") {
			value = strings.TrimSpace(value)
			if value == "" {
				return NativePolicy{}, fmt.Errorf("TALON_NATIVE_REQUIRED_CAPABILITIES contains an empty capability")
			}
			capabilities = append(capabilities, value)
		}
	}
	return NativePolicy{
		BundleDir:              bundleDir,
		PublicKeyPEM:           publicKey,
		ExpectedKeyID:          fields["TALON_NATIVE_EXPECTED_KEY_ID"],
		ExpectedKeySHA256:      fields["TALON_NATIVE_EXPECTED_KEY_SHA256"],
		ExpectedReleaseTag:     fields["TALON_NATIVE_EXPECTED_RELEASE_TAG"],
		ExpectedTalonBinCommit: fields["TALON_NATIVE_EXPECTED_TALON_BIN_COMMIT"],
		ExpectedCoreRepository: fields["TALON_NATIVE_EXPECTED_CORE_REPOSITORY"],
		ExpectedCoreTag:        fields["TALON_NATIVE_EXPECTED_CORE_TAG"],
		ExpectedCoreCommit:     fields["TALON_NATIVE_EXPECTED_CORE_COMMIT"],
		ExpectedCoreVersion:    fields["TALON_NATIVE_EXPECTED_CORE_VERSION"],
		ExpectedABIProfile:     fields["TALON_NATIVE_EXPECTED_ABI_PROFILE"],
		ExpectedABIVersion:     abiVersion,
		ExpectedHeaderSHA256:   fields["TALON_NATIVE_EXPECTED_HEADER_SHA256"],
		RequiredCapabilities:   capabilities,
	}, nil
}

func currentNativePlatform() (nativePlatform, error) {
	return nativePlatformFor(runtime.GOOS, runtime.GOARCH)
}

func nativePlatformFor(goos, goarch string) (nativePlatform, error) {
	switch goos + "/" + goarch {
	case "darwin/amd64":
		return nativePlatform{"macos-amd64", "x86_64-apple-darwin", "macos-15-intel", "libtalon.a", "libtalon.dylib"}, nil
	case "darwin/arm64":
		return nativePlatform{"macos-arm64", "aarch64-apple-darwin", "macos-15", "libtalon.a", "libtalon.dylib"}, nil
	case "linux/amd64":
		return nativePlatform{"linux-amd64", "x86_64-unknown-linux-gnu", "ubuntu-24.04", "libtalon.a", "libtalon.so"}, nil
	case "linux/arm64":
		return nativePlatform{"linux-arm64", "aarch64-unknown-linux-gnu", "ubuntu-24.04", "libtalon.a", "libtalon.so"}, nil
	default:
		return nativePlatform{}, fmt.Errorf("unsupported SDK platform %s/%s", goos, goarch)
	}
}

func verifyNativeBundle(policy NativePolicy) (_ *verifiedNative, err error) {
	platform, err := currentNativePlatform()
	if err != nil {
		return nil, err
	}
	if err := validateNativePolicy(policy); err != nil {
		return nil, err
	}
	bundleDir, err := filepath.Abs(policy.BundleDir)
	if err != nil {
		return nil, fmt.Errorf("resolve native bundle directory: %w", err)
	}
	info, err := os.Lstat(bundleDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("native bundle directory is unavailable")
	}
	manifestName := "libtalon-core-" + platform.Name + ".manifest.json"
	manifestPath := filepath.Join(bundleDir, manifestName)
	manifestBytes, err := readBoundedRegularFile(manifestPath, maxManifestBytes)
	if err != nil {
		return nil, fmt.Errorf("read native manifest: %w", err)
	}
	var manifest nativeManifest
	if err := decodeStrictJSON(manifestBytes, &manifest); err != nil {
		return nil, fmt.Errorf("strict native manifest decode: %w", err)
	}
	if err := validateNativeManifest(manifest, manifestName, platform, policy); err != nil {
		return nil, err
	}
	publicKey, fingerprint, err := parseTrustedEd25519Key(policy.PublicKeyPEM)
	if err != nil {
		return nil, err
	}
	if fingerprint != policy.ExpectedKeySHA256 || fingerprint != manifest.Signing.PublicKeySHA256 {
		return nil, fmt.Errorf("trusted public-key fingerprint does not match policy and manifest")
	}
	signaturePath := filepath.Join(bundleDir, manifest.Signing.Signature)
	signature, err := readBoundedRegularFile(signaturePath, ed25519.SignatureSize)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return nil, fmt.Errorf("read Ed25519 manifest signature: invalid signature file")
	}
	if !ed25519.Verify(publicKey, manifestBytes, signature) {
		return nil, fmt.Errorf("Ed25519 manifest signature verification failed")
	}

	archivePath := filepath.Join(bundleDir, manifest.Artifact.Archive.Path)
	libraryBytes, libraryRecord, err := verifyNativeArchive(archivePath, manifest, platform)
	if err != nil {
		return nil, err
	}
	for label, record := range map[string]fileRecord{
		"sbom":              manifest.Materials.SBOM,
		"license inventory": manifest.Materials.LicenseInventory,
		"core license":      manifest.Materials.CoreLicense,
		"notice":            manifest.Materials.Notice,
	} {
		if err := verifyFileRecord(filepath.Join(bundleDir, record.Path), record, maxNativeMemberBytes); err != nil {
			return nil, fmt.Errorf("verify %s: %w", label, err)
		}
	}

	tempDir, err := os.MkdirTemp("", "talon-native-verified-")
	if err != nil {
		return nil, fmt.Errorf("create verified native directory: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(tempDir)
		}
	}()
	libraryPath := filepath.Join(tempDir, platform.DynamicLibrary)
	file, err := os.OpenFile(libraryPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o500)
	if err != nil {
		return nil, fmt.Errorf("create verified native library: %w", err)
	}
	if _, err = file.Write(libraryBytes); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return nil, fmt.Errorf("write verified native library: %w", err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close verified native library: %w", closeErr)
	}

	policyHash := hashNativePolicy(policy)
	return &verifiedNative{
		LibraryPath:          libraryPath,
		tempDir:              tempDir,
		policyHash:           policyHash,
		Manifest:             manifest,
		requiredCapabilities: append([]string(nil), policy.RequiredCapabilities...),
		Info: NativeInfo{
			Platform:       platform.Name,
			ReleaseTag:     manifest.Release.Tag,
			TalonBinCommit: manifest.Release.TalonBinCommit,
			CoreRepository: manifest.Source.Repository,
			CoreTag:        manifest.Source.Tag,
			CoreCommit:     manifest.Source.Commit,
			CoreVersion:    manifest.Source.CargoVersion,
			ABIProfile:     manifest.ABI.Profile,
			ABIVersion:     manifest.ABI.Version,
			HeaderSHA256:   manifest.ABI.HeaderSHA256,
			LibrarySHA256:  libraryRecord.SHA256,
			KeyID:          manifest.Signing.KeyID,
			KeySHA256:      manifest.Signing.PublicKeySHA256,
			Gates: map[string]CapabilityGate{
				"storage_conditional_batch_v1": manifest.Gates.StorageConditionalBatchV1,
			},
		},
	}, nil
}

func validateNativePolicy(policy NativePolicy) error {
	if strings.TrimSpace(policy.BundleDir) == "" || len(policy.PublicKeyPEM) == 0 || len(policy.PublicKeyPEM) > maxPublicKeyBytes {
		return fmt.Errorf("native bundle directory and bounded out-of-band public key are required")
	}
	if !namePattern.MatchString(policy.ExpectedKeyID) || !shaPattern.MatchString(policy.ExpectedKeySHA256) {
		return fmt.Errorf("expected signing key identity is invalid")
	}
	if !tagPattern.MatchString(policy.ExpectedReleaseTag) || !commitPattern.MatchString(policy.ExpectedTalonBinCommit) {
		return fmt.Errorf("expected release identity is invalid")
	}
	if !strings.HasPrefix(policy.ExpectedCoreRepository, "https://") || !tagPattern.MatchString(policy.ExpectedCoreTag) || !commitPattern.MatchString(policy.ExpectedCoreCommit) || !semverPattern.MatchString(policy.ExpectedCoreVersion) {
		return fmt.Errorf("expected Core source identity is invalid")
	}
	if policy.ExpectedABIProfile == "" || policy.ExpectedABIVersion <= 0 || !shaPattern.MatchString(policy.ExpectedHeaderSHA256) {
		return fmt.Errorf("expected ABI identity is invalid")
	}
	seen := map[string]struct{}{}
	for _, capability := range policy.RequiredCapabilities {
		if capability != "storage_conditional_batch_v1" && capability != "storage_conditional_point_read" && capability != "storage_conditional_snapshot_read" && capability != "storage_conditional_snapshot_read_v2" && capability != "revision_stream" {
			return fmt.Errorf("unknown required native capability %q", capability)
		}
		if _, exists := seen[capability]; exists {
			return fmt.Errorf("duplicate required native capability %q", capability)
		}
		seen[capability] = struct{}{}
	}
	return nil
}

func validateNativeManifest(manifest nativeManifest, manifestName string, platform nativePlatform, policy NativePolicy) error {
	if manifest.SchemaVersion != "1.0" {
		return fmt.Errorf("unsupported native manifest schema")
	}
	if manifest.Release.Tag != policy.ExpectedReleaseTag || manifest.Release.Channel != nativeReleaseChannel || manifest.Release.TalonBinCommit != policy.ExpectedTalonBinCommit || !commitPattern.MatchString(manifest.Release.TalonBinCommit) {
		return fmt.Errorf("native release identity does not match trusted policy")
	}
	if manifest.Source.Repository != policy.ExpectedCoreRepository || manifest.Source.Tag != policy.ExpectedCoreTag || manifest.Source.Commit != policy.ExpectedCoreCommit || manifest.Source.CargoVersion != policy.ExpectedCoreVersion || !semverPattern.MatchString(manifest.Source.CargoVersion) || !manifest.Source.Clean || manifest.Source.SourceDateEpoch <= 0 || !shaPattern.MatchString(manifest.Source.CargoLockSHA256) {
		return fmt.Errorf("Core source identity does not match trusted policy")
	}
	if manifest.ABI.Profile != policy.ExpectedABIProfile || manifest.ABI.Version != policy.ExpectedABIVersion || manifest.ABI.HeaderSHA256 != policy.ExpectedHeaderSHA256 {
		return fmt.Errorf("Core ABI identity does not match trusted policy")
	}
	if !sameStringSet(manifest.ABI.RequiredSymbols, sdkRequiredSymbols) {
		return fmt.Errorf("Core ABI required symbol set does not match this SDK")
	}
	if manifest.Build.Target != platform.Target || manifest.Build.Runner != platform.Runner || manifest.Build.Source == "" || manifest.Build.WorkflowRunID == "" || manifest.Build.Rustc == "" || manifest.Build.Cargo == "" || manifest.Build.Reproducibility == "" || !sameStrings(manifest.Build.Command, []string{"build", "--locked", "--release", "--lib"}) {
		return fmt.Errorf("native build identity does not match platform contract")
	}
	if manifest.Artifact.Kind != "talon-core-native-library" || manifest.Artifact.Platform != platform.Name {
		return fmt.Errorf("native artifact kind or platform mismatch")
	}
	expectedArchive := "libtalon-core-" + platform.Name + ".tar.gz"
	if err := validateFileRecord(manifest.Artifact.Archive, expectedArchive); err != nil {
		return fmt.Errorf("invalid native archive record: %w", err)
	}
	if len(manifest.Artifact.Files) != 5 {
		return fmt.Errorf("native archive file set is incomplete")
	}
	expectedFiles := map[string]struct{}{platform.StaticLibrary: {}, platform.DynamicLibrary: {}, "talon.h": {}, "LICENSE.core": {}, "NOTICE": {}}
	files := make(map[string]fileRecord, len(manifest.Artifact.Files))
	for _, record := range manifest.Artifact.Files {
		if err := validateFileRecord(record, ""); err != nil {
			return fmt.Errorf("invalid native member record: %w", err)
		}
		if _, exists := files[record.Path]; exists {
			return fmt.Errorf("duplicate native member %q", record.Path)
		}
		if _, expected := expectedFiles[record.Path]; !expected {
			return fmt.Errorf("unexpected native member %q", record.Path)
		}
		files[record.Path] = record
	}
	if len(files) != len(expectedFiles) || files["talon.h"].SHA256 != manifest.ABI.HeaderSHA256 {
		return fmt.Errorf("native member set or header identity mismatch")
	}
	materialExpected := map[string]string{
		"sbom":              "libtalon-core-" + platform.Name + ".sbom.cdx.json",
		"license_inventory": "libtalon-core-" + platform.Name + ".licenses.json",
		"core_license":      "LICENSE.core",
		"notice":            "NOTICE",
	}
	materials := map[string]fileRecord{"sbom": manifest.Materials.SBOM, "license_inventory": manifest.Materials.LicenseInventory, "core_license": manifest.Materials.CoreLicense, "notice": manifest.Materials.Notice}
	for name, record := range materials {
		if err := validateFileRecord(record, materialExpected[name]); err != nil {
			return fmt.Errorf("invalid %s record: %w", name, err)
		}
	}
	if files["LICENSE.core"] != manifest.Materials.CoreLicense || files["NOTICE"] != manifest.Materials.Notice {
		return fmt.Errorf("inner and outer legal material records differ")
	}
	if manifest.Signing.Algorithm != "Ed25519" || manifest.Signing.KeyID != policy.ExpectedKeyID || manifest.Signing.PublicKeySHA256 != policy.ExpectedKeySHA256 || manifest.Signing.Signature != manifestName+".sig" || !namePattern.MatchString(manifest.Signing.Signature) {
		return fmt.Errorf("manifest signing identity does not match trusted policy")
	}
	if manifest.Compatibility.SDKContract != nativeManifestContract || manifest.Compatibility.RuntimeVerification != "ed25519-manifest+sha256-native+abi-identity" || !manifest.Compatibility.BinarySelfAttestation {
		return fmt.Errorf("native compatibility contract mismatch")
	}
	gate := manifest.Gates.StorageConditionalBatchV1
	if (gate.Status != "gated" && gate.Status != "available") || strings.TrimSpace(gate.Reason) == "" {
		return fmt.Errorf("invalid storage_conditional_batch_v1 gate")
	}
	for _, capability := range policy.RequiredCapabilities {
		if capability == "storage_conditional_batch_v1" {
			if gate.Status != "available" {
				return fmt.Errorf("required capability %s is gated: %s", capability, gate.Reason)
			}
			// Even an "available" manifest cannot silently enable an ABI that this
			// SDK version has not implemented.
			return fmt.Errorf("required capability %s is not implemented by this SDK", capability)
		}
		// revision_stream is checked against the loaded Core self-manifest. The
		// external manifest v1 has no generic runtime capability map, so merely
		// accepting the policy name here does not imply availability.
	}
	return nil
}

func validateFileRecord(record fileRecord, expectedName string) error {
	if record.Path == "" || record.Path != filepath.Base(record.Path) || !namePattern.MatchString(record.Path) || record.Size <= 0 || !shaPattern.MatchString(record.SHA256) {
		return fmt.Errorf("unsafe path, size, or SHA-256")
	}
	if expectedName != "" && record.Path != expectedName {
		return fmt.Errorf("path %q does not match %q", record.Path, expectedName)
	}
	return nil
}

func verifyNativeArchive(path string, manifest nativeManifest, platform nativePlatform) ([]byte, fileRecord, error) {
	file, err := openBoundedRegularFile(path, manifest.Artifact.Archive.Size, maxNativeArchiveBytes)
	if err != nil {
		return nil, fileRecord{}, fmt.Errorf("open native archive: %w", err)
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, io.LimitReader(file, maxNativeArchiveBytes+1)); err != nil {
		return nil, fileRecord{}, fmt.Errorf("hash native archive: %w", err)
	}
	if hex.EncodeToString(digest.Sum(nil)) != manifest.Artifact.Archive.SHA256 {
		return nil, fileRecord{}, fmt.Errorf("native archive SHA-256 mismatch")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, fileRecord{}, fmt.Errorf("rewind native archive: %w", err)
	}
	gz, err := gzip.NewReader(file)
	if err != nil {
		return nil, fileRecord{}, fmt.Errorf("open native gzip stream: %w", err)
	}
	defer gz.Close()
	archive := tar.NewReader(gz)
	expected := make(map[string]fileRecord, len(manifest.Artifact.Files))
	for _, record := range manifest.Artifact.Files {
		expected[record.Path] = record
	}
	seen := make(map[string]struct{}, len(expected))
	var library []byte
	var libraryRecord fileRecord
	for {
		header, nextErr := archive.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return nil, fileRecord{}, fmt.Errorf("read native archive: %w", nextErr)
		}
		if header.Name == "" || header.Name != filepath.Base(header.Name) || !namePattern.MatchString(header.Name) || (header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA) {
			return nil, fileRecord{}, fmt.Errorf("unsafe or unsupported archive member %q", header.Name)
		}
		record, ok := expected[header.Name]
		if !ok {
			return nil, fileRecord{}, fmt.Errorf("unexpected archive member %q", header.Name)
		}
		if _, duplicate := seen[header.Name]; duplicate {
			return nil, fileRecord{}, fmt.Errorf("duplicate archive member %q", header.Name)
		}
		if header.Size != record.Size || header.Size > maxNativeMemberBytes {
			return nil, fileRecord{}, fmt.Errorf("archive member %q size mismatch", header.Name)
		}
		data, readErr := io.ReadAll(io.LimitReader(archive, header.Size+1))
		digest := hashBytes(data)
		if readErr != nil || int64(len(data)) != header.Size || hex.EncodeToString(digest[:]) != record.SHA256 {
			return nil, fileRecord{}, fmt.Errorf("archive member %q digest mismatch", header.Name)
		}
		seen[header.Name] = struct{}{}
		if header.Name == platform.DynamicLibrary {
			library = data
			libraryRecord = record
		}
	}
	if len(seen) != len(expected) || len(library) == 0 {
		return nil, fileRecord{}, fmt.Errorf("native archive member set is incomplete")
	}
	return library, libraryRecord, nil
}

func verifyFileRecord(path string, record fileRecord, maxSize int64) error {
	file, err := openBoundedRegularFile(path, record.Size, maxSize)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, io.LimitReader(file, maxSize+1)); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != record.SHA256 {
		return fmt.Errorf("SHA-256 mismatch")
	}
	return nil
}

func readBoundedRegularFile(path string, max int64) ([]byte, error) {
	file, err := openBoundedRegularFile(path, -1, max)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	value, err := io.ReadAll(io.LimitReader(file, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(value)) > max {
		return nil, fmt.Errorf("file exceeds size bound")
	}
	return value, nil
}

func openBoundedRegularFile(path string, expectedSize, maxSize int64) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > maxSize || (expectedSize >= 0 && before.Size() != expectedSize) {
		return nil, fmt.Errorf("file is not a bounded regular file of the expected size")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) || after.Size() != before.Size() {
		_ = file.Close()
		return nil, fmt.Errorf("file changed while being opened")
	}
	return file, nil
}

func parseTrustedEd25519Key(value []byte) (ed25519.PublicKey, string, error) {
	block, rest := pem.Decode(value)
	if block == nil || block.Type != "PUBLIC KEY" || strings.TrimSpace(string(rest)) != "" {
		return nil, "", fmt.Errorf("trusted public key must contain exactly one PUBLIC KEY PEM block")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, "", fmt.Errorf("parse trusted public key: %w", err)
	}
	publicKey, ok := parsed.(ed25519.PublicKey)
	if !ok || len(publicKey) != ed25519.PublicKeySize {
		return nil, "", fmt.Errorf("trusted public key is not Ed25519")
	}
	hash := sha256.Sum256(block.Bytes)
	return publicKey, hex.EncodeToString(hash[:]), nil
}

func decodeStrictJSON(data []byte, destination interface{}) error {
	return serverprotocol.DecodeStrictJSON(data, destination)
}

func sameStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	leftCopy := append([]string(nil), left...)
	rightCopy := append([]string(nil), right...)
	sort.Strings(leftCopy)
	sort.Strings(rightCopy)
	for index := range leftCopy {
		if leftCopy[index] != rightCopy[index] || !symbolPattern.MatchString(leftCopy[index]) {
			return false
		}
		if index > 0 && leftCopy[index] == leftCopy[index-1] {
			return false
		}
	}
	return true
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func verifyCoreBuildIdentity(data []byte, verified *verifiedNative) (coreBuildManifest, error) {
	var build coreBuildManifest
	if len(data) == 0 || len(data) > maxManifestBytes {
		return build, fmt.Errorf("Core build manifest exceeds the SDK bound")
	}
	if err := decodeStrictJSON(data, &build); err != nil {
		return build, fmt.Errorf("strict Core build manifest decode: %w", err)
	}
	external := verified.Manifest
	if (build.ManifestVersion != 1 && build.ManifestVersion != 2) || build.CoreSemver != external.Source.CargoVersion || build.GitCommit != external.Source.Commit || build.GitDirty == nil || *build.GitDirty || build.Target != external.Build.Target || build.CargoLockSHA256 != external.Source.CargoLockSHA256 || build.HeaderSHA256 != external.ABI.HeaderSHA256 {
		return build, fmt.Errorf("Core source/build identity differs from signed artifact metadata")
	}
	if build.ABI.Profile != external.ABI.Profile || build.ABI.Version != external.ABI.Version || !sameStringSet(build.ABI.RequiredSymbols, external.ABI.RequiredSymbols) || !sameStringSet(build.ABI.RequiredSymbols, sdkRequiredSymbols) {
		return build, fmt.Errorf("Core runtime ABI differs from signed artifact metadata")
	}
	seenFeatures := map[string]struct{}{}
	for _, feature := range build.Features {
		if feature == "" {
			return build, fmt.Errorf("Core build manifest contains an empty feature")
		}
		if _, duplicate := seenFeatures[feature]; duplicate {
			return build, fmt.Errorf("Core build manifest contains duplicate feature %q", feature)
		}
		seenFeatures[feature] = struct{}{}
	}
	manifestFeature := fmt.Sprintf("native_build_manifest_v%d", build.ManifestVersion)
	for _, required := range []string{manifestFeature, "native_error_codes_v1", "sql_tlv_v1", "native_conditional_transaction_v2", "conditional_transaction_command_digest_v1"} {
		if _, ok := seenFeatures[required]; !ok {
			return build, fmt.Errorf("Core build manifest omitted required feature %q", required)
		}
	}
	capabilities := map[string]NativeCapability{}
	capabilityNames := map[string]int{}
	for _, capability := range build.Capabilities {
		if capability.Name == "" || capability.Version <= 0 || (capability.Status != "available" && capability.Status != "gated") {
			return build, fmt.Errorf("Core build manifest contains an invalid capability")
		}
		if capability.Status == "gated" && (capability.Reason == nil || strings.TrimSpace(*capability.Reason) == "") {
			return build, fmt.Errorf("gated Core capability %q omitted its reason", capability.Name)
		}
		if capability.Limits != nil && build.ManifestVersion < 2 {
			return build, fmt.Errorf("Core build manifest v1 capability %q unexpectedly contains limits", capability.Name)
		}
		identity := fmt.Sprintf("%s@%d", capability.Name, capability.Version)
		if _, duplicate := capabilities[identity]; duplicate {
			return build, fmt.Errorf("Core build manifest contains duplicate capability %q version %d", capability.Name, capability.Version)
		}
		capabilities[identity] = capability
		capabilityNames[capability.Name]++
	}
	for name, count := range capabilityNames {
		if count > 1 && name != "storage_conditional_snapshot_read" {
			return build, fmt.Errorf("Core build manifest contains ambiguous versions of capability %q", name)
		}
	}
	conditionalV2, ok := capabilities["native_conditional_transaction_v2@2"]
	if !ok || conditionalV2.Version != 2 || conditionalV2.Status != "available" {
		return build, fmt.Errorf("native_conditional_transaction_v2 is not available to this SDK")
	}
	if revisionStream, ok := singleNativeCapability(build.Capabilities, "revision_stream"); ok {
		if revisionStream.Version != revisionStreamVersion || !containsString(build.Features, "revision_stream_v1") || !containsString(build.Features, "revision_stream_v2_mmr_proof") {
			return build, fmt.Errorf("revision_stream capability does not match the SDK v2 authenticated-proof contract")
		}
	}
	pointRead, hasPointRead := capabilities[fmt.Sprintf("storage_conditional_point_read@%d", conditionalPointReadVersion)]
	pointReadFeature := containsString(build.Features, "storage_conditional_point_read_v1")
	if hasPointRead != pointReadFeature || (hasPointRead && pointRead.Version != conditionalPointReadVersion) {
		return build, fmt.Errorf("storage_conditional_point_read capability does not match the SDK v1 typed contract")
	}
	for _, version := range []int{conditionalSnapshotReadVersion, serverprotocol.ConditionalSnapshotReadVersionV2} {
		_, hasSnapshotRead := capabilities[fmt.Sprintf("storage_conditional_snapshot_read@%d", version)]
		snapshotReadFeature := containsString(build.Features, fmt.Sprintf("storage_conditional_snapshot_read_v%d", version))
		if hasSnapshotRead != snapshotReadFeature {
			return build, fmt.Errorf("storage_conditional_snapshot_read capability v%d does not match its typed feature", version)
		}
	}
	storageV1, ok := capabilities["storage_conditional_batch@1"]
	if external.Gates.StorageConditionalBatchV1.Status == "available" && (!ok || storageV1.Version != 1 || storageV1.Status != "available") {
		return build, fmt.Errorf("signed storage_conditional_batch_v1 gate is not supported by loaded Core")
	}
	if !shaPattern.MatchString(build.BuildBindingSHA256) || computeBuildBinding(build) != build.BuildBindingSHA256 {
		return build, fmt.Errorf("Core build_binding_sha256 is invalid")
	}
	return build, nil
}

func verifyRequiredRuntimeCapabilities(build coreBuildManifest, required []string) error {
	for _, name := range required {
		if name != "revision_stream" && name != "storage_conditional_point_read" && name != "storage_conditional_snapshot_read" && name != "storage_conditional_snapshot_read_v2" {
			continue
		}
		capabilityName, version := name, 0
		if name == "storage_conditional_snapshot_read" {
			version = conditionalSnapshotReadVersion
		} else if name == "storage_conditional_snapshot_read_v2" {
			capabilityName, version = "storage_conditional_snapshot_read", serverprotocol.ConditionalSnapshotReadVersionV2
		} else if name == "storage_conditional_point_read" {
			version = conditionalPointReadVersion
		} else if name == "revision_stream" {
			version = revisionStreamVersion
		}
		found := findNativeCapability(build.Capabilities, capabilityName, version)
		available := false
		if name == "revision_stream" {
			available = found != nil && found.Version == revisionStreamVersion && found.Status == "available" && containsString(build.Features, "revision_stream_v1") && containsString(build.Features, "revision_stream_v2_mmr_proof")
		} else if name == "storage_conditional_point_read" {
			available = found != nil && found.Version == conditionalPointReadVersion && found.Status == "available" && containsString(build.Features, "storage_conditional_point_read_v1")
		} else {
			available = found != nil && found.Status == "available" && containsString(build.Features, fmt.Sprintf("storage_conditional_snapshot_read_v%d", version))
		}
		if !available {
			reason := ""
			if found != nil && found.Reason != nil {
				reason = ": " + *found.Reason
			}
			return fmt.Errorf("required runtime capability %s is not available%s", name, reason)
		}
	}
	return nil
}

func computeBuildBinding(build coreBuildManifest) string {
	hash := sha256.New()
	dirty := "unknown"
	if build.GitDirty != nil {
		dirty = strconv.FormatBool(*build.GitDirty)
	}
	fields := []string{strconv.Itoa(build.ManifestVersion), build.CoreSemver, build.GitCommit, dirty, build.Target, build.CargoLockSHA256, build.HeaderSHA256, build.ABI.Profile, strconv.Itoa(build.ABI.Version)}
	for _, value := range fields {
		bindBuildField(hash, value)
	}
	for _, value := range build.ABI.RequiredSymbols {
		bindBuildField(hash, value)
	}
	for _, value := range build.Features {
		bindBuildField(hash, value)
	}
	for _, capability := range build.Capabilities {
		bindBuildField(hash, capability.Name)
		bindBuildField(hash, strconv.Itoa(capability.Version))
		bindBuildField(hash, capability.Status)
		reason := ""
		if capability.Reason != nil {
			reason = *capability.Reason
		}
		bindBuildField(hash, reason)
		if build.ManifestVersion >= 2 {
			if capability.Limits == nil {
				bindBuildField(hash, "absent")
			} else {
				bindBuildField(hash, "present")
				limits := capability.Limits
				for _, value := range []string{
					strconv.FormatUint(limits.MaxConditions, 10),
					strconv.FormatUint(limits.MaxMutations, 10),
					strconv.FormatUint(limits.MaxKeyBytes, 10),
					strconv.FormatUint(limits.MaxReceiptBytes, 10),
					strconv.FormatUint(limits.ServerHTTPMaxRequestBytes, 10),
					limits.ReceiptAuthentication,
				} {
					bindBuildField(hash, value)
				}
				for _, operator := range limits.AllowedConditionOperators {
					bindBuildField(hash, operator)
				}
			}
		}
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func findNativeCapability(capabilities []NativeCapability, name string, version int) *NativeCapability {
	for index := range capabilities {
		if capabilities[index].Name == name && capabilities[index].Version == version {
			return &capabilities[index]
		}
	}
	return nil
}

func singleNativeCapability(capabilities []NativeCapability, name string) (NativeCapability, bool) {
	for _, capability := range capabilities {
		if capability.Name == name {
			return capability, true
		}
	}
	return NativeCapability{}, false
}

func bindBuildField(hash io.Writer, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = hash.Write(length[:])
	_, _ = hash.Write([]byte(value))
}

func hashNativePolicy(policy NativePolicy) [32]byte {
	hash := sha256.New()
	values := []string{policy.BundleDir, policy.ExpectedKeyID, policy.ExpectedKeySHA256, policy.ExpectedReleaseTag, policy.ExpectedTalonBinCommit, policy.ExpectedCoreRepository, policy.ExpectedCoreTag, policy.ExpectedCoreCommit, policy.ExpectedCoreVersion, policy.ExpectedABIProfile, fmt.Sprintf("%d", policy.ExpectedABIVersion), policy.ExpectedHeaderSHA256}
	values = append(values, policy.RequiredCapabilities...)
	for _, value := range values {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(value))
	}
	_, _ = hash.Write(policy.PublicKeyPEM)
	var result [32]byte
	copy(result[:], hash.Sum(nil))
	return result
}

func hashBytes(value []byte) [32]byte { return sha256.Sum256(value) }
