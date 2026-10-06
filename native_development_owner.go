package talon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

const (
	LocalDevelopmentProvenance          = "unsigned-local-development"
	LocalDevelopmentStaticVerification  = "static-artifacts"
	LocalDevelopmentRuntimeVerification = "loader-core-attested"
)

// LocalDevelopmentVerification is a point-in-time observation, never a release
// trust token or a process admission. Static verification checks pinned files
// and the sidecar contract; only runtime verification executes the library and
// compares its actual ABI self-report. OpenWithOptions still rechecks admission.
type LocalDevelopmentVerification struct {
	PolicySHA256 string
	Provenance   string
	Stage        string
	Policy       LocalDevelopmentPolicy
	Info         NativeInfo
}

// LocalDevelopmentLocators contains the complete local policy locator set.
// There is no signed bundle locator in the unsigned local-development contract.
type LocalDevelopmentLocators struct {
	LibraryPath       string
	HeaderPath        string
	BuildManifestPath string
}

// LocalDevelopmentRelocation proves exact preservation of every original byte
// except the three JSON locator string tokens. Both sides pass owner validation.
// PreservedBytesSHA256 hashes the document with those tokens replaced by fixed
// markers; it is evidence of preservation, not a signature or trust identity.
type LocalDevelopmentRelocation struct {
	Original             LocalDevelopmentVerification
	Relocated            LocalDevelopmentVerification
	PreservedBytesSHA256 string
}

func localPolicyError(cause error) error {
	return newError(CodeNativeVerification, "verify local development policy", "local policy verification failed", cause)
}

// VerifyLocalDevelopmentPolicy verifies an external SHA256 pin, strict policy
// bytes, all three pinned regular files, ABI/build binding and capabilities.
// It does not read environment state, execute code, create files, or open a DB.
func VerifyLocalDevelopmentPolicy(data []byte, expectedSHA256 string) (LocalDevelopmentVerification, error) {
	p, err := decodePinnedLocalDevelopmentPolicy(data, expectedSHA256)
	if err != nil {
		return LocalDevelopmentVerification{}, localPolicyError(err)
	}
	v, _, err := verifyLocalDevelopmentArtifacts(p)
	if err != nil {
		return LocalDevelopmentVerification{}, localPolicyError(err)
	}
	return localDevelopmentObservation(v, expectedSHA256, LocalDevelopmentStaticVerification), nil
}

// VerifyLocalDevelopmentPolicyFile reads a bounded, non-symlink, non-writable-
// by-group/others policy file and uses the same byte verifier and external pin.
func VerifyLocalDevelopmentPolicyFile(path, expectedSHA256 string) (LocalDevelopmentVerification, error) {
	if !canonicalAbsolutePath(path) || !shaPattern.MatchString(expectedSHA256) {
		return LocalDevelopmentVerification{}, localPolicyError(fmt.Errorf("canonical policy path and lowercase SHA256 pin are required"))
	}
	data, err := readLocalDevelopmentFile(path, maxManifestBytes)
	if err != nil {
		return LocalDevelopmentVerification{}, localPolicyError(err)
	}
	return VerifyLocalDevelopmentPolicy(data, expectedSHA256)
}

func localDevelopmentObservation(v *verifiedNative, pin, stage string) LocalDevelopmentVerification {
	p := *v.Development
	p.RequiredCapabilities = cloneLocalRequiredCapabilities(p.RequiredCapabilities)
	info := v.Info
	info.Features = append([]string(nil), v.developmentBuild.Features...)
	info.Capabilities = cloneNativeCapabilities(v.developmentBuild.Capabilities)
	return LocalDevelopmentVerification{PolicySHA256: pin, Provenance: LocalDevelopmentProvenance, Stage: stage, Policy: p, Info: info}
}

func cloneLocalRequiredCapabilities(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string{}, values...)
}

// readLocalDevelopmentFile reuses the owner regular-file/race/budget checks and
// inspects permissions on the opened descriptor. Local inputs cannot be group
// or world writable. Read-only copies and usual 0600/0644/0755 inputs are valid.
func readLocalDevelopmentFile(path string, max int64) ([]byte, error) {
	f, err := openBoundedRegularFile(path, -1, max)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.Mode().Perm()&0022 != 0 {
		return nil, fmt.Errorf("local artifact must not be group or world writable")
	}
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("file exceeds size bound")
	}
	return data, nil
}

type localLocatorSpan struct {
	field      string
	start, end int
}

var localPolicyFields = map[string]bool{
	"contract": true, "library_path": true, "library_sha256": true,
	"header_path": true, "header_sha256": true, "build_manifest_path": true,
	"build_manifest_sha256": true, "expected_abi_profile": true,
	"expected_abi_version": true, "build_profile": true, "required_capabilities": true,
}

func decodePinnedLocalDevelopmentPolicy(data []byte, pin string) (LocalDevelopmentPolicy, error) {
	var p LocalDevelopmentPolicy
	if !shaPattern.MatchString(pin) || len(data) == 0 || len(data) > maxManifestBytes {
		return p, fmt.Errorf("external policy pin or byte budget is invalid")
	}
	if nativeSHA256Hex(data) != pin {
		return p, fmt.Errorf("local development policy SHA256 mismatch")
	}
	if err := decodeStrictJSON(data, &p); err != nil {
		return p, err
	}
	if _, err := localPolicyLocatorSpans(data); err != nil {
		return p, err
	}
	return p, validateLocalDevelopmentPolicy(p)
}

// Called only after strict JSON decode, including duplicate-key rejection. Exact
// field spelling also rejects encoding/json's case-insensitive struct aliases.
func localPolicyLocatorSpans(data []byte) ([]localLocatorSpan, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("local policy must be an object")
	}
	seen := map[string]bool{}
	var spans []localLocatorSpan
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok || !localPolicyFields[key] || seen[key] {
			return nil, fmt.Errorf("unknown or duplicate local policy field")
		}
		seen[key] = true
		var raw json.RawMessage
		if err := d.Decode(&raw); err != nil {
			return nil, err
		}
		if key == "library_path" || key == "header_path" || key == "build_manifest_path" {
			end := int(d.InputOffset())
			spans = append(spans, localLocatorSpan{key, end - len(raw), end})
		}
	}
	if len(seen) != len(localPolicyFields) {
		return nil, fmt.Errorf("local policy omitted a required field")
	}
	return spans, nil
}

func locatorValues(locators LocalDevelopmentLocators) map[string]string {
	return map[string]string{"library_path": locators.LibraryPath, "header_path": locators.HeaderPath, "build_manifest_path": locators.BuildManifestPath}
}

func replaceLocalLocatorTokens(data []byte, spans []localLocatorSpan, values map[string]string) []byte {
	var output []byte
	start := 0
	for _, span := range spans {
		output = append(output, data[start:span.start]...)
		token, _ := json.Marshal(values[span.field])
		output = append(output, token...)
		start = span.end
	}
	return append(output, data[start:]...)
}

func preservedLocalPolicyBytes(data []byte, spans []localLocatorSpan) []byte {
	return replaceLocalLocatorTokens(data, spans, map[string]string{"library_path": "<library_path>", "header_path": "<header_path>", "build_manifest_path": "<build_manifest_path>"})
}

// VerifyLocalDevelopmentPolicyRelocation verifies the original first, verifies
// the independently pinned output, asserts the explicit destination locators,
// and rejects any other byte change (even reformatting or capability ordering).
func VerifyLocalDevelopmentPolicyRelocation(original []byte, originalPin string, relocated []byte, relocatedPin string, expected LocalDevelopmentLocators) (LocalDevelopmentRelocation, error) {
	var proof LocalDevelopmentRelocation
	a, err := VerifyLocalDevelopmentPolicy(original, originalPin)
	if err != nil {
		return proof, err
	}
	b, err := VerifyLocalDevelopmentPolicy(relocated, relocatedPin)
	if err != nil {
		return proof, err
	}
	return proveLocalDevelopmentRelocation(original, relocated, a, b, expected)
}

func proveLocalDevelopmentRelocation(original, relocated []byte, a, b LocalDevelopmentVerification, expected LocalDevelopmentLocators) (LocalDevelopmentRelocation, error) {
	var proof LocalDevelopmentRelocation
	p := b.Policy
	if p.LibraryPath != expected.LibraryPath || p.HeaderPath != expected.HeaderPath || p.BuildManifestPath != expected.BuildManifestPath {
		return proof, localPolicyError(fmt.Errorf("relocated policy differs from explicit destination locators"))
	}
	leftSpans, _ := localPolicyLocatorSpans(original)
	rightSpans, _ := localPolicyLocatorSpans(relocated)
	left, right := preservedLocalPolicyBytes(original, leftSpans), preservedLocalPolicyBytes(relocated, rightSpans)
	if !bytes.Equal(left, right) {
		return proof, localPolicyError(fmt.Errorf("relocation changed bytes outside the three locator tokens"))
	}
	return LocalDevelopmentRelocation{Original: a, Relocated: b, PreservedBytesSHA256: nativeSHA256Hex(left)}, nil
}

// DeriveLocalDevelopmentPolicy changes only the three locator tokens in a new
// byte slice. It never writes policy/artifact files; the caller owns installation.
// Both the original and resulting artifacts must already exist and be pinned.
func DeriveLocalDevelopmentPolicy(original []byte, originalPin string, destination LocalDevelopmentLocators) ([]byte, LocalDevelopmentRelocation, error) {
	a, err := VerifyLocalDevelopmentPolicy(original, originalPin)
	if err != nil {
		return nil, LocalDevelopmentRelocation{}, err
	}
	spans, _ := localPolicyLocatorSpans(original)
	output := replaceLocalLocatorTokens(original, spans, locatorValues(destination))
	b, err := VerifyLocalDevelopmentPolicy(output, nativeSHA256Hex(output))
	if err != nil {
		return nil, LocalDevelopmentRelocation{}, err
	}
	proof, err := proveLocalDevelopmentRelocation(original, output, a, b, destination)
	if err != nil {
		return nil, LocalDevelopmentRelocation{}, err
	}
	return output, proof, nil
}
