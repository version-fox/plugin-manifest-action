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
	"os"
	"path/filepath"
	"testing"
)

func testGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	out, err := git(root, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func gitFixture(t *testing.T) (string, string, string) {
	t.Helper()
	root := fixture(t)
	remote := filepath.Join(t.TempDir(), "remote.git")
	testGit(t, root, "init", "-b", "main")
	testGit(t, root, "config", "user.name", "Test")
	testGit(t, root, "config", "user.email", "test@example.com")
	testGit(t, root, "add", ".")
	testGit(t, root, "commit", "-m", "Initial plugin")
	testGit(t, root, "init", "--bare", remote)
	testGit(t, root, "remote", "add", "origin", remote)
	testGit(t, root, "push", "origin", "main")
	return root, remote, testGit(t, root, "rev-parse", "HEAD")
}

func TestReleaseRetryUsesSameCommit(t *testing.T) {
	root, _, source := gitFixture(t)
	req := Request{Root: root, Version: "1.0.1", SourceSHA: source, Branch: "main", Event: "workflow_dispatch", Ref: "refs/heads/main"}
	first, err := Prepare(req)
	if err != nil {
		t.Fatal(err)
	}
	before, err := Build(root, t.TempDir(), "owner/example", first.Version)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Push(); err != nil {
		t.Fatal(err)
	}
	testGit(t, root, "checkout", "--detach", source)
	second, err := Prepare(req)
	if err != nil {
		t.Fatal(err)
	}
	if second.NeedsPush || first.Commit != second.Commit {
		t.Fatalf("retry changed release commit: %#v %#v", first, second)
	}
	after, err := Build(root, t.TempDir(), "owner/example", second.Version)
	if err != nil {
		t.Fatal(err)
	}
	if before.SHA256 != after.SHA256 {
		t.Fatal("retry changed release artifact")
	}
}

func TestRejectReusingVersionForDifferentSource(t *testing.T) {
	root, _, source := gitFixture(t)
	req := Request{Root: root, Version: "1.0.1", SourceSHA: source, Branch: "main", Event: "workflow_dispatch", Ref: "refs/heads/main"}
	snapshot, err := Prepare(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := snapshot.Push(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "lib/new.lua"), []byte("return {}"), 0644); err != nil {
		t.Fatal(err)
	}
	testGit(t, root, "add", ".")
	testGit(t, root, "commit", "-m", "More code")
	testGit(t, root, "push", "origin", "HEAD:main")
	req.SourceSHA = testGit(t, root, "rev-parse", "HEAD")
	if _, err := Prepare(req); err == nil {
		t.Fatal("existing version accepted different source")
	}
}

func TestTagReleaseRequiresMatchingMetadata(t *testing.T) {
	root, _, source := gitFixture(t)
	testGit(t, root, "tag", "v1.0.1")
	_, err := Prepare(Request{Root: root, SourceSHA: source, Event: "push", Ref: "refs/tags/v1.0.1"})
	if err == nil {
		t.Fatal("mismatching metadata/tag accepted")
	}
}

func TestRejectManualReleaseFromFeatureBranch(t *testing.T) {
	root, _, source := gitFixture(t)
	_, err := Prepare(Request{Root: root, Version: "1.0.1", SourceSHA: source, Branch: "main", Event: "workflow_dispatch", Ref: "refs/heads/feature"})
	if err == nil {
		t.Fatal("non-default-branch manual release accepted")
	}
}

func TestAtomicPushRejectsAdvancedBranchWithoutTag(t *testing.T) {
	root, remote, source := gitFixture(t)
	snapshot, err := Prepare(Request{Root: root, Version: "1.0.1", SourceSHA: source, Branch: "main", Event: "workflow_dispatch", Ref: "refs/heads/main"})
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "other")
	testGit(t, root, "clone", "--branch", "main", remote, other)
	testGit(t, other, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "Concurrent update")
	testGit(t, other, "push", "origin", "main")
	if err := snapshot.Push(); err == nil {
		t.Fatal("concurrent branch update was overwritten")
	}
	if tag := testGit(t, root, "ls-remote", "origin", "refs/tags/v1.0.1"); tag != "" {
		t.Fatal("failed atomic push still published the tag")
	}
}
