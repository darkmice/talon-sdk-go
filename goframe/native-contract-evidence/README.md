# Native SQL evidence, 2026-10-04

SDK base: `7847de0f0d1b4018fb1a4569d8e5cdc713a8f0f4`, local changes in this worktree.
All database writes are to fresh temporary test directories. Native acceptance
failures are recorded as FAIL, not converted into passing characterization tests.

- `released-v0154.txt`: `TALON_TEST_EMBEDDED_NATIVE=1 GOWORK=off CGO_ENABLED=1 go test -ldflags=-linkmode=external ./goframe -run '^TestNativeSQLConsumer' -count=1 -v`. Signed cached go-runtime v0.1.54; three strict contracts FAIL; exact values and owner tests PASS.
- `initial-unindexed-control.txt`: earlier fixture before adding consumer's operator_id secondary index. Operator equality without an index sees pending INSERT, role_id indexed equality misses. Earlier restart run also demonstrates the 0-table variant in GoFrame; table loss is not invariant across runs.
- `local-a968c4d.txt`: `TALON_TEST_LOCAL_CORE_LIBRARY=/Users/dark/WebstormProjects/talon-core/target/release/libtalon.dylib TALON_TEST_LOCAL_CORE_HEADER=/Users/dark/WebstormProjects/talon-core/include/talon.h TALON_TEST_LOCAL_CORE_BUILD_MANIFEST=/Users/dark/WebstormProjects/talon-core/target/native-build-manifest.json GOWORK=off CGO_ENABLED=1 go test -ldflags=-linkmode=external . -run '^TestLocalSignedCoreKVInterop$' -count=1 -v`. Ephemeral local test signature, not a production key. Three strict contracts FAIL; old interoperability matrix, exact values and owner contracts PASS.
- `consumer-v075.txt`: ai-platform/saas `GOWORK=off CGO_ENABLED=1 go test -ldflags=-linkmode=external ./internal/talon -run 'Test(TwoHandlesOnOneDirectoryContendForTheSingleTransactionOwner|WhetherTheSingleOwnerRefusalCoversReadsWritesOrBoth|TheOuterHandleStatementInsideATransactionCallback|OperatorAccountSQLUncommittedAssignmentIsInvisibleToTheIndexedProbe|OperatorAccountSQLUncommittedReceiptIsVisibleToTheIndexedProbe)$' -count=1 -v`. Five actual-schema characterization/owner tests PASS with the locked SDK v0.7.5. The old invisible-assignment expectation is not a healthy-contract pass.
- `components.txt`: `GOWORK=off CGO_ENABLED=1 go test -ldflags=-linkmode=external ./...`, native opt-in unset; PASS. Native contracts are skipped here.
- `purego.txt`: `CGO_ENABLED=0 GOWORK=off go test ./server ./internal/serverprotocol`; PASS.

`GOWORK=off CGO_ENABLED=1 go vet ./...` and `git diff --check` also passed.
Error-chain/uncertain-result fault injection is fake-native component evidence;
none of these logs claims that real Core fault injection produced uncertain COMMIT.
