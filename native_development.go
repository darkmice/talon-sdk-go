package talon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	NativeAdmissionRelease          = "release"
	NativeAdmissionLocalDevelopment = "local-development"
	localDevelopmentContract        = "talon-native-local-development-v1"
)

// LocalDevelopmentPolicy is an explicit, unsigned local-code admission. It is
// never a release trust identity. The caller trusts the selected paths and hashes.
// PrepareLocalDevelopment extracts the self-manifest from the actual library.
type LocalDevelopmentPolicy struct {
	Contract            string `json:"contract"`
	LibraryPath         string `json:"library_path"`
	LibrarySHA256       string `json:"library_sha256"`
	HeaderPath          string `json:"header_path"`
	HeaderSHA256        string `json:"header_sha256"`
	BuildManifestPath   string `json:"build_manifest_path"`
	BuildManifestSHA256 string `json:"build_manifest_sha256"`
	ExpectedABIProfile  string `json:"expected_abi_profile"`
	ExpectedABIVersion  int    `json:"expected_abi_version"`
	// BuildProfile is caller-declared: the current Core ABI does not attest it.
	BuildProfile         string   `json:"build_profile"`
	RequiredCapabilities []string `json:"required_capabilities"` // name@version
}

func canonicalAbsolutePath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && utf8.ValidString(path) && !strings.ContainsRune(path, 0)
}

func validateLocalDevelopmentPolicy(p LocalDevelopmentPolicy) error {
	if p.Contract != localDevelopmentContract {
		return fmt.Errorf("invalid local development contract")
	}
	for label, path := range map[string]string{"library": p.LibraryPath, "header": p.HeaderPath, "self-manifest": p.BuildManifestPath} {
		if !canonicalAbsolutePath(path) {
			return fmt.Errorf("%s path must be canonical absolute UTF-8", label)
		}
	}
	if p.LibraryPath == p.HeaderPath || p.LibraryPath == p.BuildManifestPath || p.HeaderPath == p.BuildManifestPath {
		return fmt.Errorf("local artifact paths must be distinct")
	}
	for _, digest := range []string{p.LibrarySHA256, p.HeaderSHA256, p.BuildManifestSHA256} {
		if !shaPattern.MatchString(digest) {
			return fmt.Errorf("local artifact SHA256 must be pinned")
		}
	}
	if p.ExpectedABIProfile != "talon-native-c" || p.ExpectedABIVersion != 1 {
		return fmt.Errorf("unsupported local development ABI profile/version")
	}
	if p.BuildProfile != "debug" && p.BuildProfile != "release" && p.BuildProfile != "unknown" {
		return fmt.Errorf("declared build profile must be debug, release, or unknown")
	}
	seen := map[string]bool{}
	for _, capability := range p.RequiredCapabilities {
		if _, _, err := parseLocalCapability(capability); err != nil {
			return err
		}
		if seen[capability] {
			return fmt.Errorf("duplicate required local capability %q", capability)
		}
		seen[capability] = true
	}
	return nil
}

func parseLocalCapability(value string) (string, int, error) {
	name, versionText, ok := strings.Cut(value, "@")
	version, err := strconv.Atoi(versionText)
	if !ok || err != nil || version <= 0 || strconv.Itoa(version) != versionText || name == "" {
		return "", 0, fmt.Errorf("required local capability must be name@version: %q", value)
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
			return "", 0, fmt.Errorf("invalid local capability name %q", name)
		}
	}
	// This is an external release gate, not an SDK-implemented operation.
	if name == "storage_conditional_batch" {
		return "", 0, fmt.Errorf("storage_conditional_batch release gate is not implemented by this SDK")
	}
	return name, version, nil
}

func validateLocalDevelopmentBuild(build coreBuildManifest, p LocalDevelopmentPolicy) error {
	platform, err := currentNativePlatform()
	if err != nil {
		return err
	}
	if (build.ManifestVersion != 1 && build.ManifestVersion != 2) || !semverPattern.MatchString(build.CoreSemver) || !commitPattern.MatchString(build.GitCommit) || build.GitDirty == nil || build.Target != platform.Target || !shaPattern.MatchString(build.CargoLockSHA256) || build.HeaderSHA256 != p.HeaderSHA256 {
		return fmt.Errorf("local Core source/target/header identity is invalid")
	}
	if build.ABI.Profile != p.ExpectedABIProfile || build.ABI.Version != p.ExpectedABIVersion || !supportedNativeSymbolSet(build.ABI.RequiredSymbols) {
		return fmt.Errorf("local Core ABI differs from pinned SDK-supported ABI")
	}
	for _, identity := range p.RequiredCapabilities {
		name, version, err := parseLocalCapability(identity)
		if err != nil {
			return err
		}
		capability := findNativeCapability(build.Capabilities, name, version)
		if capability == nil || capability.Status != "available" || !containsString(build.Features, fmt.Sprintf("%s_v%d", name, version)) {
			return fmt.Errorf("required local capability %s is unavailable or missing its feature", identity)
		}
	}
	return nil
}

func nativeSHA256Hex(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
func hashLocalDevelopmentPolicy(p LocalDevelopmentPolicy) [32]byte {
	data, _ := json.Marshal(p)
	return sha256.Sum256(append([]byte(localDevelopmentContract+"\x00"), data...))
}

// LocalDevelopmentPolicyFromEnvironment requires all three explicit opt-in
// fields. Any release policy field mixed into the development environment fails.
func LocalDevelopmentPolicyFromEnvironment() (LocalDevelopmentPolicy, error) {
	var p LocalDevelopmentPolicy
	if os.Getenv("TALON_NATIVE_MODE") != NativeAdmissionLocalDevelopment {
		return p, fmt.Errorf("TALON_NATIVE_MODE=local-development is required")
	}
	for _, item := range os.Environ() {
		name, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(name, "TALON_NATIVE_") && name != "TALON_NATIVE_MODE" && name != "TALON_NATIVE_DEV_POLICY_FILE" && name != "TALON_NATIVE_DEV_POLICY_SHA256" {
			return p, fmt.Errorf("release/unknown native field %s cannot be mixed with local development", name)
		}
	}
	path, digest := os.Getenv("TALON_NATIVE_DEV_POLICY_FILE"), os.Getenv("TALON_NATIVE_DEV_POLICY_SHA256")
	if !canonicalAbsolutePath(path) || !shaPattern.MatchString(digest) {
		return p, fmt.Errorf("absolute development policy file and SHA256 are required")
	}
	data, err := readLocalDevelopmentFile(path, maxManifestBytes)
	if err != nil {
		return p, err
	}
	return decodePinnedLocalDevelopmentPolicy(data, digest)
}

// snapshotDevelopmentLibrary loads immutable verified bytes, never a path that
// can be rebuilt between hashing and dlopen. The private copy is not a release.
func snapshotDevelopmentLibrary(data []byte) (_ string, _ string, err error) {
	platform, err := currentNativePlatform()
	if err != nil {
		return "", "", err
	}
	dir, err := os.MkdirTemp("", "talon-native-local-development-")
	if err != nil {
		return "", "", err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
		}
	}()
	path := filepath.Join(dir, platform.DynamicLibrary)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0500)
	if err != nil {
		return "", "", err
	}
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	return path, dir, err
}

func verifyLocalDevelopment(p LocalDevelopmentPolicy) (_ *verifiedNative, err error) {
	v, library, err := verifyLocalDevelopmentArtifacts(p)
	if err != nil {
		return nil, err
	}
	v.LibraryPath, v.tempDir, err = snapshotDevelopmentLibrary(library)
	if err != nil {
		return nil, err
	}
	return v, nil
}

// Shared owner of static validation. It neither executes native code nor writes
// a loader snapshot. Admission adds a private copy of these exact library bytes.
func verifyLocalDevelopmentArtifacts(p LocalDevelopmentPolicy) (_ *verifiedNative, _ []byte, err error) {
	if err = validateLocalDevelopmentPolicy(p); err != nil {
		return nil, nil, err
	}
	// Read each input once and compare the exact bytes that will be used.
	library, err := readLocalDevelopmentFile(p.LibraryPath, maxNativeMemberBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("read local library: %w", err)
	}
	if nativeSHA256Hex(library) != p.LibrarySHA256 {
		return nil, nil, fmt.Errorf("local library SHA256 mismatch")
	}
	header, err := readLocalDevelopmentFile(p.HeaderPath, maxManifestBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("read local header: %w", err)
	}
	if nativeSHA256Hex(header) != p.HeaderSHA256 {
		return nil, nil, fmt.Errorf("local header SHA256 mismatch")
	}
	data, err := readLocalDevelopmentFile(p.BuildManifestPath, maxManifestBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("read local self-manifest: %w", err)
	}
	if nativeSHA256Hex(data) != p.BuildManifestSHA256 {
		return nil, nil, fmt.Errorf("local self-manifest SHA256 mismatch")
	}
	var build coreBuildManifest
	if err = decodeStrictJSON(data, &build); err != nil {
		return nil, nil, err
	}
	// Detach slice memory from the caller before retaining the process policy.
	p.RequiredCapabilities = cloneLocalRequiredCapabilities(p.RequiredCapabilities)
	platform, _ := currentNativePlatform()
	v := &verifiedNative{Development: &p, developmentBuild: build, policyHash: hashLocalDevelopmentPolicy(p), Info: NativeInfo{
		Admission: NativeAdmissionLocalDevelopment, Platform: platform.Name, CoreCommit: build.GitCommit, CoreVersion: build.CoreSemver,
		ABIProfile: build.ABI.Profile, ABIVersion: build.ABI.Version, HeaderSHA256: p.HeaderSHA256, LibrarySHA256: p.LibrarySHA256,
		BuildProfile: p.BuildProfile, BuildProfileSource: "caller-declared (not attested by Core ABI)",
		DevelopmentLibraryPath: p.LibraryPath, DevelopmentHeaderPath: p.HeaderPath, DevelopmentManifestPath: p.BuildManifestPath, DevelopmentManifestSHA256: p.BuildManifestSHA256,
		Gates: map[string]CapabilityGate{"storage_conditional_batch_v1": {Status: "gated", Reason: "local development does not grant release-only admission"}},
	}}
	if _, err = verifyCoreBuildIdentity(data, v); err != nil {
		return nil, nil, err
	}
	v.Info.CoreGitDirty = *build.GitDirty
	return v, library, nil
}
