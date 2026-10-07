package goframe

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"

	talon "github.com/darkmice/talon-sdk-go"
)

type fakeIdentityNative struct {
	fakeNative
	info                   talon.NativeInfo
	identityErr            error
	readCalls, verifyCalls int
}

func (f *fakeIdentityNative) CheckedNativeInfo() (talon.NativeInfo, error) {
	f.readCalls++
	return f.info, f.identityErr
}
func (f *fakeIdentityNative) VerifyLocalDevelopmentPolicy(_ []byte, _ string) (talon.NativeInfo, error) {
	f.verifyCalls++
	return f.info, f.identityErr
}

type identityTestConnector struct {
	conn  driver.Conn
	calls *int
}

func (c identityTestConnector) Connect(context.Context) (driver.Conn, error) {
	*c.calls++
	return c.conn, nil
}
func (c identityTestConnector) Driver() driver.Driver { return nativeDriver{} }

func TestNativeIdentityUsesHeldConnectionAndPreservesCause(t *testing.T) {
	ctx := context.Background()
	fake := &fakeIdentityNative{info: talon.NativeInfo{Admission: talon.NativeAdmissionLocalDevelopment, LibrarySHA256: "loaded"}}
	calls := 0
	pool := sql.OpenDB(identityTestConnector{&nativeConn{db: fake}, &calls})
	defer pool.Close()
	conn, err := pool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	info, err := ConnNativeInfo(conn)
	if err != nil || info.LibrarySHA256 != "loaded" {
		t.Fatal("held identity was not used")
	}
	if _, err := VerifyConnLocalDevelopmentPolicy(conn, []byte("owner input"), "external pin"); err != nil {
		t.Fatal(err)
	}
	cause := errors.New("owner rejected mismatched loaded identity")
	fake.identityErr = &talon.TalonError{Code: talon.CodeNativeVerification, Cause: cause}
	_, err = VerifyConnLocalDevelopmentPolicy(conn, nil, "")
	if talon.ErrorCodeOf(err) != talon.CodeNativeVerification || !errors.Is(err, cause) {
		t.Fatal("owner code or cause was discarded")
	}
	if calls != 1 || fake.readCalls != 1 || fake.verifyCalls != 2 || len(fake.execCalls) != 0 || len(fake.queryCalls) != 0 {
		t.Fatal("identity opened another connection or dispatched SQL")
	}
	conn.Close()
	if _, err := ConnNativeInfo(conn); !errors.Is(err, sql.ErrConnDone) {
		t.Fatal("closed connection was accepted")
	}
}

func TestNativeIdentityRefusesMissingOwner(t *testing.T) {
	if _, err := ConnNativeInfo(nil); talon.ErrorCodeOf(err) != talon.CodeInvalidArgument {
		t.Fatal("nil connection was accepted")
	}
	conn := &nativeConn{db: &fakeNative{}}
	if _, err := conn.NativeInfo(); talon.ErrorCodeOf(err) != talon.CodeCapabilityUnavailable {
		t.Fatal("missing identity owner was accepted")
	}
	if _, err := conn.VerifyLocalDevelopmentPolicy(nil, ""); talon.ErrorCodeOf(err) != talon.CodeCapabilityUnavailable {
		t.Fatal("missing verifier owner was accepted")
	}
}
