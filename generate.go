package main

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"log"
	"os"
	"path/filepath"
	"strings"

	ignore "github.com/sabhiram/go-gitignore"

	"aletheia.icu/broccoli/fs"
)

// Generator collects the necessary info about the package and
// bundles the provided assets according to provided flags.
type Generator struct {
	pkg *Package

	inputFiles   []string // list of source dirs
	includeGlob  string   // files to be included
	excludeGlob  string   // files to be excluded
	useGitignore bool     // .gitignore files will be parsed
	quality      int      // compression level (1-11)
}

const template = `%s
package %s

import "aletheia.icu/broccoli/fs"

var %s = fs.New(%t, []byte(%q))
`

type wildcards []wildcard

func (w wildcards) test(path string, info os.FileInfo) bool {
	for _, card := range w {
		if !card.test(info) {
			if *verbose {
				log.Println("ignoring", path)
			}
			return false
		}
	}

	return true
}

func (g *Generator) generate() ([]byte, error) {
	var (
		files []*fs.File
		cards wildcards
		state = map[string]bool{}

		total int64
	)

	if g.includeGlob != "" {
		cards = append(cards, wildcardFrom(true, g.includeGlob))
	} else if g.excludeGlob != "" {
		cards = append(cards, wildcardFrom(false, g.excludeGlob))
	}

	if g.useGitignore {
		ignores, err := g.parseGitignores()
		if err != nil {
			return nil, fmt.Errorf("cannot open .gitignore: %w", err)
		}
		cards = append(cards, ignores...)
	}

	for _, input := range g.inputFiles {
		info, err := os.Stat(input)
		if err != nil {
			return nil, fmt.Errorf("file or directory %s not found", input)
		}

		var f *fs.File
		if !info.IsDir() {
			if _, ok := state[input]; ok {
				return nil, fmt.Errorf("duplicate path in the input: %s", input)
			}
			state[input] = true

			f, err = fs.NewFile(input)
			if err != nil {
				return nil, fmt.Errorf("cannot open file or directory: %w", err)
			}

			total += f.Fsize
			files = append(files, f)
			continue
		}

		err = filepath.Walk(input, func(path string, info os.FileInfo, _ error) error {
			if !cards.test(path, info) {
				return nil
			}

			f, err := fs.NewFile(path)
			if err != nil {
				return err
			}
			if _, ok := state[path]; ok {
				return fmt.Errorf("duplicate path in the input: %s", path)
			}

			total += f.Fsize
			state[path] = true
			files = append(files, f)
			return nil
		})

		if err != nil {
			return nil, fmt.Errorf("cannot open file or directory: %w", err)
		}
	}

	if *verbose {
		log.Println("total bytes read:", total)
	}

	bundle, err := fs.Pack(files, g.quality)
	if err != nil {
		return nil, fmt.Errorf("could not compress the input: %w", err)
	}

	if *verbose {
		log.Println("total bytes compressed:", len(bundle))
	}

	return bundle, nil
}

type wildcard interface {
	test(os.FileInfo) bool
}

type includeWildcard struct {
	include  bool
	patterns []string
}

func (w includeWildcard) test(info os.FileInfo) bool {
	if info.IsDir() {
		return true
	}

	pass := !w.include
	for _, pattern := range w.patterns {
		match, err := filepath.Match(pattern, info.Name())
		if err != nil {
			log.Fatal("invalid wildcard:", pattern)
		}

		if match {
			pass = w.include
			break
		}
	}

	return pass
}

func wildcardFrom(include bool, patterns string) wildcard {
	w := strings.Split(patterns, ",")
	for i, v := range w {
		w[i] = strings.Trim(v, ` "`)
	}

	return includeWildcard{include, w}
}

type gitignoreWildcard struct {
	ign *ignore.GitIgnore
}

func (w gitignoreWildcard) test(info os.FileInfo) bool {
	return !w.ign.MatchesPath(info.Name())
}

func (g *Generator) parseGitignores() (cards []wildcard, err error) {
	err = filepath.Walk(".", func(path string, info os.FileInfo, _ error) error {
		if !info.IsDir() && info.Name() == ".gitignore" {
			ign, err := ignore.CompileIgnoreFile(path)
			if err != nil {
				return err
			}
			cards = append(cards, gitignoreWildcard{ign: ign})
		}
		return nil
	})
	return
}

// Package holds information about a Go package
type Package struct {
	dir      string
	name     string
	defs     map[*ast.Ident]types.Object
	typesPkg *types.Package
}

func (g *Generator) parsePackage() {
	pkg, err := build.Default.ImportDir(".", 0)
	if err != nil {
		log.Fatalln("cannot parse package:", err)
	}

	var names []string
	names = append(names, pkg.GoFiles...)
	names = append(names, pkg.CgoFiles...)
	names = append(names, pkg.SFiles...)

	var astFiles []*ast.File
	g.pkg = new(Package)
	set := token.NewFileSet()
	for _, name := range names {
		if !strings.HasSuffix(name, ".go") {
			continue
		}
		parsedFile, err := parser.ParseFile(set, name, nil, parser.ParseComments)
		if err != nil {
			log.Fatalf("parsing package: %s: %s\n", name, err)
		}
		astFiles = append(astFiles, parsedFile)
	}
	if len(astFiles) == 0 {
		log.Fatalln("no buildable Go files")
	}
	g.pkg.name = astFiles[0].Name.Name
	g.pkg.dir = "."

	// Type check the package.
	g.pkg.check(set, astFiles)
}

// check type-checks the package.
func (pkg *Package) check(fs *token.FileSet, astFiles []*ast.File) {
	pkg.defs = make(map[*ast.Ident]types.Object)
	config := types.Config{Importer: defaultImporter(), FakeImportC: true}
	info := &types.Info{
		Defs: pkg.defs,
	}
	typesPkg, err := config.Check(pkg.dir, fs, astFiles, info)
	if err != nil {
		log.Println("checking package:", err)
		log.Println("proceeding anyway...")
	}

	pkg.typesPkg = typesPkg
}

func defaultImporter() types.Importer {
	return importer.For("source", nil)
}
