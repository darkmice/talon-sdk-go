package talon

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalPolicyOwnerRealCore(t *testing.T) {
	if os.Getenv("TALON_TEST_LOCAL_POLICY_OWNER") != "1" {
		t.Skip("requires explicit current local Core policy owner acceptance")
	}
	file, pin := os.Getenv("TALON_NATIVE_DEV_POLICY_FILE"), os.Getenv("TALON_NATIVE_DEV_POLICY_SHA256")
	original, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	before := append([]byte(nil), original...)
	r, err := VerifyLocalDevelopmentPolicyFile(file, pin)
	if err != nil {
		t.Fatal(err)
	}
	if r.Info.LibrarySHA256 != os.Getenv("TALON_TEST_OWNER_LIBRARY_SHA256") {
		t.Fatal("real Core differs from externally selected acceptance identity")
	}
	if !r.Info.CoreGitDirty || r.Stage != LocalDevelopmentStaticVerification {
		t.Fatal("incorrect static provenance")
	}
	locators := ownerCopyLocators(t, r.Policy)
	output, proof, err := DeriveLocalDevelopmentPolicy(original, pin, locators)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range [][]byte{original, output} {
		observed, err := AttestLocalDevelopmentPolicy(candidate, nativeSHA256Hex(candidate))
		if err != nil {
			t.Fatal(err)
		}
		if observed.Stage != LocalDevelopmentRuntimeVerification || observed.Provenance != LocalDevelopmentProvenance || observed.Info.LibrarySHA256 != r.Info.LibrarySHA256 || observed.Info.KeyID != "" || observed.Info.ReleaseTag != "" || observed.Info.Gates["storage_conditional_batch_v1"].Status != "gated" {
			t.Fatal("real runtime provenance or release gate changed")
		}
		if loadedNative != nil {
			t.Fatal("probe admitted process runtime")
		}
	}
	// A repinned semantically identical sidecar can pass the static layer, but
	// runtime owner must reject its different exact bytes from real Core output.
	bad := proof.Relocated.Policy
	manifest, err := os.ReadFile(bad.BuildManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	bad.BuildManifestPath = filepath.Join(t.TempDir(), "different-bytes.json")
	manifest = append(manifest, '\n')
	if err := os.WriteFile(bad.BuildManifestPath, manifest, 0600); err != nil {
		t.Fatal(err)
	}
	bad.BuildManifestSHA256 = nativeSHA256Hex(manifest)
	changed := ownerPolicyBytes(t, bad)
	if _, err := VerifyLocalDevelopmentPolicy(changed, nativeSHA256Hex(changed)); err != nil {
		t.Fatal(err)
	}
	_, err = AttestLocalDevelopmentPolicy(changed, nativeSHA256Hex(changed))
	ownerReject(t, err, CodeNativeVerification)
	if _, err := AttestLocalDevelopmentPolicyFile(file, pin); err != nil {
		t.Fatal("probe did not recover after self-report rejection:", err)
	}
	// Reject actual Core's gated capability without granting a release identity.
	bad = r.Policy
	bad.RequiredCapabilities = []string{"revision_stream@2"}
	changed = ownerPolicyBytes(t, bad)
	_, err = VerifyLocalDevelopmentPolicy(changed, nativeSHA256Hex(changed))
	ownerReject(t, err, CodeNativeVerification)
	// Bind an existing temporary DB handle to the actual loader owner. No
	// second handle is opened for identity checks and no application tables exist.
	db, err := OpenWithOptions(t.TempDir(), OpenOptions{LocalDevelopment: &r.Policy})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	info, err := db.VerifyLocalDevelopmentPolicy(original, pin)
	if err != nil {
		t.Fatal(err)
	}
	if info.LibrarySHA256 != r.Info.LibrarySHA256 {
		t.Fatal("handle identity differs")
	}
	_, err = db.VerifyLocalDevelopmentPolicy(output, nativeSHA256Hex(output))
	ownerReject(t, err, CodeNativeVerification)
	bad = r.Policy
	bad.BuildProfile = "release"
	changed = ownerPolicyBytes(t, bad)
	_, err = db.VerifyLocalDevelopmentPolicy(changed, nativeSHA256Hex(changed))
	ownerReject(t, err, CodeNativeVerification)
	_, err = AttestLocalDevelopmentPolicy(original, pin)
	ownerReject(t, err, CodeNativeVerification)
	// Signed identity validation retains its clean-build requirement.
	data, err := os.ReadFile(r.Policy.BuildManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var build coreBuildManifest
	if err := decodeStrictJSON(data, &build); err != nil {
		t.Fatal(err)
	}
	release := &verifiedNative{Manifest: nativeManifest{Source: nativeSource{CargoVersion: build.CoreSemver, Commit: build.GitCommit, CargoLockSHA256: build.CargoLockSHA256}, Build: nativeBuild{Target: build.Target}, ABI: nativeABI{Profile: build.ABI.Profile, Version: build.ABI.Version, HeaderSHA256: build.HeaderSHA256, RequiredSymbols: build.ABI.RequiredSymbols}}}
	if _, err := verifyCoreBuildIdentity(data, release); err == nil {
		t.Fatal("dirty real manifest was admitted as signed release")
	}
	db.Close()
	if _, err := db.CheckedNativeInfo(); ErrorCodeOf(err) != CodeDatabaseClosed {
		t.Fatal("closed handle returned current identity")
	}
	if _, err := db.VerifyLocalDevelopmentPolicy(original, pin); ErrorCodeOf(err) != CodeDatabaseClosed {
		t.Fatal("closed handle bound policy")
	}
	current, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, current) || !strings.EqualFold(proof.Original.PolicySHA256, pin) {
		t.Fatal("original policy changed")
	}
	t.Logf("owner acceptance provenance=%s library_sha256=%s core_commit=%s abi=%s@%d static+original-loader+relocated-loader+handle-binding=passed", r.Provenance, r.Info.LibrarySHA256, r.Info.CoreCommit, r.Info.ABIProfile, r.Info.ABIVersion)
}
