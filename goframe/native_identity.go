package goframe

import (
	"database/sql"

	talon "github.com/darkmice/talon-sdk-go"
)

// NativeIdentityProvider is exposed by Talon's physical database/sql connection
// inside sql.Conn.Raw. It observes only that connection's existing native handle.
// Calls do not execute SQL or start, commit, roll back, or replace transactions.
type NativeIdentityProvider interface {
	NativeInfo() (talon.NativeInfo, error)
	VerifyLocalDevelopmentPolicy([]byte, string) (talon.NativeInfo, error)
}

type nativeIdentityDB interface {
	CheckedNativeInfo() (talon.NativeInfo, error)
	VerifyLocalDevelopmentPolicy([]byte, string) (talon.NativeInfo, error)
}

func identityUnavailable() error {
	return &talon.TalonError{Code: talon.CodeCapabilityUnavailable, Operation: "native identity", Message: "connection does not expose the Talon native handle identity owner"}
}

func (c *nativeConn) NativeInfo() (talon.NativeInfo, error) {
	owner, ok := c.db.(nativeIdentityDB)
	if !ok {
		return talon.NativeInfo{}, identityUnavailable()
	}
	return owner.CheckedNativeInfo()
}

func (c *nativeConn) VerifyLocalDevelopmentPolicy(data []byte, pin string) (talon.NativeInfo, error) {
	owner, ok := c.db.(nativeIdentityDB)
	if !ok {
		return talon.NativeInfo{}, identityUnavailable()
	}
	return owner.VerifyLocalDevelopmentPolicy(data, pin)
}

// ConnNativeInfo reads the existing held connection, without acquiring a pool
// connection or opening another handle. The caller owns the sql.Conn lifetime.
func ConnNativeInfo(conn *sql.Conn) (talon.NativeInfo, error) {
	return withNativeIdentity(conn, func(owner NativeIdentityProvider) (talon.NativeInfo, error) { return owner.NativeInfo() })
}

// VerifyConnLocalDevelopmentPolicy binds the external bytes/pin to the actual
// loader-attested native identity of this held SQL connection. It can run while
// a transaction on that same connection owns Core's single transaction slot.
func VerifyConnLocalDevelopmentPolicy(conn *sql.Conn, data []byte, pin string) (talon.NativeInfo, error) {
	return withNativeIdentity(conn, func(owner NativeIdentityProvider) (talon.NativeInfo, error) {
		return owner.VerifyLocalDevelopmentPolicy(data, pin)
	})
}

func withNativeIdentity(conn *sql.Conn, read func(NativeIdentityProvider) (talon.NativeInfo, error)) (talon.NativeInfo, error) {
	var info talon.NativeInfo
	if conn == nil {
		return info, &talon.TalonError{Code: talon.CodeInvalidArgument, Operation: "native identity", Message: "an existing SQL connection is required"}
	}
	err := conn.Raw(func(raw interface{}) error {
		owner, ok := raw.(NativeIdentityProvider)
		if !ok {
			return identityUnavailable()
		}
		var err error
		info, err = read(owner)
		return err
	})
	return info, err
}

var _ NativeIdentityProvider = (*nativeConn)(nil)
