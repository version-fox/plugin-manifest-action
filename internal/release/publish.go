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
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type RemoteAsset struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type RemoteRelease struct {
	ID      int64         `json:"id"`
	TagName string        `json:"tag_name"`
	Draft   bool          `json:"draft"`
	Assets  []RemoteAsset `json:"assets"`
}

type ReleaseStore interface {
	Lookup(tag string) (*RemoteRelease, error)
	Latest() (*RemoteRelease, error)
	Create(tag, commit string) (*RemoteRelease, error)
	Download(asset RemoteAsset) ([]byte, error)
	Upload(tag, path string, replace bool) error
	Publish(tag string, latest bool) error
}

// GitHub uses gh's existing authentication. No token is passed on the command line.
type GitHub struct{ Repository string }

func (g GitHub) Lookup(tag string) (*RemoteRelease, error) {
	return g.lookup("tags/" + tag)
}

func (g GitHub) Latest() (*RemoteRelease, error) { return g.lookup("latest") }

func (g GitHub) lookup(path string) (*RemoteRelease, error) {
	out, err := command("", "gh", "api", "repos/"+g.Repository+"/releases/"+path)
	if err != nil {
		if strings.Contains(err.Error(), "HTTP 404") {
			return nil, nil
		}
		return nil, err
	}
	var release RemoteRelease
	if err := json.Unmarshal(out, &release); err != nil {
		return nil, err
	}
	return &release, nil
}

func (g GitHub) Create(tag, commit string) (*RemoteRelease, error) {
	args := []string{"release", "create", tag, "--repo", g.Repository, "--draft", "--title", tag}
	if tag == "manifest" {
		args = append(args, "--target", commit, "--notes", "Stable plugin manifest consumed by the vfox registry.", "--latest=false")
	} else {
		args = append(args, "--verify-tag", "--generate-notes")
	}
	if _, err := command("", "gh", args...); err != nil {
		return nil, err
	}
	return g.Lookup(tag)
}

func (g GitHub) Download(asset RemoteAsset) ([]byte, error) {
	return command("", "gh", "api", fmt.Sprintf("repos/%s/releases/assets/%d", g.Repository, asset.ID), "-H", "Accept: application/octet-stream")
}

func (g GitHub) Upload(tag, path string, replace bool) error {
	args := []string{"release", "upload", tag, path, "--repo", g.Repository}
	if replace {
		args = append(args, "--clobber")
	}
	_, err := command("", "gh", args...)
	return err
}

func (g GitHub) Publish(tag string, latest bool) error {
	_, err := command("", "gh", "release", "edit", tag, "--repo", g.Repository, "--draft=false", fmt.Sprintf("--latest=%t", latest))
	return err
}

func findAsset(release *RemoteRelease, name string) (RemoteAsset, bool) {
	if release != nil {
		for _, asset := range release.Assets {
			if asset.Name == name {
				return asset, true
			}
		}
	}
	return RemoteAsset{}, false
}

// Publish uploads immutable version assets first. Only a complete, published
// version may advance the mutable manifest; older retries never move it backward.
func Publish(store ReleaseStore, bundle *Bundle, commit string) error {
	manifestRelease, err := store.Lookup("manifest")
	if err != nil {
		return err
	}
	latest := true
	// A failed replacement may have removed the mutable manifest asset. The
	// published Latest release still records the newest completed version.
	latestRelease, err := store.Latest()
	if err != nil {
		return err
	}
	if latestRelease != nil && strings.HasPrefix(latestRelease.TagName, "v") {
		latestVersion := strings.TrimPrefix(latestRelease.TagName, "v")
		if _, err := ParseVersion(latestVersion); err == nil {
			cmp, err := CompareVersions(bundle.Version, latestVersion)
			if err != nil {
				return err
			}
			latest = cmp >= 0
		}
	}
	var current []byte
	if asset, found := findAsset(manifestRelease, "manifest.json"); found {
		current, err = store.Download(asset)
		if err != nil {
			return err
		}
		var manifest struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(current, &manifest); err != nil {
			return fmt.Errorf("read current manifest: %w", err)
		}
		cmp, err := CompareVersions(bundle.Version, manifest.Version)
		if err != nil {
			return err
		}
		latest = latest && cmp >= 0
		if cmp == 0 {
			expected, err := os.ReadFile(bundle.Manifest)
			if err != nil {
				return err
			}
			if !equalJSON(current, expected) {
				return fmt.Errorf("manifest already contains different content for version %s", bundle.Version)
			}
		}
	}
	tag := "v" + bundle.Version
	versionRelease, err := store.Lookup(tag)
	if err != nil {
		return err
	}
	if versionRelease == nil {
		versionRelease, err = store.Create(tag, commit)
		if err != nil {
			return err
		}
		if versionRelease == nil {
			return fmt.Errorf("new version release was not found")
		}
	}
	for _, path := range []string{bundle.Archive, bundle.Manifest} {
		if asset, exists := findAsset(versionRelease, filepath.Base(path)); exists {
			actual, err := store.Download(asset)
			if err != nil {
				return err
			}
			expected, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if !bytes.Equal(actual, expected) {
				return fmt.Errorf("release %s already has different content for %s", tag, filepath.Base(path))
			}
		} else {
			if !versionRelease.Draft {
				return fmt.Errorf("published release %s is incomplete; its assets will not be changed", tag)
			}
			if err := store.Upload(tag, path, false); err != nil {
				return err
			}
		}
	}
	if versionRelease.Draft {
		if err := store.Publish(tag, latest); err != nil {
			return err
		}
	}
	// Re-read and download the final assets before advertising them in the manifest.
	versionRelease, err = store.Lookup(tag)
	if err != nil {
		return err
	}
	if versionRelease == nil || versionRelease.Draft {
		return fmt.Errorf("version release is not published")
	}
	for _, path := range []string{bundle.Archive, bundle.Manifest} {
		asset, exists := findAsset(versionRelease, filepath.Base(path))
		if !exists {
			return fmt.Errorf("published asset %s is missing", filepath.Base(path))
		}
		actual, err := store.Download(asset)
		if err != nil {
			return err
		}
		expected, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.Equal(actual, expected) {
			return fmt.Errorf("published asset %s did not verify", filepath.Base(path))
		}
	}
	if !latest {
		return nil
	}
	expected, err := os.ReadFile(bundle.Manifest)
	if err != nil {
		return err
	}
	if equalJSON(current, expected) && manifestRelease != nil && !manifestRelease.Draft {
		return nil
	}
	if manifestRelease == nil {
		manifestRelease, err = store.Create("manifest", commit)
		if err != nil {
			return err
		}
		if manifestRelease == nil {
			return fmt.Errorf("new manifest release was not found")
		}
	}
	if !equalJSON(current, expected) {
		_, exists := findAsset(manifestRelease, "manifest.json")
		if err := store.Upload("manifest", bundle.Manifest, exists); err != nil {
			return err
		}
	}
	if manifestRelease.Draft {
		if err := store.Publish("manifest", false); err != nil {
			return err
		}
	}
	manifestRelease, err = store.Lookup("manifest")
	if err != nil {
		return err
	}
	if manifestRelease == nil || manifestRelease.Draft {
		return fmt.Errorf("manifest release is not published")
	}
	asset, exists := findAsset(manifestRelease, "manifest.json")
	if !exists {
		return fmt.Errorf("published manifest is missing")
	}
	actual, err := store.Download(asset)
	if err != nil {
		return err
	}
	if !equalJSON(actual, expected) {
		return fmt.Errorf("published manifest did not verify")
	}
	return nil
}

func equalJSON(a, b []byte) bool {
	var first, second any
	if json.Unmarshal(a, &first) != nil || json.Unmarshal(b, &second) != nil {
		return false
	}
	x, err := json.Marshal(first)
	if err != nil {
		return false
	}
	y, err := json.Marshal(second)
	if err != nil {
		return false
	}
	return bytes.Equal(x, y)
}
