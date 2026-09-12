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
	"fmt"
	"os"
	"path"
	"strings"
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

func TestGitHubLookupDraftAfterPublishedEndpoint404(t *testing.T) {
	store := GitHub{Repository: "owner/plugin", run: func(args ...string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "/releases/tags/") {
			return nil, fmt.Errorf("gh: Not Found (HTTP 404)")
		}
		return []byte(`[[{"id":1,"tag_name":"v0.9.0","draft":false}], [{"id":2,"tag_name":"v1.0.0","draft":true,"assets":[{"id":3,"name":"plugin.zip"}]}]]`), nil
	}}
	release, err := store.Lookup("v1.0.0")
	if err != nil || release == nil || !release.Draft || release.ID != 2 || len(release.Assets) != 1 {
		t.Fatalf("draft release was not recovered: %#v, %v", release, err)
	}
	missing, err := store.Lookup("v9.0.0")
	if err != nil || missing != nil {
		t.Fatalf("missing release: %#v, %v", missing, err)
	}
}

func TestGitHubCreateFindsNewDraft(t *testing.T) {
	created := false
	store := GitHub{Repository: "owner/plugin", run: func(args ...string) ([]byte, error) {
		if len(args) >= 2 && args[0] == "release" && args[1] == "create" {
			created = true
			return []byte("https://github.com/owner/plugin/releases/tag/v1.0.0"), nil
		}
		if strings.Contains(strings.Join(args, " "), "/releases/tags/") {
			return nil, fmt.Errorf("gh: Not Found (HTTP 404)")
		}
		if !created {
			t.Fatal("release lookup occurred before creation")
		}
		return []byte(`[[{"id":2,"tag_name":"v1.0.0","draft":true,"assets":[]}]]`), nil
	}}
	release, err := store.Create("v1.0.0", strings.Repeat("a", 40))
	if err != nil || release == nil || !release.Draft {
		t.Fatalf("created draft was not found: %#v, %v", release, err)
	}
}

func TestGitHubLookupPropagatesAPIErrors(t *testing.T) {
	for _, message := range []string{"HTTP 403", "HTTP 500", "invalid JSON"} {
		t.Run(message, func(t *testing.T) {
			store := GitHub{Repository: "owner/plugin", run: func(args ...string) ([]byte, error) {
				if message == "invalid JSON" {
					return []byte("bad response"), nil
				}
				return nil, fmt.Errorf("gh: %s", message)
			}}
			if _, err := store.Lookup("v1.0.0"); err == nil {
				t.Fatal("API error was treated as a missing release")
			}
		})
	}
}

// This probe only reads the explicitly selected release, including unpublished drafts.
func TestGitHubReadOnlyDraft(t *testing.T) {
	repository, tag := os.Getenv("VFOX_TEST_DRAFT_REPOSITORY"), os.Getenv("VFOX_TEST_DRAFT_TAG")
	if repository == "" || tag == "" {
		t.Skip("set VFOX_TEST_DRAFT_REPOSITORY and VFOX_TEST_DRAFT_TAG")
	}
	release, err := (GitHub{Repository: repository}).Lookup(tag)
	if err != nil || release == nil || !release.Draft || release.TagName != tag {
		t.Fatalf("draft lookup failed: %#v, %v", release, err)
	}
}
