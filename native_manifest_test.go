/*
 * Copyright (c) 2026 Talon Contributors
 * Author: dark.lijin@gmail.com
 * Licensed under the Talon Community Dual License Agreement.
 * See the LICENSE file in the project root for full license information.
 */

package talon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type testNativeManifest struct {
	SchemaVersion string `json:"schema_version"`
	ReleaseGate   struct {
		ReleaseReady                   bool     `json:"release_ready"`
		SDKNativeMatrixReleaseReady    bool     `json:"sdk_native_matrix_release_ready"`
		DeclaredCoreMatrixReleaseReady bool     `json:"declared_core_matrix_release_ready"`
		BlockingPlatforms              []string `json:"blocking_platforms"`
		Blockers                       []string `json:"blockers"`
	} `json:"release_gate"`
	NativeCore struct {
		GitSHA          string `json:"git_sha"`
		CargoLockSHA256 string `json:"cargo_lock_sha256"`
		SourceClean     bool   `json:"source_clean"`
	} `json:"native_core"`
	ABI struct {
		Profile         string   `json:"profile"`
		Header          string   `json:"header"`
		HeaderSHA256    string   `json:"header_sha256"`
		RequiredSymbols []string `json:"required_symbols"`
	} `json:"abi"`
	Materials struct {
		CoreLicense struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
		} `json:"core_license"`
	} `json:"materials"`
	Platforms []struct {
		GOOS         string `json:"goos"`
		GOARCH       string `json:"goarch"`
		ReleaseReady bool   `json:"release_ready"`
		Provenance   struct {
			SourceGitSHA            string `json:"source_git_sha"`
			SourceClean             bool   `json:"source_clean"`
			NativeConformanceTested bool   `json:"native_conformance_tested"`
			SBOMAndLicensesCovered  bool   `json:"sbom_and_licenses_covered"`
			VerificationLimitation  string `json:"verification_limitation"`
		} `json:"provenance"`
		Materials struct {
			CycloneDXSBOM struct {
				Path   string `json:"path"`
				SHA256 string `json:"sha256"`
			} `json:"cyclonedx_sbom"`
			LicenseInventory struct {
				Path   string `json:"path"`
				SHA256 string `json:"sha256"`
			} `json:"license_inventory"`
		} `json:"materials"`
		DynamicLibrary struct {
			Path   string `json:"path"`
			Size   int64  `json:"size"`
			SHA256 string `json:"sha256"`
		} `json:"dynamic_library"`
		StaticLibrary struct {
			Path   string `json:"path"`
			Size   int64  `json:"size"`
			SHA256 string `json:"sha256"`
		} `json:"static_library"`
	} `json:"platforms"`
}

func loadTestNativeManifest(t *testing.T) (string, testNativeManifest) {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Dir(sourceFile)
	raw, err := os.ReadFile(filepath.Join(root, "native-manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest testNativeManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	return root, manifest
}

func TestNativeManifestChecksumsAndCurrentPlatform(t *testing.T) {
	root, manifest := loadTestNativeManifest(t)
	if manifest.SchemaVersion != "1.2" {
		t.Fatalf("schema_version = %q, want 1.2", manifest.SchemaVersion)
	}
	if manifest.NativeCore.GitSHA == "" || len(manifest.NativeCore.CargoLockSHA256) != 64 || !manifest.NativeCore.SourceClean {
		t.Fatalf("native_core source provenance is incomplete: %+v", manifest.NativeCore)
	}
	if manifest.ABI.Profile == "" || len(manifest.ABI.RequiredSymbols) == 0 {
		t.Fatalf("ABI provenance is incomplete: %+v", manifest.ABI)
	}
	assertManifestFile(t, root, manifest.ABI.Header, manifest.ABI.HeaderSHA256, -1)
	assertManifestFile(t, root, manifest.Materials.CoreLicense.Path, manifest.Materials.CoreLicense.SHA256, -1)

	foundCurrentPlatform := false
	for _, platform := range manifest.Platforms {
		assertManifestFile(t, root, platform.DynamicLibrary.Path, platform.DynamicLibrary.SHA256, platform.DynamicLibrary.Size)
		assertManifestFile(t, root, platform.StaticLibrary.Path, platform.StaticLibrary.SHA256, platform.StaticLibrary.Size)
		assertManifestFile(t, root, platform.Materials.CycloneDXSBOM.Path, platform.Materials.CycloneDXSBOM.SHA256, -1)
		assertManifestFile(t, root, platform.Materials.LicenseInventory.Path, platform.Materials.LicenseInventory.SHA256, -1)
		if platform.Provenance.SourceGitSHA == "" {
			t.Errorf("%s/%s has no source SHA", platform.GOOS, platform.GOARCH)
		}
		if !platform.ReleaseReady && platform.Provenance.VerificationLimitation == "" {
			t.Errorf("%s/%s is blocked without a verification limitation", platform.GOOS, platform.GOARCH)
		}
		if platform.GOOS == runtime.GOOS && platform.GOARCH == runtime.GOARCH {
			foundCurrentPlatform = true
			assertRequiredSymbols(t, filepath.Join(root, filepath.FromSlash(platform.DynamicLibrary.Path)), manifest.ABI.RequiredSymbols)
			if platform.ReleaseReady && (platform.Provenance.SourceGitSHA != manifest.NativeCore.GitSHA ||
				!platform.Provenance.SourceClean || !platform.Provenance.NativeConformanceTested ||
				!platform.Provenance.SBOMAndLicensesCovered) {
				t.Fatalf("current release-ready platform lacks complete provenance: %+v", platform.Provenance)
			}
		}
	}
	if !foundCurrentPlatform {
		t.Fatalf("manifest has no entry for current platform %s/%s", runtime.GOOS, runtime.GOARCH)
	}
}

func TestNativeManifestReleaseBlockersAreMachineReadable(t *testing.T) {
	_, manifest := loadTestNativeManifest(t)
	if manifest.ReleaseGate.ReleaseReady {
		t.Fatal("full declared Core matrix must not be marked release-ready before CI aggregation")
	}
	if !manifest.ReleaseGate.SDKNativeMatrixReleaseReady || manifest.ReleaseGate.DeclaredCoreMatrixReleaseReady {
		t.Fatalf("release gate scopes are incorrect: %+v", manifest.ReleaseGate)
	}
	if len(manifest.ReleaseGate.BlockingPlatforms) == 0 || len(manifest.ReleaseGate.Blockers) == 0 {
		t.Fatalf("release blockers are incomplete: %+v", manifest.ReleaseGate)
	}
}

func assertRequiredSymbols(t *testing.T, library string, symbols []string) {
	t.Helper()
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("nm", "-gU", library)
	case "linux":
		command = exec.Command("nm", "-D", "--defined-only", library)
	default:
		return
	}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect native symbols: %v\n%s", err, output)
	}
	text := string(output)
	for _, symbol := range symbols {
		if !strings.Contains(text, symbol) {
			t.Errorf("required symbol %q missing from %s", symbol, library)
		}
	}
}

func assertManifestFile(t *testing.T, root, relativePath, wantSHA string, wantSize int64) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relativePath)))
	if err != nil {
		t.Fatalf("read %s: %v", relativePath, err)
	}
	if wantSize >= 0 && int64(len(data)) != wantSize {
		t.Errorf("%s size = %d, want %d", relativePath, len(data), wantSize)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != wantSHA {
		t.Errorf("%s sha256 = %s, want %s", relativePath, got, wantSHA)
	}
}
