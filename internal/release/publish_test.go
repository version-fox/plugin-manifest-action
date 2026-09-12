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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeStore struct {
	releases     map[string]*RemoteRelease
	data         map[int64][]byte
	nextID       int64
	operations   []string
	failManifest bool
	failArchive  bool
	lookupError  error
	latestTag    string
}

func newStore() *fakeStore {
	return &fakeStore{releases: map[string]*RemoteRelease{}, data: map[int64][]byte{}}
}
func (f *fakeStore) Lookup(tag string) (*RemoteRelease, error) {
	if f.lookupError != nil {
		return nil, f.lookupError
	}
	r := f.releases[tag]
	if r == nil {
		return nil, nil
	}
	copy := *r
	copy.Assets = append([]RemoteAsset(nil), r.Assets...)
	return &copy, nil
}

func (f *fakeStore) Latest() (*RemoteRelease, error) { return f.Lookup(f.latestTag) }
func (f *fakeStore) Create(tag, commit string) (*RemoteRelease, error) {
	f.nextID++
	f.releases[tag] = &RemoteRelease{ID: f.nextID, TagName: tag, Draft: true}
	f.operations = append(f.operations, "create "+tag)
	return f.Lookup(tag)
}
func (f *fakeStore) Download(asset RemoteAsset) ([]byte, error) { return f.data[asset.ID], nil }
func (f *fakeStore) Upload(tag, path string, replace bool) error {
	if f.failManifest && tag == "manifest" {
		f.failManifest = false
		return fmt.Errorf("manifest upload failed")
	}
	if f.failArchive && strings.HasSuffix(path, ".zip") {
		f.failArchive = false
		return fmt.Errorf("archive upload failed")
	}
	r := f.releases[tag]
	if r == nil {
		return fmt.Errorf("release missing")
	}
	if asset, exists := findAsset(r, filepath.Base(path)); exists {
		if !replace {
			return fmt.Errorf("asset already exists")
		}
		f.data[asset.ID], _ = os.ReadFile(path)
	} else {
		f.nextID++
		r.Assets = append(r.Assets, RemoteAsset{ID: f.nextID, Name: filepath.Base(path)})
		f.data[f.nextID], _ = os.ReadFile(path)
	}
	f.operations = append(f.operations, "upload "+tag+"/"+filepath.Base(path))
	return nil
}
func (f *fakeStore) Publish(tag string, latest bool) error {
	f.releases[tag].Draft = false
	if latest {
		f.latestTag = tag
	}
	f.operations = append(f.operations, fmt.Sprintf("publish %s latest=%t", tag, latest))
	return nil
}

func TestOldRetryCannotReplaceMissingNewerManifest(t *testing.T) {
	store := newStore()
	old := testBundle(t, "1.0.1")
	if err := Publish(store, old, "older"); err != nil {
		t.Fatal(err)
	}
	newer := testBundle(t, "1.1.0")
	store.failManifest = true
	if err := Publish(store, newer, "newer"); err == nil {
		t.Fatal("expected manifest failure")
	}
	// Simulate gh --clobber deleting the old manifest before an upload failure.
	store.releases["manifest"].Assets = nil
	if err := Publish(store, old, "older"); err != nil {
		t.Fatal(err)
	}
	if len(store.releases["manifest"].Assets) != 0 {
		t.Fatal("old retry recreated an outdated manifest")
	}
	if err := Publish(store, newer, "newer"); err != nil {
		t.Fatal(err)
	}
	asset, exists := findAsset(store.releases["manifest"], "manifest.json")
	if !exists {
		t.Fatal("new retry did not restore manifest")
	}
	expected, _ := os.ReadFile(newer.Manifest)
	if !equalJSON(store.data[asset.ID], expected) {
		t.Fatal("recovered manifest is not newest version")
	}
}

func testBundle(t *testing.T, version string) *Bundle {
	t.Helper()
	root := fixture(t)
	if err := UpdateVersion(root, version); err != nil {
		t.Fatal(err)
	}
	b, err := Build(root, t.TempDir(), "owner/example", version)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPublishThenRetryDoesNotChangeAssets(t *testing.T) {
	store := newStore()
	bundle := testBundle(t, "1.0.1")
	if err := Publish(store, bundle, "commit"); err != nil {
		t.Fatal(err)
	}
	operations := len(store.operations)
	if err := Publish(store, bundle, "commit"); err != nil {
		t.Fatal(err)
	}
	if len(store.operations) != operations {
		t.Fatalf("successful retry performed writes: %v", store.operations)
	}
	order := strings.Join(store.operations, "\n")
	if strings.Index(order, "publish v1.0.1") > strings.Index(order, "upload manifest/") {
		t.Fatalf("manifest advanced before version publication: %s", order)
	}
}

func TestResumeAfterManifestFailure(t *testing.T) {
	store := newStore()
	store.failManifest = true
	bundle := testBundle(t, "1.0.1")
	if err := Publish(store, bundle, "commit"); err == nil {
		t.Fatal("expected failure")
	}
	if store.releases["v1.0.1"].Draft {
		t.Fatal("version release should already be published")
	}
	if err := Publish(store, bundle, "commit"); err != nil {
		t.Fatal(err)
	}
	for _, operation := range store.operations {
		if strings.HasPrefix(operation, "upload v1.0.1/") && strings.Count(strings.Join(store.operations, "\n"), operation) != 1 {
			t.Fatal("retry replaced version assets")
		}
	}
}

func TestResumeAfterArchiveFailure(t *testing.T) {
	store := newStore()
	store.failArchive = true
	bundle := testBundle(t, "1.0.1")
	if err := Publish(store, bundle, "commit"); err == nil {
		t.Fatal("expected failure")
	}
	if store.releases["manifest"] != nil {
		t.Fatal("manifest appeared before archive upload succeeded")
	}
	if err := Publish(store, bundle, "commit"); err != nil {
		t.Fatal(err)
	}
}

func TestRejectDifferentVersionAsset(t *testing.T) {
	store := newStore()
	bundle := testBundle(t, "1.0.1")
	if err := Publish(store, bundle, "commit"); err != nil {
		t.Fatal(err)
	}
	asset, _ := findAsset(store.releases["v1.0.1"], filepath.Base(bundle.Archive))
	store.data[asset.ID] = []byte("different artifact")
	before := len(store.operations)
	if err := Publish(store, bundle, "commit"); err == nil {
		t.Fatal("different published artifact accepted")
	}
	if len(store.operations) != before {
		t.Fatal("conflicting artifact was overwritten")
	}
}

func TestOldRetryDoesNotDowngradeManifest(t *testing.T) {
	store := newStore()
	newer := testBundle(t, "1.1.0")
	if err := Publish(store, newer, "newer"); err != nil {
		t.Fatal(err)
	}
	beforeAsset, _ := findAsset(store.releases["manifest"], "manifest.json")
	before := string(store.data[beforeAsset.ID])
	if err := Publish(store, testBundle(t, "1.0.1"), "older"); err != nil {
		t.Fatal(err)
	}
	afterAsset, _ := findAsset(store.releases["manifest"], "manifest.json")
	if string(store.data[afterAsset.ID]) != before {
		t.Fatal("older version overwrote current manifest")
	}
	if strings.Contains(strings.Join(store.operations, "\n"), "publish v1.0.1 latest=true") {
		t.Fatal("older release was marked latest")
	}
}

func TestLookupFailureDoesNotCreateRelease(t *testing.T) {
	store := newStore()
	store.lookupError = fmt.Errorf("API unavailable")
	if err := Publish(store, testBundle(t, "1.0.1"), "commit"); err == nil {
		t.Fatal("API failure ignored")
	}
	if len(store.operations) != 0 {
		t.Fatal("lookup failure caused writes")
	}
}
