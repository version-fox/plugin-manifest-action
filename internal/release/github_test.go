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
		t.Fatalf("missing release handling failed: %#v, %v", missing, err)
	}
}

func TestGitHubLookupResolvesDraftAndPublishedReleases(t *testing.T) {
	for _, draft := range []bool{true, false} {
		t.Run(fmt.Sprint(draft), func(t *testing.T) {
			store := GitHub{Repository: "owner/plugin", run: func(args ...string) ([]byte, error) {
				if len(args) > 1 && args[1] == "graphql" {
					if args[len(args)-1] == "tag=v9.0.0" {
						return []byte(`{"data":{"repository":{"release":null}}}`), nil
					}
					return []byte(`{"data":{"repository":{"release":{"databaseId":2}}}}`), nil
				}
				if args[1] != "repos/owner/plugin/releases/2" {
					t.Fatalf("unexpected release read: %v", args)
				}
				return []byte(fmt.Sprintf(`{"id":2,"tag_name":"v1.0.0","draft":%t,"assets":[{"id":3,"name":"plugin.zip"}]}`, draft)), nil
			}}
			release, err := store.Lookup("v1.0.0")
			if err != nil || release == nil || release.Draft != draft || release.ID != 2 || len(release.Assets) != 1 {
				t.Fatalf("release was not recovered: %#v, %v", release, err)
			}
			missing, err := store.Lookup("v9.0.0")
			if err != nil || missing != nil {
				t.Fatalf("missing release: %#v, %v", missing, err)
			}
		})
	}
}

func TestGitHubCreateDoesNotRequireImmediateReadVisibility(t *testing.T) {
	for _, tag := range []string{"v1.0.0", "manifest"} {
		t.Run(tag, func(t *testing.T) {
			store := GitHub{Repository: "owner/plugin", run: func(args ...string) ([]byte, error) {
				if len(args) > 2 && args[1] == "--method" && args[2] == "POST" {
					return []byte(fmt.Sprintf(`{"id":2,"tag_name":%q,"draft":true,"assets":[]}`, tag)), nil
				}
				if len(args) > 1 && args[1] == "repos/owner/plugin/git/ref/tags/"+tag {
					return []byte(`{"ref":"refs/tags/v1.0.0"}`), nil
				}
				t.Fatalf("new draft must come from create response, not a possibly stale read: %v", args)
				return nil, nil
			}}
			release, err := store.Create(tag, strings.Repeat("a", 40))
			if err != nil || release == nil || !release.Draft || release.TagName != tag {
				t.Fatalf("created draft was not returned: %#v, %v", release, err)
			}
		})
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
