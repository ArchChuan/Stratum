//go:build ignore

// Command code-quality-ratchet calculates deterministic per-function Go metrics.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"sort"
)

type functionMetrics struct {
	ID         string `json:"id"`
	File       string `json:"file"`
	Name       string `json:"name"`
	Cyclomatic int    `json:"cyclomatic"`
	Cognitive  int    `json:"cognitive"`
	Lines      int    `json:"lines"`
	MaxNesting int    `json:"max_nesting"`
	Params     int    `json:"params"`
	BodyHash   string `json:"body_hash"`
}

type counter struct {
	cyclomatic int
	cognitive  int
	maxNesting int
}

type baseline struct {
	Version   int               `json:"version"`
	Functions []functionMetrics `json:"functions"`
}

type commandOptions struct {
	root     string
	baseline string
	files    []string
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "code-quality-ratchet:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: code-quality-ratchet <scan|check|refresh> [options] FILE...")
	}
	options, err := parseOptions(args[0], args[1:])
	if err != nil {
		return err
	}
	metrics, err := scanFiles(options.root, options.files)
	if err != nil {
		return err
	}
	switch args[0] {
	case "scan":
		return writeJSON(os.Stdout, metrics)
	case "refresh":
		if options.baseline == "" {
			return errors.New("refresh requires --baseline")
		}
		return writeBaseline(options.baseline, metrics)
	case "check":
		if options.baseline == "" {
			return errors.New("check requires --baseline")
		}
		stored, err := readBaseline(options.baseline)
		if err != nil {
			return err
		}
		return compareMetrics(stored.Functions, metrics)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func parseOptions(command string, args []string) (commandOptions, error) {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	root := flags.String("root", ".", "repository root")
	baselinePath := flags.String("baseline", "", "baseline JSON path")
	if err := flags.Parse(args); err != nil {
		return commandOptions{}, err
	}
	if flags.NArg() == 0 {
		return commandOptions{}, fmt.Errorf("%s requires at least one file", command)
	}
	return commandOptions{root: *root, baseline: *baselinePath, files: flags.Args()}, nil
}

func writeJSON(file *os.File, value any) error {
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func writeBaseline(path string, metrics []functionMetrics) error {
	debt := make([]functionMetrics, 0)
	for _, metric := range metrics {
		if exceedsBlockingTarget(metric) {
			debt = append(debt, metric)
		}
	}
	directory := filepath.Dir(path)
	file, err := os.CreateTemp(directory, ".code-quality-baseline-*")
	if err != nil {
		return fmt.Errorf("create baseline: %w", err)
	}
	temporaryPath := file.Name()
	defer os.Remove(temporaryPath)
	if err := writeJSON(file, baseline{Version: 1, Functions: debt}); err != nil {
		file.Close()
		return fmt.Errorf("write baseline: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close baseline: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("install baseline: %w", err)
	}
	return nil
}

func exceedsBlockingTarget(metric functionMetrics) bool {
	return metric.Cyclomatic > 10 || metric.Cognitive > 15 || metric.Lines > 120 || metric.MaxNesting > 4
}

func readBaseline(path string) (baseline, error) {
	file, err := os.Open(path)
	if err != nil {
		return baseline{}, fmt.Errorf("open baseline: %w", err)
	}
	defer file.Close()
	var stored baseline
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&stored); err != nil {
		return baseline{}, fmt.Errorf("decode baseline: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return baseline{}, errors.New("decode baseline: multiple JSON values")
		}
		return baseline{}, fmt.Errorf("decode baseline trailing data: %w", err)
	}
	if stored.Version != 1 {
		return baseline{}, fmt.Errorf("unsupported baseline version %d", stored.Version)
	}
	return stored, nil
}

func compareMetrics(stored, current []functionMetrics) error {
	previous := make(map[string]functionMetrics, len(stored))
	for _, metric := range stored {
		previous[metric.ID] = metric
	}
	violations := make([]string, 0)
	for _, metric := range current {
		if metric.Params > 6 {
			fmt.Fprintf(os.Stderr, "warning: params %s: current=%d target<=6\n", metric.ID, metric.Params)
		}
		old, exists := previous[metric.ID]
		if !exists {
			appendNewViolations(&violations, metric)
			continue
		}
		appendWorsenedViolations(&violations, old, metric)
	}
	if len(violations) == 0 {
		fmt.Println("code quality ratchet passed")
		return nil
	}
	for _, violation := range violations {
		fmt.Fprintln(os.Stderr, violation)
	}
	return fmt.Errorf("%d blocking metric violation(s)", len(violations))
}

func appendNewViolations(violations *[]string, metric functionMetrics) {
	appendLimitViolation(violations, metric, "cyclomatic", 0, metric.Cyclomatic, 10, true)
	appendLimitViolation(violations, metric, "cognitive", 0, metric.Cognitive, 15, true)
	appendLimitViolation(violations, metric, "lines", 0, metric.Lines, 120, true)
	appendLimitViolation(violations, metric, "max_nesting", 0, metric.MaxNesting, 4, true)
}

func appendWorsenedViolations(violations *[]string, old, current functionMetrics) {
	appendLimitViolation(violations, current, "cyclomatic", old.Cyclomatic, current.Cyclomatic, 10,
		current.Cyclomatic > old.Cyclomatic)
	appendLimitViolation(violations, current, "cognitive", old.Cognitive, current.Cognitive, 15,
		current.Cognitive > old.Cognitive)
	appendLimitViolation(violations, current, "lines", old.Lines, current.Lines, 120,
		current.Lines > old.Lines)
	appendLimitViolation(violations, current, "max_nesting", old.MaxNesting, current.MaxNesting, 4,
		current.MaxNesting > old.MaxNesting)
}

func appendLimitViolation(
	violations *[]string,
	metric functionMetrics,
	name string,
	oldValue, currentValue, target int,
	changed bool,
) {
	if !changed || currentValue <= target {
		return
	}
	*violations = append(*violations, fmt.Sprintf(
		"%s %s: baseline=%d current=%d target=%d file=%s function=%s",
		name, metric.ID, oldValue, currentValue, target, metric.File, metric.Name,
	))
}

func scanFiles(root string, files []string) ([]functionMetrics, error) {
	fset := token.NewFileSet()
	metrics := make([]functionMetrics, 0)
	for _, name := range files {
		rel := filepath.ToSlash(filepath.Clean(name))
		if filepath.IsAbs(name) {
			var err error
			rel, err = filepath.Rel(root, name)
			if err != nil {
				return nil, fmt.Errorf("resolve %s: %w", name, err)
			}
			rel = filepath.ToSlash(rel)
		}
		path := filepath.Join(root, filepath.FromSlash(rel))
		source, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", rel, err)
		}
		file, err := parser.ParseFile(fset, path, source, parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", rel, err)
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			metric := measureFunction(fset, source, rel, function)
			metrics = append(metrics, metric)
			literalIndex := 0
			ast.Inspect(function.Body, func(node ast.Node) bool {
				literal, ok := node.(*ast.FuncLit)
				if !ok {
					return true
				}
				literalIndex++
				name := fmt.Sprintf("%s$literal%d", metric.Name, literalIndex)
				metrics = append(metrics, measureFunctionLiteral(fset, source, rel, name, literal))
				return true
			})
		}
	}
	sort.Slice(metrics, func(i, j int) bool { return metrics[i].ID < metrics[j].ID })
	return metrics, nil
}

func measureFunction(fset *token.FileSet, source []byte, file string, function *ast.FuncDecl) functionMetrics {
	name := function.Name.Name
	identityName := name
	if receiver := receiverName(function.Recv); receiver != "" {
		identityName = receiver + "." + name
	}
	values := counter{cyclomatic: 1}
	measureBlock(function.Body.List, 0, &values)
	start := fset.Position(function.Pos())
	end := fset.Position(function.End())
	bodyStart := fset.Position(function.Body.Pos()).Offset
	bodyEnd := fset.Position(function.Body.End()).Offset
	hash := sha256.Sum256(source[bodyStart:bodyEnd])
	return functionMetrics{
		ID:         file + ":" + identityName,
		File:       file,
		Name:       identityName,
		Cyclomatic: values.cyclomatic,
		Cognitive:  values.cognitive,
		Lines:      end.Line - start.Line + 1,
		MaxNesting: values.maxNesting,
		Params:     fieldCount(function.Type.Params),
		BodyHash:   hex.EncodeToString(hash[:]),
	}
}

func measureFunctionLiteral(
	fset *token.FileSet,
	source []byte,
	file, name string,
	function *ast.FuncLit,
) functionMetrics {
	values := counter{cyclomatic: 1}
	measureBlock(function.Body.List, 0, &values)
	start := fset.Position(function.Pos())
	end := fset.Position(function.End())
	bodyStart := fset.Position(function.Body.Pos()).Offset
	bodyEnd := fset.Position(function.Body.End()).Offset
	hash := sha256.Sum256(source[bodyStart:bodyEnd])
	return functionMetrics{
		ID:         file + ":" + name,
		File:       file,
		Name:       name,
		Cyclomatic: values.cyclomatic,
		Cognitive:  values.cognitive,
		Lines:      end.Line - start.Line + 1,
		MaxNesting: values.maxNesting,
		Params:     fieldCount(function.Type.Params),
		BodyHash:   hex.EncodeToString(hash[:]),
	}
}

func measureBlock(statements []ast.Stmt, nesting int, values *counter) {
	for _, statement := range statements {
		measureStatement(statement, nesting, values)
	}
}

func measureStatement(statement ast.Stmt, nesting int, values *counter) {
	switch node := statement.(type) {
	case *ast.IfStmt:
		addDecision(values, nesting)
		measureExpr(node.Cond, values)
		measureBlock(node.Body.List, nesting+1, values)
		if node.Else != nil {
			if block, ok := node.Else.(*ast.BlockStmt); ok {
				measureBlock(block.List, nesting+1, values)
			} else {
				measureStatement(node.Else, nesting, values)
			}
		}
	case *ast.ForStmt:
		addDecision(values, nesting)
		measureExpr(node.Cond, values)
		measureBlock(node.Body.List, nesting+1, values)
	case *ast.RangeStmt:
		addDecision(values, nesting)
		measureBlock(node.Body.List, nesting+1, values)
	case *ast.SwitchStmt:
		measureCases(node.Body.List, nesting, values)
	case *ast.TypeSwitchStmt:
		measureCases(node.Body.List, nesting, values)
	case *ast.SelectStmt:
		measureCommunicationClauses(node.Body.List, nesting, values)
	case *ast.BlockStmt:
		measureBlock(node.List, nesting, values)
	case *ast.LabeledStmt:
		measureStatement(node.Stmt, nesting, values)
	case *ast.ExprStmt:
		measureExpr(node.X, values)
	case *ast.AssignStmt:
		for _, expression := range node.Rhs {
			measureExpr(expression, values)
		}
	case *ast.ReturnStmt:
		for _, expression := range node.Results {
			measureExpr(expression, values)
		}
	case *ast.GoStmt:
		measureExpr(node.Call, values)
	case *ast.DeferStmt:
		measureExpr(node.Call, values)
	}
}

func measureCases(statements []ast.Stmt, nesting int, values *counter) {
	for _, statement := range statements {
		clause, ok := statement.(*ast.CaseClause)
		if !ok {
			continue
		}
		if clause.List != nil {
			addDecision(values, nesting)
		}
		measureBlock(clause.Body, nesting+1, values)
	}
}

func measureCommunicationClauses(statements []ast.Stmt, nesting int, values *counter) {
	for _, statement := range statements {
		clause, ok := statement.(*ast.CommClause)
		if !ok {
			continue
		}
		addDecision(values, nesting)
		measureBlock(clause.Body, nesting+1, values)
	}
}

func addDecision(values *counter, nesting int) {
	values.cyclomatic++
	values.cognitive += 1 + nesting
	if nesting+1 > values.maxNesting {
		values.maxNesting = nesting + 1
	}
}

func measureExpr(expression ast.Expr, values *counter) {
	if expression == nil {
		return
	}
	ast.Inspect(expression, func(node ast.Node) bool {
		binary, ok := node.(*ast.BinaryExpr)
		if ok && (binary.Op == token.LAND || binary.Op == token.LOR) {
			values.cyclomatic++
			values.cognitive++
		}
		return true
	})
}

func receiverName(fields *ast.FieldList) string {
	if fields == nil || len(fields.List) == 0 {
		return ""
	}
	expression := fields.List[0].Type
	if star, ok := expression.(*ast.StarExpr); ok {
		expression = star.X
	}
	if index, ok := expression.(*ast.IndexExpr); ok {
		expression = index.X
	}
	if index, ok := expression.(*ast.IndexListExpr); ok {
		expression = index.X
	}
	if identifier, ok := expression.(*ast.Ident); ok {
		return identifier.Name
	}
	return "receiver"
}

func fieldCount(fields *ast.FieldList) int {
	if fields == nil {
		return 0
	}
	count := 0
	for _, field := range fields.List {
		if len(field.Names) == 0 {
			count++
		} else {
			count += len(field.Names)
		}
	}
	return count
}
