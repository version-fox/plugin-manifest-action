/*
 *    Copyright 2026 Han Li and contributors
 *
 *    Licensed under the Apache License, Version 2.0 (the "License");
 *    you may not use this file except in compliance with the License.
 *    You may obtain a copy of the License at
 *
 *      http://www.apache.org/licenses/LICENSE-2.0
 *
 *    Unless required by applicable law or agreed to in writing, software
 *    distributed under the License is distributed on an "AS IS" BASIS,
 *    WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 *    See the License for the specific language governing permissions and
 *    limitations under the License.
 */

package release

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	lua "github.com/yuin/gopher-lua"
	"github.com/yuin/gopher-lua/ast"
	"github.com/yuin/gopher-lua/parse"
)

var stableVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
var repositoryName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
var versionAssignment = regexp.MustCompile(`(?m)^(\s*PLUGIN\.version\s*=\s*)(["'])([^"'\r\n]*)(["'])(\s*(?:--[^\r\n]*)?)$`)

type Version [3]uint64

func ParseVersion(value string) (Version, error) {
	var version Version
	if !stableVersion.MatchString(value) {
		return version, fmt.Errorf("invalid stable version %q; use X.Y.Z without v", value)
	}
	for i, part := range strings.Split(value, ".") {
		n, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return version, fmt.Errorf("invalid version: %w", err)
		}
		version[i] = n
	}
	return version, nil
}

func CompareVersions(a, b string) (int, error) {
	av, err := ParseVersion(a)
	if err != nil {
		return 0, err
	}
	bv, err := ParseVersion(b)
	if err != nil {
		return 0, err
	}
	for i := range av {
		if av[i] < bv[i] {
			return -1, nil
		}
		if av[i] > bv[i] {
			return 1, nil
		}
	}
	return 0, nil
}

// ReadMetadata uses the same Lua 5.1 implementation as vfox. Lifecycle hooks
// are syntax-checked by Build, never executed by the release tool.
func ReadMetadata(root string) (map[string]any, error) {
	if _, err := os.Stat(filepath.Join(root, "main.lua")); !os.IsNotExist(err) {
		return nil, fmt.Errorf("this workflow supports metadata.lua + hooks/ plugins; main.lua is not supported")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	state := lua.NewState()
	defer state.Close()
	state.SetContext(ctx)
	if err := state.DoFile(filepath.Join(root, "metadata.lua")); err != nil {
		return nil, fmt.Errorf("load metadata: %w", err)
	}
	table, ok := state.GetGlobal("PLUGIN").(*lua.LTable)
	if !ok {
		return nil, fmt.Errorf("metadata.lua must define the PLUGIN table")
	}
	metadata := map[string]any{}
	var conversionErr error
	table.ForEach(func(key, value lua.LValue) {
		if conversionErr != nil {
			return
		}
		name, ok := key.(lua.LString)
		if !ok {
			conversionErr = fmt.Errorf("metadata keys must be strings")
			return
		}
		converted, err := luaValue(value, string(name) == "notes" || string(name) == "legacyFilenames", map[*lua.LTable]bool{})
		if err != nil {
			conversionErr = fmt.Errorf("metadata %s: %w", name, err)
			return
		}
		metadata[string(name)] = converted
	})
	if conversionErr != nil {
		return nil, conversionErr
	}
	for _, key := range []string{"name", "version", "homepage", "license", "description"} {
		if v, ok := metadata[key].(string); !ok || strings.TrimSpace(v) == "" {
			return nil, fmt.Errorf("metadata %s must be a nonempty string", key)
		}
	}
	if _, err := ParseVersion(metadata["version"].(string)); err != nil {
		return nil, err
	}
	return metadata, nil
}

func luaValue(value lua.LValue, array bool, seen map[*lua.LTable]bool) (any, error) {
	switch v := value.(type) {
	case lua.LString:
		return string(v), nil
	case lua.LNumber:
		return float64(v), nil
	case lua.LBool:
		return bool(v), nil
	case *lua.LNilType:
		return nil, nil
	case *lua.LTable:
		if seen[v] {
			return nil, fmt.Errorf("cyclic table")
		}
		seen[v] = true
		defer delete(seen, v)
		if array || v.Len() > 0 {
			result := make([]any, 0, v.Len())
			count := 0
			v.ForEach(func(_, _ lua.LValue) { count++ })
			if count != v.Len() {
				return nil, fmt.Errorf("expected a contiguous array")
			}
			for i := 1; i <= v.Len(); i++ {
				item, err := luaValue(v.RawGetInt(i), false, seen)
				if err != nil {
					return nil, err
				}
				result = append(result, item)
			}
			return result, nil
		}
		result := map[string]any{}
		var err error
		v.ForEach(func(k, item lua.LValue) {
			if err != nil {
				return
			}
			key, ok := k.(lua.LString)
			if !ok {
				err = fmt.Errorf("object keys must be strings")
				return
			}
			result[string(key)], err = luaValue(item, false, seen)
		})
		return result, err
	default:
		return nil, fmt.Errorf("unsupported value %s", value.Type())
	}
}

func UpdateVersion(root, version string) error {
	if _, err := ParseVersion(version); err != nil {
		return err
	}
	path := filepath.Join(root, "metadata.lua")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	updated, err := replaceVersion(data, version)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, updated, 0644); err != nil {
		return err
	}
	metadata, err := ReadMetadata(root)
	if err != nil {
		return err
	}
	if metadata["version"] != version {
		return fmt.Errorf("metadata version did not update to %s", version)
	}
	return nil
}

func replaceVersion(data []byte, version string) ([]byte, error) {
	matches := versionAssignment.FindAllSubmatchIndex(data, -1)
	if len(matches) != 1 {
		return nil, fmt.Errorf("metadata.lua must contain exactly one standalone PLUGIN.version assignment")
	}
	m := matches[0]
	if data[m[4]] != data[m[8]] {
		return nil, fmt.Errorf("mismatched quotes in PLUGIN.version")
	}
	updated := append([]byte{}, data[:m[6]]...)
	updated = append(updated, version...)
	updated = append(updated, data[m[7]:]...)
	return updated, nil
}

type Bundle struct {
	Version     string
	Archive     string
	Manifest    string
	SHA256      string
	DownloadURL string
}

func Build(root, out, repository, version string) (*Bundle, error) {
	if !repositoryName.MatchString(repository) {
		return nil, fmt.Errorf("invalid owner/repository: %q", repository)
	}
	metadata, err := ReadMetadata(root)
	if err != nil {
		return nil, err
	}
	current := metadata["version"].(string)
	if version == "" {
		version = current
	}
	if _, err := ParseVersion(version); err != nil {
		return nil, err
	}
	if current != version {
		return nil, fmt.Errorf("metadata version %s does not match requested version %s", current, version)
	}
	for _, name := range []string{"available.lua", "pre_install.lua", "env_keys.lua"} {
		info, err := os.Stat(filepath.Join(root, "hooks", name))
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("required hook hooks/%s is missing", name)
		}
	}
	var files []string
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		included := rel == "metadata.lua" || rel == "LICENSE" || rel == "LICENSE.md" || rel == "LICENSE.txt" || rel == "hooks" || rel == "lib" || rel == "bin" || strings.HasPrefix(rel, "hooks/") || strings.HasPrefix(rel, "lib/") || strings.HasPrefix(rel, "bin/")
		if !included {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("runtime file %s must be a regular file", rel)
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	state := lua.NewState()
	defer state.Close()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, name := range files {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			return nil, err
		}
		if strings.HasSuffix(name, ".lua") {
			if _, err := state.Load(bytes.NewReader(data), name); err != nil {
				return nil, fmt.Errorf("Lua syntax in %s: %w", name, err)
			}
			if method := requiredMethod(name); method != "" {
				statements, err := parse.Parse(bytes.NewReader(data), name)
				if err != nil {
					return nil, err
				}
				if !definesHook(statements, method) {
					return nil, fmt.Errorf("%s must directly define PLUGIN:%s", name, method)
				}
			}
		}
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0644)
		if strings.HasPrefix(name, "bin/") {
			info, err := os.Stat(filepath.Join(root, name))
			if err != nil {
				return nil, err
			}
			if info.Mode().Perm()&0111 != 0 {
				header.SetMode(0755)
			}
		}
		header.SetModTime(time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC))
		entry, err := writer.CreateHeader(header)
		if err != nil {
			return nil, err
		}
		if _, err := entry.Write(data); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	archiveName := strings.Split(repository, "/")[1] + "-" + version + ".zip"
	downloadURL := "https://github.com/" + repository + "/releases/download/v" + version + "/" + archiveName
	sum := sha256.Sum256(buffer.Bytes())
	hash := hex.EncodeToString(sum[:])
	metadata["downloadUrl"] = downloadURL
	metadata["sha256"] = hash
	manifest, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return nil, err
	}
	manifest = append(manifest, '\n')
	if err := os.MkdirAll(out, 0755); err != nil {
		return nil, err
	}
	bundle := &Bundle{Version: version, Archive: filepath.Join(out, archiveName), Manifest: filepath.Join(out, "manifest.json"), SHA256: hash, DownloadURL: downloadURL}
	if err := os.WriteFile(bundle.Archive, buffer.Bytes(), 0644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(bundle.Manifest, manifest, 0644); err != nil {
		return nil, err
	}
	return bundle, nil
}

func requiredMethod(path string) string {
	switch path {
	case "hooks/available.lua":
		return "Available"
	case "hooks/pre_install.lua":
		return "PreInstall"
	case "hooks/env_keys.lua":
		return "EnvKeys"
	default:
		return ""
	}
}

func pluginAttribute(expr ast.Expr, method string) bool {
	attr, ok := expr.(*ast.AttrGetExpr)
	if !ok {
		return false
	}
	object, ok := attr.Object.(*ast.IdentExpr)
	if !ok || object.Value != "PLUGIN" {
		return false
	}
	key, ok := attr.Key.(*ast.StringExpr)
	return ok && key.Value == method
}

func definesHook(statements []ast.Stmt, method string) bool {
	for _, statement := range statements {
		switch stmt := statement.(type) {
		case *ast.FuncDefStmt:
			if receiver, ok := stmt.Name.Receiver.(*ast.IdentExpr); ok && receiver.Value == "PLUGIN" && stmt.Name.Method == method {
				return true
			}
			if pluginAttribute(stmt.Name.Func, method) {
				return true
			}
		case *ast.AssignStmt:
			for i, lhs := range stmt.Lhs {
				if i < len(stmt.Rhs) && pluginAttribute(lhs, method) {
					if _, ok := stmt.Rhs[i].(*ast.FunctionExpr); ok {
						return true
					}
				}
			}
		}
	}
	return false
}
