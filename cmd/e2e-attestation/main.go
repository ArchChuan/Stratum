package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/byteBuilderX/stratum/internal/platform/e2eattestation"
)

var systemPacks = []string{"dashboard", "iam", "agent", "skill", "mcp", "knowledge", "memory", "evaluation", "workflow"}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: e2e-attestation <digest|generate|verify>")
	}
	switch args[0] {
	case "digest":
		flags := flag.NewFlagSet("digest", flag.ContinueOnError)
		flags.SetOutput(stderr)
		root := flags.String("root", ".", "repository root")
		ref := flags.String("ref", "", "committed Git ref; defaults to local source")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		var digest string
		var err error
		if *ref == "" {
			digest, err = e2eattestation.LocalSourceDigest(*root)
		} else {
			digest, err = e2eattestation.CommittedSourceDigest(*root, *ref)
		}
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, digest)
		return err
	case "generate":
		flags := flag.NewFlagSet("generate", flag.ContinueOnError)
		flags.SetOutput(stderr)
		root := flags.String("root", ".", "repository root")
		inputPath := flags.String("input", "", "safe results JSON")
		outputDir := flags.String("output-dir", "test/e2e/attestations", "attestation output directory")
		manifest := flags.String("manifest", "test/e2e/stateful/manifest.json", "coverage manifest")
		profile := flags.String("profile", "", "soak acceptance profile: test or release")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *inputPath == "" {
			return errors.New("--input is required")
		}
		data, err := os.ReadFile(*inputPath)
		if err != nil {
			return fmt.Errorf("read safe results: %w", err)
		}
		results, err := e2eattestation.DecodeSafeResults(data)
		if err != nil {
			return err
		}
		if err := validateProfileFlags(results.Mode, *profile); err != nil {
			return err
		}
		if results.AcceptanceProfile != *profile {
			return fmt.Errorf("safe results profile %q does not match --profile %q", results.AcceptanceProfile, *profile)
		}
		path, _, err := e2eattestation.GenerateAttestation(*root, results, e2eattestation.GenerateOptions{
			ManifestPath: *manifest, OutputDir: *outputDir,
		})
		if err != nil {
			return err
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return fmt.Errorf("resolve attestation path: %w", err)
		}
		_, err = fmt.Fprintln(stdout, absolute)
		return err
	case "verify":
		flags := flag.NewFlagSet("verify", flag.ContinueOnError)
		flags.SetOutput(stderr)
		root := flags.String("root", ".", "repository root")
		path := flags.String("attestation", "", "attestation JSON")
		manifest := flags.String("manifest", "test/e2e/stateful/manifest.json", "coverage manifest")
		ref := flags.String("ref", "", "committed Git ref; defaults to local source")
		requiredMode := flags.String("required-mode", "short", "required execution mode: short or soak")
		requiredProfile := flags.String("required-profile", "", "required soak acceptance profile: test or release")
		packs := stringListFlag{}
		flags.Var(&packs, "required-pack", "required passing pack; repeat to override full-system defaults")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *path == "" {
			return errors.New("--attestation is required")
		}
		if *requiredMode != "short" && *requiredMode != "soak" {
			return errors.New("--required-mode must be short or soak")
		}
		if err := validateProfileFlags(*requiredMode, *requiredProfile); err != nil {
			return err
		}
		required := systemPacks
		if len(packs) > 0 {
			required = packs
		}
		return e2eattestation.VerifyAttestationFile(*root, *path, e2eattestation.VerifyOptions{
			ManifestPath: *manifest, Ref: *ref, RequiredMode: *requiredMode,
			RequiredProfile: *requiredProfile, RequiredPacks: required,
		})
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func validateProfileFlags(mode, profile string) error {
	if mode == "short" {
		if profile != "" {
			return errors.New("short mode cannot use --required-profile or --profile")
		}
		return nil
	}
	if profile != e2eattestation.AcceptanceProfileTest && profile != e2eattestation.AcceptanceProfileRelease {
		return errors.New("soak mode requires --required-profile/--profile set to test or release")
	}
	return nil
}

type stringListFlag []string

func (values *stringListFlag) String() string { return strings.Join(*values, ",") }
func (values *stringListFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("required pack cannot be empty")
	}
	*values = append(*values, value)
	return nil
}
