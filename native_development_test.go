package talon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func developmentTestPolicy(t *testing.T) (LocalDevelopmentPolicy, coreBuildManifest) {
	t.Helper()
	dir := t.TempDir()
	platform, err := currentNativePlatform()
	if err != nil {
		t.Fatal(err)
	}
	dirty := true
	p := LocalDevelopmentPolicy{Contract: localDevelopmentContract, LibraryPath: filepath.Join(dir, "libtalon"), HeaderPath: filepath.Join(dir, "talon.h"), BuildManifestPath: filepath.Join(dir, "manifest.json"), ExpectedABIProfile: "talon-native-c", ExpectedABIVersion: 1, BuildProfile: "debug", RequiredCapabilities: []string{"native_sql_result@2"}}
	library, header := []byte("test bytes: never dlopened"), []byte("test header")
	p.LibrarySHA256, p.HeaderSHA256 = nativeSHA256Hex(library), nativeSHA256Hex(header)
	b := coreBuildManifest{ManifestVersion: 2, CoreSemver: "0.1.1", GitCommit: strings.Repeat("a", 40), GitDirty: &dirty, Target: platform.Target, CargoLockSHA256: strings.Repeat("b", 64), HeaderSHA256: p.HeaderSHA256,
		ABI:      coreBuildABI{Profile: p.ExpectedABIProfile, Version: p.ExpectedABIVersion, RequiredSymbols: append([]string(nil), sdkRequiredSymbols...)},
		Features: []string{"native_build_manifest_v2", "native_error_codes_v1", "sql_tlv_v1", "native_conditional_transaction_v2", "conditional_transaction_command_digest_v1", "native_sql_result_v2"}, Capabilities: []NativeCapability{{Name: "native_conditional_transaction_v2", Version: 2, Status: "available"}, {Name: "native_sql_result", Version: 2, Status: "available"}}}
	b.BuildBindingSHA256 = computeBuildBinding(b)
	data, _ := json.Marshal(b)
	p.BuildManifestSHA256 = nativeSHA256Hex(data)
	for path, content := range map[string][]byte{p.LibraryPath: library, p.HeaderPath: header, p.BuildManifestPath: data} {
		if err := os.WriteFile(path, content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return p, b
}

func TestLocalDevelopmentPinsAndRuntimeContract(t *testing.T) {
	p, b := developmentTestPolicy(t)
	v, err := verifyLocalDevelopment(p)
	if err != nil {
		t.Fatal(err)
	}
	defer removeVerifiedNative(v)
	if !v.Info.CoreGitDirty || v.Info.Admission != NativeAdmissionLocalDevelopment || v.Info.ReleaseTag != "" || v.Info.KeyID != "" || v.Info.Gates["storage_conditional_batch_v1"].Status != "gated" {
		t.Fatalf("misclassified provenance: %+v", v.Info)
	}
	for _, path := range []string{p.LibraryPath, p.HeaderPath, p.BuildManifestPath} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			orig, _ := os.ReadFile(path)
			defer os.WriteFile(path, orig, 0600)
			if err := os.WriteFile(path, append(orig, '!'), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := verifyLocalDevelopment(p); err == nil {
				t.Fatal("changed artifact was accepted")
			}
		})
	}
	// Updating the self-manifest pin does not permit an unsupported ABI, missing
	// SQL capability, missing required feature, wrong header, or corrupt binding.
	for _, kind := range []string{"abi", "capability", "feature", "binding", "header"} {
		t.Run(kind, func(t *testing.T) {
			data, _ := json.Marshal(b)
			var changed coreBuildManifest
			json.Unmarshal(data, &changed)
			switch kind {
			case "abi":
				changed.ABI.Version = 2
			case "capability":
				changed.Capabilities[1].Status = "gated"
				reason := "unavailable"
				changed.Capabilities[1].Reason = &reason
			case "feature":
				changed.Features = changed.Features[:len(changed.Features)-1]
			case "binding":
				changed.BuildBindingSHA256 = strings.Repeat("0", 64)
			case "header":
				changed.HeaderSHA256 = strings.Repeat("0", 64)
			}
			if kind != "binding" {
				changed.BuildBindingSHA256 = computeBuildBinding(changed)
			}
			modified, _ := json.Marshal(changed)
			policy := p
			policy.BuildManifestSHA256 = nativeSHA256Hex(modified)
			if _, err := verifyCoreBuildIdentity(modified, &verifiedNative{Development: &policy}); err == nil {
				t.Fatal("invalid local runtime contract was accepted")
			}
		})
	}
	data, _ := json.Marshal(b)
	// Same real-shaped dirty manifest must be rejected by the release identity
	// branch even when every other release source/ABI identity matches exactly.
	release := &verifiedNative{Manifest: nativeManifest{Source: nativeSource{CargoVersion: b.CoreSemver, Commit: b.GitCommit, CargoLockSHA256: b.CargoLockSHA256}, Build: nativeBuild{Target: b.Target}, ABI: nativeABI{Profile: b.ABI.Profile, Version: b.ABI.Version, HeaderSHA256: b.HeaderSHA256, RequiredSymbols: b.ABI.RequiredSymbols}}}
	if _, err := verifyCoreBuildIdentity(data, release); err == nil {
		t.Fatal("release admitted dirty manifest")
	}
	if _, err := OpenWithOptions(t.TempDir(), OpenOptions{Native: NativePolicy{BundleDir: "release"}, LocalDevelopment: &p}); ErrorCodeOf(err) != CodeNativeVerification {
		t.Fatalf("mixed policies: %v", err)
	}
}

func TestLocalDevelopmentEnvironmentOptIn(t *testing.T) {
	// Isolate native fields so this unit test also works in a configured shell.
	for _, item := range os.Environ() {
		name, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(name, "TALON_NATIVE_") {
			t.Setenv(name, "")
			os.Unsetenv(name)
		}
	}
	p, _ := developmentTestPolicy(t)
	data, _ := json.Marshal(p)
	file := filepath.Join(t.TempDir(), "policy.json")
	os.WriteFile(file, data, 0600)
	t.Setenv("TALON_NATIVE_DEV_POLICY_FILE", file)
	t.Setenv("TALON_NATIVE_DEV_POLICY_SHA256", nativeSHA256Hex(data))
	if _, err := LocalDevelopmentPolicyFromEnvironment(); err == nil {
		t.Fatal("missing opt-in accepted")
	}
	if _, err := Open(t.TempDir()); ErrorCodeOf(err) != CodeNativeVerification {
		t.Fatalf("default route admitted local configuration: %v", err)
	}
	t.Setenv("TALON_NATIVE_MODE", NativeAdmissionLocalDevelopment)
	if _, err := LocalDevelopmentPolicyFromEnvironment(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TALON_NATIVE_DEV_POLICY_SHA256", strings.Repeat("0", 64))
	if _, err := LocalDevelopmentPolicyFromEnvironment(); err == nil {
		t.Fatal("policy tampering accepted")
	}
	t.Setenv("TALON_NATIVE_DEV_POLICY_SHA256", nativeSHA256Hex(data))
	t.Setenv("TALON_NATIVE_BUNDLE_DIR", "release")
	if _, err := LocalDevelopmentPolicyFromEnvironment(); err == nil {
		t.Fatal("mixed release environment accepted")
	}
}

func TestLocalDevelopmentRealAdmission(t *testing.T) {
	if os.Getenv("TALON_TEST_LOCAL_DEVELOPMENT") != "1" {
		t.Skip("requires explicit real local development configuration")
	}
	p, err := LocalDevelopmentPolicyFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TALON_NATIVE_MODE", "")
	if _, err := Open(t.TempDir()); ErrorCodeOf(err) != CodeNativeVerification {
		t.Fatalf("default release route admitted same real local configuration: %v", err)
	}
	t.Setenv("TALON_NATIVE_MODE", NativeAdmissionLocalDevelopment)
	// Real artifact rejection before any admission in this process.
	for _, kind := range []string{"library hash", "header hash", "manifest hash", "ABI", "capability gate"} {
		bad := p
		switch kind {
		case "library hash":
			bad.LibrarySHA256 = strings.Repeat("0", 64)
		case "header hash":
			bad.HeaderSHA256 = strings.Repeat("0", 64)
		case "manifest hash":
			bad.BuildManifestSHA256 = strings.Repeat("0", 64)
		case "ABI":
			bad.ExpectedABIVersion++
		case "capability gate":
			bad.RequiredCapabilities = []string{"revision_stream@2"}
		}
		if _, err := OpenWithOptions(t.TempDir(), OpenOptions{LocalDevelopment: &bad}); ErrorCodeOf(err) != CodeNativeVerification {
			t.Fatalf("%s accepted or unclassified: %v", kind, err)
		}
	}
	data, err := os.ReadFile(p.BuildManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var b coreBuildManifest
	if err := decodeStrictJSON(data, &b); err != nil {
		t.Fatal(err)
	}
	release := &verifiedNative{Manifest: nativeManifest{Source: nativeSource{CargoVersion: b.CoreSemver, Commit: b.GitCommit, CargoLockSHA256: b.CargoLockSHA256}, Build: nativeBuild{Target: b.Target}, ABI: nativeABI{Profile: b.ABI.Profile, Version: b.ABI.Version, HeaderSHA256: b.HeaderSHA256, RequiredSymbols: b.ABI.RequiredSymbols}}}
	if _, err := verifyCoreBuildIdentity(data, release); err == nil {
		t.Fatal("same real dirty library manifest accepted as release")
	}
	// Even a newly pinned, semantically identical sidecar must match the exact
	// bytes returned by the real loaded ABI. Offline JSON validation alone is insufficient.
	bad := p
	bad.BuildManifestPath = filepath.Join(t.TempDir(), "different-self-manifest.json")
	modified := append(append([]byte(nil), data...), '\n')
	if err := os.WriteFile(bad.BuildManifestPath, modified, 0600); err != nil {
		t.Fatal(err)
	}
	bad.BuildManifestSHA256 = nativeSHA256Hex(modified)
	if _, err := OpenWithOptions(t.TempDir(), OpenOptions{LocalDevelopment: &bad}); ErrorCodeOf(err) != CodeNativeVerification {
		t.Fatalf("runtime ABI accepted a different pinned sidecar: %v", err)
	}
	// Direct options and normal Open share the same admitted development policy.
	db, err := OpenWithOptions(t.TempDir(), OpenOptions{LocalDevelopment: &p})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	info := db.NativeInfo()
	if info.Admission != NativeAdmissionLocalDevelopment || !info.CoreGitDirty || info.BuildProfile != "debug" || info.ReleaseTag != "" || info.KeyID != "" {
		t.Fatalf("real identity: %+v", info)
	}
	if err := db.RequireCapability("storage_conditional_batch_v1"); ErrorCodeOf(err) != CodeCapabilityUnavailable {
		t.Fatalf("development opened release-only gate: %v", err)
	}
	peer, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	peer.Close()
	if _, err := ensureNativeLoaded(NativePolicy{}); ErrorCodeOf(err) != CodeNativeVerification {
		t.Fatalf("development leaked into default explicit release options: %v", err)
	}
	t.Logf("admission=%s dirty=%t declared_profile=%s source=%s core=%s library_sha256=%s self_manifest_sha256=%s", info.Admission, info.CoreGitDirty, info.BuildProfile, info.BuildProfileSource, info.CoreCommit, info.LibrarySHA256, info.DevelopmentManifestSHA256)
}
