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
	"os"
	"path"
	"testing"
)

// Opt-in protocol check for the real gh adapter. This test only reads published
// data and cannot create, edit or upload any release.
func TestGitHubReadOnlyProtocol(t *testing.T) {
	repository := os.Getenv("VFOX_TEST_GITHUB_REPOSITORY")
	if repository == "" {
		t.Skip("set VFOX_TEST_GITHUB_REPOSITORY for the read-only GitHub protocol check")
	}
	store := GitHub{Repository: repository}
	latest, err := store.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil || latest.TagName == "" {
		t.Fatal("latest release could not be decoded")
	}
	r, err := store.Lookup("manifest")
	if err != nil {
		t.Fatal(err)
	}
	asset, found := findAsset(r, "manifest.json")
	if !found {
		t.Fatal("published manifest is missing")
	}
	data, err := store.Download(asset)
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Version     string `json:"version"`
		DownloadURL string `json:"downloadUrl"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseVersion(manifest.Version); err != nil {
		t.Fatal(err)
	}
	r, err = store.Lookup("v" + manifest.Version)
	if err != nil {
		t.Fatal(err)
	}
	asset, found = findAsset(r, path.Base(manifest.DownloadURL))
	if !found {
		t.Fatal("published archive is missing")
	}
	archive, err := store.Download(asset)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) == 0 {
		t.Fatal("downloaded archive is empty")
	}
	if missing, err := store.Lookup("v0.0.0-vfox-release-read-only-probe"); err != nil || missing != nil {
		t.Fatalf("404 handling failed: %#v, %v", missing, err)
	}
}
