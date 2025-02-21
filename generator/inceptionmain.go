/**
 *  Copyright 2014 Paul Querna
 *
 *  Licensed under the Apache License, Version 2.0 (the "License");
 *  you may not use this file except in compliance with the License.
 *  You may obtain a copy of the License at
 *
 *      http://www.apache.org/licenses/LICENSE-2.0
 *
 *  Unless required by applicable law or agreed to in writing, software
 *  distributed under the License is distributed on an "AS IS" BASIS,
 *  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 *  See the License for the specific language governing permissions and
 *  limitations under the License.
 *
 */

package generator

import (
	"bytes"
	"errors"
	"fmt"
	"go/format"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/maxproc/ffjson/shared"
)

const inceptionMainTemplate = `
// DO NOT EDIT!
// Code generated by ffjson <https://github.com/maxproc/ffjson>
// DO NOT EDIT!

package main

import (
	"github.com/maxproc/ffjson/inception"
	importedinceptionpackage "{{.ImportName}}"
)

func main() {
	i := ffjsoninception.NewInception("{{.InputPath}}", "{{.PackageName}}", "{{.OutputPath}}", {{.ResetFields}})
	i.AddMany(importedinceptionpackage.FFJSONExpose())
	i.Execute()
}
`

const ffjsonExposeTemplate = `
// Code generated by ffjson <https://github.com/maxproc/ffjson>
//
// This should be automatically deleted by running 'ffjson',
// if leftover, please delete it.

package {{.PackageName}}

import (
	ffjsonshared "github.com/maxproc/ffjson/shared"
)

func FFJSONExpose() []ffjsonshared.InceptionType {
	rv := make([]ffjsonshared.InceptionType, 0)
{{range .StructNames}}
	rv = append(rv, ffjsonshared.InceptionType{Obj: {{.Name}}{}, Options: ffjson{{printf "%#v" .Options}} } )
{{end}}
	return rv
}
`

type structName struct {
	Name    string
	Options shared.StructOptions
}

type templateCtx struct {
	StructNames []structName
	ImportName  string
	PackageName string
	InputPath   string
	OutputPath  string
	ResetFields bool
}

type InceptionMain struct {
	goCmd        string
	inputPath    string
	exposePath   string
	outputPath   string
	TempMainPath string
	tempDir      string
	tempMain     *os.File
	tempExpose   *os.File
	resetFields  bool
}

func NewInceptionMain(goCmd string, inputPath string, outputPath string, resetFields bool) *InceptionMain {
	exposePath := getExposePath(inputPath)
	return &InceptionMain{
		goCmd:       goCmd,
		inputPath:   inputPath,
		outputPath:  outputPath,
		exposePath:  exposePath,
		resetFields: resetFields,
	}
}

func getImportName(goCmd, inputPath string) (string, error) {
	p, err := filepath.Abs(inputPath)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(p)

	// `go list dir` gives back the module name
	// Should work for GOPATH as well as with modules
	// Errors if no go files are found
	cmd := exec.Command(goCmd, "list", dir)
	b, err := cmd.Output()
	if err == nil {
		return string(b[:len(b)-1]), nil
	}

	gopaths := strings.Split(os.Getenv("GOPATH"), string(os.PathListSeparator))

	for _, path := range gopaths {
		gpath, err := filepath.Abs(path)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(filepath.ToSlash(gpath), dir)
		if err != nil {
			return "", err
		}

		if len(rel) < 4 || rel[:4] != "src"+string(os.PathSeparator) {
			continue
		}
		return rel[4:], nil
	}
	return "", errors.New(fmt.Sprintf("Could not find source directory: GOPATH=%q REL=%q", gopaths, dir))

}

func getExposePath(inputPath string) string {
	return inputPath[0:len(inputPath)-3] + "_ffjson_expose.go"
}

func (im *InceptionMain) renderTpl(f *os.File, t *template.Template, tc *templateCtx) error {
	buf := new(bytes.Buffer)
	err := t.Execute(buf, tc)
	if err != nil {
		return err
	}
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return err
	}
	_, err = f.Write(formatted)
	return err
}

func (im *InceptionMain) Generate(packageName string, si []*StructInfo, importName string) error {
	var err error
	if importName == "" {
		importName, err = getImportName(im.goCmd, im.inputPath)
		if err != nil {
			return err
		}
	}

	im.tempDir, err = ioutil.TempDir(filepath.Dir(im.inputPath), "ffjson-inception")
	if err != nil {
		return err
	}

	importName = filepath.ToSlash(importName)
	// for `go run` to work, we must have a file ending in ".go".
	im.tempMain, err = TempFileWithPostfix(im.tempDir, "ffjson-inception", ".go")
	if err != nil {
		return err
	}

	im.TempMainPath = im.tempMain.Name()
	sn := make([]structName, len(si))
	for i, st := range si {
		sn[i].Name = st.Name
		sn[i].Options = st.Options
	}

	tc := &templateCtx{
		ImportName:  importName,
		PackageName: packageName,
		StructNames: sn,
		InputPath:   im.inputPath,
		OutputPath:  im.outputPath,
		ResetFields: im.resetFields,
	}

	t := template.Must(template.New("inception.go").Parse(inceptionMainTemplate))

	err = im.renderTpl(im.tempMain, t, tc)
	if err != nil {
		return err
	}

	im.tempExpose, err = os.Create(im.exposePath)
	if err != nil {
		return err
	}

	t = template.Must(template.New("ffjson_expose.go").Parse(ffjsonExposeTemplate))

	err = im.renderTpl(im.tempExpose, t, tc)
	if err != nil {
		return err
	}

	return nil
}

func (im *InceptionMain) Run() error {
	var out bytes.Buffer
	var errOut bytes.Buffer

	cmd := exec.Command(im.goCmd, "run", "-a", im.TempMainPath)
	cmd.Stdout = &out
	cmd.Stderr = &errOut

	err := cmd.Run()

	if err != nil {
		return errors.New(
			fmt.Sprintf("Go Run Failed for: %s\nSTDOUT:\n%s\nSTDERR:\n%s\n",
				im.TempMainPath,
				string(out.Bytes()),
				string(errOut.Bytes())))
	}

	defer func() {
		if im.tempExpose != nil {
			im.tempExpose.Close()
		}

		if im.tempMain != nil {
			im.tempMain.Close()
		}

		os.Remove(im.TempMainPath)
		os.Remove(im.exposePath)
		os.Remove(im.tempDir)
	}()

	return nil
}
