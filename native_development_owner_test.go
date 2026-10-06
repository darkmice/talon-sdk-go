package talon

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func ownerPolicyBytes(t *testing.T, p LocalDevelopmentPolicy) []byte {
	t.Helper()
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(data, '\n')
}

func ownerReject(t *testing.T, err error, code ErrorCode) {
	t.Helper()
	if err == nil || ErrorCodeOf(err) != code {
		t.Fatalf("expected %s rejection, got %v", code, err)
	}
	var envelope *TalonError
	if !errors.As(err, &envelope) || envelope.Cause == nil {
		t.Fatal("error cause was discarded")
	}
}

func TestLocalPolicyOwnerStaticAndLoaderLayers(t *testing.T) {
	p, _ := developmentTestPolicy(t)
	data := ownerPolicyBytes(t, p)
	beforeEnv := os.Environ()
	before, _ := os.ReadDir(filepath.Dir(p.LibraryPath))
	r, err := VerifyLocalDevelopmentPolicy(data, nativeSHA256Hex(data))
	if err != nil {
		t.Fatal(err)
	}
	if r.Provenance != LocalDevelopmentProvenance || r.Stage != LocalDevelopmentStaticVerification || r.Info.ReleaseTag != "" || r.Info.KeyID != "" || r.Info.Gates["storage_conditional_batch_v1"].Status != "gated" {
		t.Fatal("static verification misrepresented admission")
	}
	if loadedNative != nil {
		t.Fatal("static verifier admitted a runtime")
	}
	after, _ := os.ReadDir(filepath.Dir(p.LibraryPath))
	if !reflect.DeepEqual(beforeEnv, os.Environ()) || len(before) != len(after) {
		t.Fatal("static verifier changed environment or artifact files")
	}
	file := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	fromFile, err := VerifyLocalDevelopmentPolicyFile(file, nativeSHA256Hex(data))
	if err != nil || !reflect.DeepEqual(r, fromFile) {
		t.Fatal("file and byte owners differ")
	}
	// A static fixture is deliberately not a library. Static success must not be
	// presented as runtime success; a real loader refusal preserves its cause.
	_, err = AttestLocalDevelopmentPolicy(data, nativeSHA256Hex(data))
	ownerReject(t, err, CodeNativeLoad)
	if loadedNative != nil {
		t.Fatal("failed probe admitted a runtime")
	}
}

func TestLocalPolicyOwnerPreservesEmptyRequirementIdentity(t *testing.T) {
	for _, required := range [][]string{nil, {}} {
		p, _ := developmentTestPolicy(t)
		p.RequiredCapabilities = required
		data := ownerPolicyBytes(t, p)
		r, err := VerifyLocalDevelopmentPolicy(data, nativeSHA256Hex(data))
		if err != nil {
			t.Fatal(err)
		}
		v, _, err := verifyLocalDevelopmentArtifacts(p)
		if err != nil {
			t.Fatal(err)
		}
		if (r.Policy.RequiredCapabilities == nil) != (required == nil) || hashLocalDevelopmentPolicy(r.Policy) != hashLocalDevelopmentPolicy(p) || v.policyHash != hashLocalDevelopmentPolicy(p) {
			t.Fatal("empty requirement clone changed loaded policy identity")
		}
	}
}

func TestLocalPolicyOwnerStrictInput(t *testing.T) {
	p, _ := developmentTestPolicy(t)
	data := ownerPolicyBytes(t, p)
	cases := map[string][]byte{
		"empty":             nil,
		"unknown":           bytes.Replace(data, []byte("{\n"), []byte("{\n\"bundle_manifest_path\":\"/not-a-local-locator\",\n"), 1),
		"duplicate":         bytes.Replace(data, []byte("{\n"), []byte("{\n\"contract\":\"talon-native-local-development-v1\",\n"), 1),
		"escaped_duplicate": bytes.Replace(data, []byte("{\n"), []byte("{\n\"con\\u0074ract\":\"talon-native-local-development-v1\",\n"), 1),
		"case_alias":        bytes.Replace(data, []byte(`"contract"`), []byte(`"Contract"`), 1),
		"trailing":          append(append([]byte(nil), data...), []byte("{}")...),
		"invalid_utf8":      append(append([]byte(nil), data...), byte(0xff)),
		"budget":            bytes.Repeat([]byte(" "), maxManifestBytes+1),
		"null":              []byte("null"),
		"array":             []byte("[]"),
	}
	var omitted map[string]json.RawMessage
	if err := json.Unmarshal(data, &omitted); err != nil {
		t.Fatal(err)
	}
	delete(omitted, "required_capabilities")
	cases["omitted"], _ = json.Marshal(omitted)
	for name, changed := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := VerifyLocalDevelopmentPolicy(changed, nativeSHA256Hex(changed))
			ownerReject(t, err, CodeNativeVerification)
		})
	}
	for _, pin := range []string{"", strings.Repeat("A", 64), strings.Repeat("0", 64)} {
		_, err := VerifyLocalDevelopmentPolicy(data, pin)
		ownerReject(t, err, CodeNativeVerification)
	}
	for _, kind := range []string{"contract", "relative", "unclean", "nul", "aliased", "abi_profile", "abi_version", "build_profile", "cap_duplicate", "cap_bad_version", "release_gate"} {
		t.Run(kind, func(t *testing.T) {
			bad := p
			switch kind {
			case "contract":
				bad.Contract = "release"
			case "relative":
				bad.LibraryPath = "relative"
			case "unclean":
				bad.LibraryPath = filepath.Dir(p.LibraryPath) + "/./libtalon"
			case "nul":
				bad.LibraryPath += "\x00"
			case "aliased":
				bad.HeaderPath = bad.LibraryPath
			case "abi_profile":
				bad.ExpectedABIProfile = "unsupported"
			case "abi_version":
				bad.ExpectedABIVersion++
			case "build_profile":
				bad.BuildProfile = "production"
			case "cap_duplicate":
				bad.RequiredCapabilities = []string{"native_sql_result@2", "native_sql_result@2"}
			case "cap_bad_version":
				bad.RequiredCapabilities = []string{"native_sql_result@02"}
			case "release_gate":
				bad.RequiredCapabilities = []string{"storage_conditional_batch@1"}
			}
			changed := ownerPolicyBytes(t, bad)
			_, err := VerifyLocalDevelopmentPolicy(changed, nativeSHA256Hex(changed))
			ownerReject(t, err, CodeNativeVerification)
		})
	}
}

func TestLocalPolicyOwnerFilesAndCauses(t *testing.T) {
	for _, member := range []string{"library", "header", "manifest", "policy"} {
		for _, kind := range []string{"missing", "symlink", "empty", "directory", "writable", "budget", "tampered"} {
			t.Run(member+"_"+kind, func(t *testing.T) {
				p, _ := developmentTestPolicy(t)
				data := ownerPolicyBytes(t, p)
				policyPath := filepath.Join(t.TempDir(), "policy.json")
				if err := os.WriteFile(policyPath, data, 0600); err != nil {
					t.Fatal(err)
				}
				path, max := p.LibraryPath, maxNativeMemberBytes
				switch member {
				case "header":
					path, max = p.HeaderPath, maxManifestBytes
				case "manifest":
					path, max = p.BuildManifestPath, maxManifestBytes
				case "policy":
					path, max = policyPath, maxManifestBytes
				}
				orig, _ := os.ReadFile(path)
				switch kind {
				case "missing":
					os.Remove(path)
				case "symlink":
					target := path + ".target"
					os.Rename(path, target)
					os.Symlink(target, path)
				case "empty":
					os.WriteFile(path, nil, 0600)
				case "directory":
					os.Remove(path)
					os.Mkdir(path, 0700)
				case "writable":
					os.Chmod(path, 0666)
				case "budget":
					if err := os.Truncate(path, max+1); err != nil {
						t.Fatal(err)
					}
				case "tampered":
					os.WriteFile(path, append(orig, '!'), 0600)
				}
				_, err := VerifyLocalDevelopmentPolicyFile(policyPath, nativeSHA256Hex(data))
				ownerReject(t, err, CodeNativeVerification)
				if kind == "missing" && !errors.Is(err, os.ErrNotExist) {
					t.Fatal("filesystem cause was lost")
				}
			})
		}
	}
}

func TestLocalPolicyOwnerManifestContract(t *testing.T) {
	for _, kind := range []string{"unknown", "duplicate", "abi", "symbols", "target", "header", "binding", "capability", "feature", "duplicate_feature", "duplicate_capability"} {
		t.Run(kind, func(t *testing.T) {
			p, b := developmentTestPolicy(t)
			switch kind {
			case "abi":
				b.ABI.Version++
			case "symbols":
				b.ABI.RequiredSymbols = b.ABI.RequiredSymbols[1:]
			case "target":
				b.Target = "wrong-target"
			case "header":
				b.HeaderSHA256 = strings.Repeat("0", 64)
			case "capability":
				b.Capabilities[1].Status = "gated"
				reason := "test"
				b.Capabilities[1].Reason = &reason
			case "feature":
				b.Features = b.Features[:len(b.Features)-1]
			case "duplicate_feature":
				b.Features = append(b.Features, b.Features[0])
			case "duplicate_capability":
				b.Capabilities = append(b.Capabilities, b.Capabilities[0])
			}
			b.BuildBindingSHA256 = computeBuildBinding(b)
			if kind == "binding" {
				b.BuildBindingSHA256 = strings.Repeat("0", 64)
			}
			manifest, _ := json.Marshal(b)
			if kind == "unknown" {
				manifest = bytes.Replace(manifest, []byte("{"), []byte(`{"unknown":true,`), 1)
			}
			if kind == "duplicate" {
				manifest = bytes.Replace(manifest, []byte("{"), []byte(`{"manifest_version":2,`), 1)
			}
			os.WriteFile(p.BuildManifestPath, manifest, 0600)
			p.BuildManifestSHA256 = nativeSHA256Hex(manifest)
			data := ownerPolicyBytes(t, p)
			_, err := VerifyLocalDevelopmentPolicy(data, nativeSHA256Hex(data))
			ownerReject(t, err, CodeNativeVerification)
		})
	}
}

func ownerCopyLocators(t *testing.T, p LocalDevelopmentPolicy) LocalDevelopmentLocators {
	t.Helper()
	dir := t.TempDir()
	l := LocalDevelopmentLocators{filepath.Join(dir, "库 \"local\".dylib"), filepath.Join(dir, "header.h"), filepath.Join(dir, "build.json")}
	for source, target := range map[string]string{p.LibraryPath: l.LibraryPath, p.HeaderPath: l.HeaderPath, p.BuildManifestPath: l.BuildManifestPath} {
		data, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return l
}

func TestLocalPolicyOwnerRelocationPreservesOriginal(t *testing.T) {
	p, _ := developmentTestPolicy(t)
	original := ownerPolicyBytes(t, p)
	before := append([]byte(nil), original...)
	locators := ownerCopyLocators(t, p)
	output, proof, err := DeriveLocalDevelopmentPolicy(original, nativeSHA256Hex(original), locators)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, before) || proof.Original.PolicySHA256 != nativeSHA256Hex(original) || proof.Relocated.PolicySHA256 != nativeSHA256Hex(output) || proof.PreservedBytesSHA256 == "" {
		t.Fatal("missing byte preservation proof")
	}
	for _, kind := range []string{"profile", "capabilities", "whitespace", "locators", "original_pin", "output_pin"} {
		t.Run(kind, func(t *testing.T) {
			bad := append([]byte(nil), output...)
			expected := locators
			aPin := nativeSHA256Hex(original)
			switch kind {
			case "profile":
				bad = bytes.Replace(bad, []byte(`"debug"`), []byte(`"release"`), 1)
			case "capabilities":
				bad = bytes.Replace(bad, []byte(`"native_sql_result@2"`), nil, 1)
			case "whitespace":
				bad = append(bad, ' ')
			case "locators":
				expected.HeaderPath = p.HeaderPath
			case "original_pin":
				aPin = strings.Repeat("0", 64)
			}
			bPin := nativeSHA256Hex(bad)
			if kind == "output_pin" {
				bPin = strings.Repeat("0", 64)
			}
			_, err := VerifyLocalDevelopmentPolicyRelocation(original, aPin, bad, bPin, expected)
			ownerReject(t, err, CodeNativeVerification)
		})
	}
	// The source is always verified first, even when destination validation could
	// succeed. Losing a source artifact cannot be hidden by relocation.
	os.Remove(p.LibraryPath)
	_, _, err = DeriveLocalDevelopmentPolicy(original, nativeSHA256Hex(original), locators)
	ownerReject(t, err, CodeNativeVerification)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatal("original was not verified first")
	}
}
