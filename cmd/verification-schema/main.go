package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("verification-schema", flag.ContinueOnError)
	schemaPath := flags.String("schema", "", "JSON schema path")
	inputPath := flags.String("input", "", "JSON document path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *schemaPath == "" || *inputPath == "" {
		return errors.New("--schema and --input are required")
	}
	return validate(*schemaPath, *inputPath)
}

func validate(schemaPath, inputPath string) error {
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	schema, err := compiler.Compile(schemaPath)
	if err != nil {
		return fmt.Errorf("compile verification schema: %w", err)
	}
	data, err := os.ReadFile(inputPath)
	if err != nil {
		return fmt.Errorf("read verification document: %w", err)
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("decode verification document: %w", err)
	}
	if err := schema.Validate(value); err != nil {
		return fmt.Errorf("validate verification document: %w", err)
	}
	return nil
}
