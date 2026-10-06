// talon-native-policy observes explicitly pinned local policy files without a DB.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	talon "github.com/darkmice/talon-sdk-go"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "talon-native-policy: code=%s error=%v\n", talon.ErrorCodeOf(err), err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 || (args[0] != "verify" && args[0] != "attest") {
		return fmt.Errorf("usage: talon-native-policy verify|attest --policy ABS --sha256 EXTERNAL_PIN; attest executes selected unsigned local code")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	file := flags.String("policy", "", "canonical absolute policy file path")
	pin := flags.String("sha256", "", "external SHA256 of original policy bytes")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	var r talon.LocalDevelopmentVerification
	var err error
	if args[0] == "attest" {
		r, err = talon.AttestLocalDevelopmentPolicyFile(*file, *pin)
	} else {
		r, err = talon.VerifyLocalDevelopmentPolicyFile(*file, *pin)
	}
	if err != nil {
		return err
	}
	// Never print raw policy, trust keys, or capability claims as release admission.
	return json.NewEncoder(os.Stdout).Encode(struct {
		Provenance    string `json:"provenance"`
		Stage         string `json:"stage"`
		PolicySHA256  string `json:"policy_sha256"`
		LibrarySHA256 string `json:"library_sha256"`
		CoreCommit    string `json:"core_commit"`
		ABIProfile    string `json:"abi_profile"`
		ABIVersion    int    `json:"abi_version"`
		ReleaseGate   string `json:"storage_conditional_batch_v1"`
	}{r.Provenance, r.Stage, r.PolicySHA256, r.Info.LibrarySHA256, r.Info.CoreCommit, r.Info.ABIProfile, r.Info.ABIVersion, r.Info.Gates["storage_conditional_batch_v1"].Status})
}
