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
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"metadata.lua":                  "PLUGIN = {}\nPLUGIN.name = \"example\"\nPLUGIN.version = \"1.0.0\"\nPLUGIN.homepage = \"https://example.com\"\nPLUGIN.license = \"Apache 2.0\"\nPLUGIN.description = \"Example\"\nPLUGIN.notes = {}\nPLUGIN.legacyFilenames = {\".example-version\"}\n",
		"hooks/available.lua":           "function PLUGIN:Available(ctx) return {} end\n",
		"hooks/pre_install.lua":         "function PLUGIN:PreInstall(ctx) return {} end\n",
		"hooks/env_keys.lua":            "function PLUGIN:EnvKeys(ctx) return {} end\n",
		"lib/data.json":                 "{\"resource\":true}\n",
		"LICENSE":                       "Example license\n",
		"README.md":                     "Development documentation\n",
		"Injection.lua":                 "this is an IDE hint, not runtime code",
		".github/workflows/publish.yml": "development workflow\n",
	}
	for name, data := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestBuildRejectsVersionMismatch(t *testing.T) {
	_, err := Build(fixture(t), t.TempDir(), "version-fox/vfox-example", "1.0.1")
	if err == nil || !strings.Contains(err.Error(), "metadata version") {
		t.Fatalf("expected metadata/tag mismatch to fail, got %v", err)
	}
}

func TestBuildDeterministicAndConsistent(t *testing.T) {
	root := fixture(t)
	first, err := Build(root, t.TempDir(), "version-fox/vfox-example", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	second, err := Build(root, t.TempDir(), "version-fox/vfox-example", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := os.ReadFile(first.Archive)
	b, _ := os.ReadFile(second.Archive)
	if !bytes.Equal(a, b) {
		t.Fatal("rebuilding identical source changed the archive")
	}
	zr, err := zip.OpenReader(first.Archive)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	files := map[string]bool{}
	for _, f := range zr.File {
		files[f.Name] = true
	}
	for _, name := range []string{"metadata.lua", "hooks/available.lua", "lib/data.json", "LICENSE"} {
		if !files[name] {
			t.Errorf("runtime file missing: %s", name)
		}
	}
	for _, name := range []string{"README.md", "Injection.lua", ".github/workflows/publish.yml"} {
		if files[name] {
			t.Errorf("development file included: %s", name)
		}
	}
	data, _ := os.ReadFile(first.Manifest)
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest["version"] != "1.0.0" || manifest["downloadUrl"] != "https://github.com/version-fox/vfox-example/releases/download/v1.0.0/vfox-example-1.0.0.zip" {
		t.Fatalf("inconsistent manifest: %s", data)
	}
	if notes, ok := manifest["notes"].([]any); !ok || len(notes) != 0 {
		t.Fatalf("notes must remain an array: %s", data)
	}
	if manifest["sha256"] != first.SHA256 {
		t.Fatal("manifest checksum differs from archive")
	}
}

func TestValidationRejectsBrokenPlugin(t *testing.T) {
	for _, tc := range []struct {
		name, file, content string
		remove              bool
	}{
		{"missing hook", "hooks/pre_install.lua", "", true},
		{"invalid syntax", "hooks/available.lua", "function broken(", false},
		{"missing method", "hooks/available.lua", "-- function PLUGIN:Available(ctx) end\nreturn {}", false},
		{"invalid metadata", "metadata.lua", "PLUGIN = {version = '1.0.0'}", false},
		{"ambiguous entrypoint", "main.lua", "PLUGIN = {}", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := fixture(t)
			path := filepath.Join(root, tc.file)
			if tc.remove {
				_ = os.Remove(path)
			} else {
				_ = os.WriteFile(path, []byte(tc.content), 0644)
			}
			if _, err := Build(root, t.TempDir(), "version-fox/vfox-example", ""); err == nil {
				t.Fatal("invalid plugin accepted")
			}
		})
	}
}

func TestBuildRejectsSymlink(t *testing.T) {
	root := fixture(t)
	if err := os.Symlink(filepath.Join(root, "metadata.lua"), filepath.Join(root, "lib/link.lua")); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(root, t.TempDir(), "version-fox/vfox-example", ""); err == nil {
		t.Fatal("symlink accepted")
	}
}

func TestUpdateVersionPreservesOtherMetadata(t *testing.T) {
	root := fixture(t)
	if err := UpdateVersion(root, "1.2.3"); err != nil {
		t.Fatal(err)
	}
	meta, err := ReadMetadata(root)
	if err != nil {
		t.Fatal(err)
	}
	if meta["version"] != "1.2.3" || meta["name"] != "example" {
		t.Fatalf("wrong metadata: %#v", meta)
	}
	if _, err := Build(root, t.TempDir(), "version-fox/vfox-example", "1.2.3"); err != nil {
		t.Fatal(err)
	}
}

func TestStableVersionValidation(t *testing.T) {
	for _, s := range []string{"v1.2.3", "1.2", "01.2.3", "1.2.3-beta.1", "1.2.3\n", "1.2.3;echo bad"} {
		if _, err := ParseVersion(s); err == nil {
			t.Errorf("invalid version accepted: %q", s)
		}
	}
	if c, err := CompareVersions("1.10.0", "1.9.99"); err != nil || c <= 0 {
		t.Fatalf("numeric comparison failed: %d, %v", c, err)
	}
}

func TestBuildIncludesRuntimeBinaries(t *testing.T) {
	root := fixture(t)
	installer := []byte("#!/bin/sh\nexit 0\n")
	binary := []byte{'M', 'Z', 0, 255, 1}
	if err := os.MkdirAll(filepath.Join(root, "bin", "WiX"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "install"), installer, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "WiX", "dark.exe"), binary, 0644); err != nil {
		t.Fatal(err)
	}
	bundle, err := Build(root, t.TempDir(), "version-fox/vfox-example", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	second, err := Build(root, t.TempDir(), "version-fox/vfox-example", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if bundle.SHA256 != second.SHA256 {
		t.Fatal("runtime binaries made the archive nondeterministic")
	}
	archive, err := zip.OpenReader(bundle.Archive)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	want := map[string][]byte{"bin/install": installer, "bin/WiX/dark.exe": binary}
	for _, file := range archive.File {
		expected, ok := want[file.Name]
		if !ok {
			continue
		}
		r, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := io.ReadAll(r)
		closeErr := r.Close()
		if readErr != nil || closeErr != nil {
			t.Fatalf("read archive: %v, %v", readErr, closeErr)
		}
		if !bytes.Equal(data, expected) {
			t.Fatalf("runtime binary changed: %s", file.Name)
		}
		if file.Name == "bin/install" && file.Mode().Perm() != 0755 {
			t.Fatalf("installer lost its executable mode: %v", file.Mode())
		}
		if file.Name == "bin/WiX/dark.exe" && file.Mode().Perm() != 0644 {
			t.Fatalf("unexpected binary mode: %v", file.Mode())
		}
		delete(want, file.Name)
	}
	if len(want) != 0 {
		t.Fatalf("runtime helpers are missing from plugin archive: %v", want)
	}
}
