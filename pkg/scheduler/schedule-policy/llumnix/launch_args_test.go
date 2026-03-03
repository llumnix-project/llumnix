package llumnix

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"llm-gateway/cmd/llm-gateway/app/options"

	"github.com/spf13/pflag"
)

func TestLlumnixFlagsInApplier(t *testing.T) {
	// Create a new FlagSet
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)

	// Create Config instance and add flags
	config := &options.Config{}
	config.AddConfigFlags(flags)

	// Collect all flag names
	var flagNames []string
	flags.VisitAll(func(flag *pflag.Flag) {
		if strings.HasPrefix(flag.Name, "llumnix-") {
			flagNames = append(flagNames, flag.Name)
		}
	})
	fmt.Printf("%v", flagNames)

	// Read applier.go file content
	applierContent, err := os.ReadFile("../../../../pkg/service-controller/role/applier.go")
	if err != nil {
		t.Fatalf("Failed to read applier.go file: %v", err)
	}
	applierSource := string(applierContent)

	// Check if each flag is handled in applier.go
	unhandledFlags := []string{}
	for _, flagName := range flagNames {
		// Construct the expected format that should exist in applier.go
		expectedPattern := fmt.Sprintf("--%s=", flagName)

		// Check if applier.go contains handling for this flag
		if !strings.Contains(applierSource, expectedPattern) {
			unhandledFlags = append(unhandledFlags, flagName)
		}
	}

	// If there are unhandled flags, the test fails
	if len(unhandledFlags) > 0 {
		t.Errorf("The following llumnix flags are not handled in applier.go: %v", unhandledFlags)
	}
}
