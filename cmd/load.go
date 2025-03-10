// Copyright (c) 2020 Mercari, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy of
// this software and associated documentation files (the "Software"), to deal in
// the Software without restriction, including without limitation the rights to
// use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of
// the Software, and to permit persons to whom the Software is furnished to do so,
// subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS
// FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR
// COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER
// IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN
// CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/roryq/wrench/pkg/spanner"

	"github.com/spf13/cobra"
)

const (
	dirTable      = "table"
	dirStaticData = "static_data"
	dirIndex      = "index"
)

type staticDataConfig struct {
	StaticDataTables []string
	CustomOrderBy    map[string]string
}

var loadCmd = &cobra.Command{
	Use:   "load",
	Short: "Load schema from server to file",
	RunE:  load,
}

var loadDiscreteCmd = &cobra.Command{
	Use:   "load-discrete",
	Short: "Load schema from server to discrete files per object",
	RunE:  loadDiscrete,
}

func load(c *cobra.Command, args []string) error {
	ctx := context.Background()

	client, err := newSpannerClient(ctx, c)
	if err != nil {
		return err
	}
	defer client.Close()

	ddl, err := client.LoadDDL(ctx)
	if err != nil {
		return &Error{
			err: err,
			cmd: c,
		}
	}

	err = ioutil.WriteFile(schemaFilePath(c), ddl, 0664)
	if err != nil {
		return &Error{
			err: err,
			cmd: c,
		}
	}

	return nil
}

func loadDiscrete(c *cobra.Command, args []string) error {
	ctx := context.Background()

	client, err := newSpannerClient(ctx, c)
	if err != nil {
		return err
	}
	defer client.Close()

	// load and write ddls
	ddls, err := client.LoadDDLs(ctx)
	if err != nil {
		return &Error{
			err: err,
			cmd: c,
		}
	}

	if err := clearSchemaDir(c); err != nil {
		return &Error{
			err: err,
			cmd: c,
		}
	}
	for _, ddl := range ddls {
		if err := writeDDL(ddl, schemaDirPath(c)); err != nil {
			return &Error{
				err: err,
				cmd: c,
			}
		}
	}

	// load and write static data
	config, err := readStaticDataTablesFile(staticDataTablesFilePath(c))
	if err != nil {
		return &Error{
			err: err,
			cmd: c,
		}
	}
	datas, err := client.LoadStaticDatas(ctx, config.StaticDataTables, config.CustomOrderBy)
	if err != nil {
		return &Error{
			err: err,
			cmd: c,
		}
	}
	for _, d := range datas {
		if err := writeData(d, schemaDirPath(c)); err != nil {
			return &Error{
				err: err,
				cmd: c,
			}
		}
	}

	return nil
}

func readStaticDataTablesFile(filePath string) (sdc staticDataConfig, err error) {
	filePath = path.Clean(filePath)
	if strings.HasSuffix(filePath, defaultStaticDataTablesFile) {
		// try both structured config or text file
		jsonPath := strings.ReplaceAll(filePath, defaultStaticDataTablesFile, "wrench.json")
		sdc, err = readJsonFile(jsonPath)
		if err == nil {
			return sdc, nil
		}
		txtPath := strings.ReplaceAll(filePath, defaultStaticDataTablesFile, "static_data_tables.txt")
		sdc.StaticDataTables, err = readTxtFile(txtPath)
	} else if strings.HasSuffix(filePath, ".json") {
		sdc, err = readJsonFile(filePath)
	} else if strings.HasSuffix(filePath, ".txt") {
		sdc.StaticDataTables, err = readTxtFile(filePath)
	}

	return sdc, err
}

func openFile(p string) (*os.File, error, func()) {
	f, err := os.Open(p)
	if os.IsNotExist(err) {
		return nil, nil, func() {}
	}
	if err != nil {
		return nil, err, func() {}
	}
	return f, err, func() { f.Close() }
}

func readJsonFile(filePath string) (staticDataConfig, error) {
	f, err, done := openFile(filePath)
	defer done()
	bytes, err := ioutil.ReadAll(f)
	var d staticDataConfig
	err = json.Unmarshal(bytes, &d)
	return d, err
}

func readTxtFile(filePath string) ([]string, error) {
	f, err, done := openFile(filePath)
	if err != nil {
		return []string{}, err
	}
	defer done()
	scanner := bufio.NewScanner(f)
	tables := []string{}
	for scanner.Scan() {
		tables = append(tables, scanner.Text())
	}
	return tables, nil
}

func writeDDL(ddl spanner.SchemaDDL, schemaDir string) error {
	parent := filepath.Join(schemaDir, ddl.ObjectType)
	file := filepath.Join(parent, ddl.Filename)
	if err := mkdir(parent); err != nil {
		return err
	}
	return ioutil.WriteFile(file, []byte(ddl.Statement), 0664)
}

func mkdir(parent string) error {
	_, err := os.Stat(parent)
	if os.IsNotExist(err) {
		os.MkdirAll(parent, 0700)
	} else if err != nil {
		return err
	}
	return nil
}

func writeData(data spanner.StaticData, schemaDir string) error {
	parent := filepath.Join(schemaDir, dirStaticData)
	file := filepath.Join(parent, data.ToFileName())
	if err := mkdir(parent); err != nil {
		return err
	}
	return ioutil.WriteFile(file, []byte(strings.Join(data.Statements, "\n")), 0644)
}

func schemaDirPath(c *cobra.Command) string {
	return c.Flag(flagNameDirectory).Value.String()
}

func clearSchemaDir(c *cobra.Command) error {
	tables := filepath.Join(schemaDirPath(c), dirTable)
	indexes := filepath.Join(schemaDirPath(c), dirIndex)
	staticData := filepath.Join(schemaDirPath(c), dirStaticData)

	if err := os.RemoveAll(tables); err != nil {
		return err
	}
	if err := os.RemoveAll(indexes); err != nil {
		return err
	}
	if err := os.RemoveAll(staticData); err != nil {
		return err
	}

	return nil
}
