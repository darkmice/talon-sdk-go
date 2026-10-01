package talon

import (
	"os"
	"strings"

	nativecore "github.com/darkmice/talon-bin/go-runtime"
)

// defaultNativePolicy keeps an explicit environment policy authoritative. A
// partial TALON_NATIVE_* policy fails closed instead of silently using the
// bundled module. The pinned module carries a signed native release.
func defaultNativePolicy() (NativePolicy, error) {
	for _, item := range os.Environ() {
		name, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(name, "TALON_NATIVE_") {
			return NativePolicyFromEnvironment()
		}
	}
	dir, identity, err := nativecore.Materialize()
	if err != nil {
		return NativePolicy{}, err
	}
	return nativePolicyFromGoRuntime(dir, identity), nil
}

func nativePolicyFromGoRuntime(dir string, identity nativecore.Identity) NativePolicy {
	return NativePolicy{
		BundleDir:              dir,
		RuntimeOnly:            true,
		PublicKeyPEM:           []byte(identity.PublicKeyPEM),
		ExpectedKeyID:          identity.KeyID,
		ExpectedKeySHA256:      identity.KeySHA256,
		ExpectedReleaseTag:     identity.ReleaseTag,
		ExpectedTalonBinCommit: identity.TalonBinCommit,
		ExpectedCoreRepository: identity.CoreRepository,
		ExpectedCoreTag:        identity.CoreTag,
		ExpectedCoreCommit:     identity.CoreCommit,
		ExpectedCoreVersion:    identity.CoreVersion,
		ExpectedABIProfile:     identity.ABIProfile,
		ExpectedABIVersion:     identity.ABIVersion,
		ExpectedHeaderSHA256:   identity.HeaderSHA256,
	}
}
