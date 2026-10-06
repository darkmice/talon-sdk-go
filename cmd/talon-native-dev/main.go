// talon-native-dev prepares explicitly selected local native code without releases.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	talon "github.com/darkmice/talon-sdk-go"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "talon-native-dev:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 || args[0] != "prepare" {
		return fmt.Errorf("usage: talon-native-dev prepare --library ABS --header ABS --build-profile debug|release|unknown --out NEW_ABS_DIR [--require name@version,...]")
	}
	flags := flag.NewFlagSet("prepare", flag.ContinueOnError)
	library := flags.String("library", "", "canonical absolute path of the already-built local Core library (executes local code)")
	header := flags.String("header", "", "canonical absolute path of the matching Core C header")
	profile := flags.String("build-profile", "unknown", "caller-declared build profile; current ABI does not attest it")
	output := flags.String("out", "", "new canonical absolute config directory; existing directory is rejected")
	required := flags.String("require", "native_sql_result@2,native_sql_session@1,native_shared_core@1", "required self-attested capabilities with matching features")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	var capabilities []string
	if *required != "" {
		capabilities = strings.Split(*required, ",")
	}
	p, err := talon.PrepareLocalDevelopment(*library, *header, *profile, *output, capabilities)
	if err != nil {
		return err
	}
	fmt.Printf("Prepared unsigned local-development configuration: %s\nLibrary SHA256: %s\nDeclared build profile: %s (not Core-attested)\nSource %s/env.sh to opt in; keep release TALON_NATIVE_* fields unset.\n", *output, p.LibrarySHA256, p.BuildProfile, *output)
	return nil
}
