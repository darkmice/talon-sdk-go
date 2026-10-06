package talon

/*
#include <stdlib.h>
#include "native_loader.h"
*/
import "C"
import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"
)

// PrepareLocalDevelopment executes the chosen local native code to extract its
// real ABI self-manifest, validates it, and writes a new exclusive config directory.
// No release signature/manifest is manufactured. It must run before any DB opens.
func PrepareLocalDevelopment(libraryPath, headerPath, buildProfile, outputDir string, required []string) (LocalDevelopmentPolicy, error) {
	var p LocalDevelopmentPolicy
	for _, path := range []string{libraryPath, headerPath, outputDir} {
		if !canonicalAbsolutePath(path) {
			return p, fmt.Errorf("prepare requires canonical absolute paths")
		}
	}
	if _, err := os.Lstat(outputDir); !os.IsNotExist(err) {
		return p, fmt.Errorf("output directory must be new and unavailable before preparation")
	}
	p = LocalDevelopmentPolicy{Contract: localDevelopmentContract, LibraryPath: libraryPath, HeaderPath: headerPath,
		BuildManifestPath: filepath.Join(outputDir, "core-build-manifest.json"), ExpectedABIProfile: "talon-native-c", ExpectedABIVersion: 1, BuildProfile: buildProfile, RequiredCapabilities: append([]string(nil), required...)}
	library, err := readBoundedRegularFile(libraryPath, maxNativeMemberBytes)
	if err != nil {
		return p, err
	}
	header, err := readBoundedRegularFile(headerPath, maxManifestBytes)
	if err != nil {
		return p, err
	}
	p.LibrarySHA256, p.HeaderSHA256 = nativeSHA256Hex(library), nativeSHA256Hex(header)
	// Validate non-digest fields before executing local code.
	p.BuildManifestSHA256 = strings.Repeat("0", 64)
	if err := validateLocalDevelopmentPolicy(p); err != nil {
		return p, err
	}
	path, dir, err := snapshotDevelopmentLibrary(library)
	if err != nil {
		return p, err
	}
	defer os.RemoveAll(dir)
	nativeLoadMu.Lock()
	defer nativeLoadMu.Unlock()
	if loadedNative != nil {
		return p, fmt.Errorf("prepare must run in a fresh process before native runtime admission")
	}
	cs := C.CString(path)
	rc := C.talon_sdk_load(cs)
	C.free(unsafe.Pointer(cs))
	if rc != 0 {
		return p, fmt.Errorf("load local Core for manifest extraction: %s", C.GoString(C.talon_sdk_loader_error()))
	}
	defer C.talon_sdk_unload()
	var out *C.char
	var code [128]C.char
	if C.talon_sdk_build_manifest(&out, &code[0], C.size_t(len(code))) != 0 {
		return p, fmt.Errorf("extract Core manifest failed: %s", C.GoString(&code[0]))
	}
	if out == nil {
		return p, fmt.Errorf("extract Core manifest returned null")
	}
	data, err := boundedCString(out, maxManifestBytes)
	C.talon_sdk_free_string(out)
	if err != nil {
		return p, err
	}
	p.BuildManifestSHA256 = nativeSHA256Hex(data)
	var build coreBuildManifest
	if err := decodeStrictJSON(data, &build); err != nil {
		return p, err
	}
	v := &verifiedNative{Development: &p, developmentBuild: build}
	if err := attestLoadedNative(v); err != nil {
		return p, err
	}
	// No overwrite of a previous config, including an existing empty directory.
	if err := os.Mkdir(outputDir, 0700); err != nil {
		return p, fmt.Errorf("create new config directory: %w", err)
	}
	write := func(name string, data []byte) error {
		f, err := os.OpenFile(filepath.Join(outputDir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, err = f.Write(data)
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		return err
	}
	if err := write("core-build-manifest.json", data); err != nil {
		return p, err
	}
	policyData, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return p, err
	}
	policyData = append(policyData, '\n')
	if err := write("policy.json", policyData); err != nil {
		return p, err
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	env := "export TALON_NATIVE_MODE=local-development\nexport TALON_NATIVE_DEV_POLICY_FILE=" + quote(filepath.Join(outputDir, "policy.json")) + "\nexport TALON_NATIVE_DEV_POLICY_SHA256=" + quote(nativeSHA256Hex(policyData)) + "\n"
	if err := write("env.sh", []byte(env)); err != nil {
		return p, err
	}
	return p, nil
}
