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

func toolFixture(t *testing.T) (string, string) {
	t.Helper()
	root, _, _ := gitFixture(t)
	if err := os.WriteFile(filepath.Join(root, toolVersionFile), []byte("1.0.0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	testGit(t, root, "add", ".")
	testGit(t, root, "commit", "-m", "Add tool version")
	testGit(t, root, "push", "origin", "main")
	return root, testGit(t, root, "rev-parse", "HEAD")
}

func TestToolReleaseChangesOnlyVersionAndRetries(t *testing.T) {
	root, source := toolFixture(t)
	first, err := PrepareTool(root, "1.0.1", source, "refs/heads/main", "main")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, toolVersionFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "1.0.1\n" {
		t.Fatalf("unexpected VERSION: %s", data)
	}
	if changed := testGit(t, root, "diff", "--name-only", source, first.Commit); changed != toolVersionFile {
		t.Fatalf("tool release changed files besides VERSION: %s", changed)
	}
	if err := first.Push(); err != nil {
		t.Fatal(err)
	}
	testGit(t, root, "checkout", "--detach", source)
	second, err := PrepareTool(root, "1.0.1", source, "refs/heads/main", "main")
	if err != nil {
		t.Fatal(err)
	}
	if second.NeedsPush || first.Commit != second.Commit {
		t.Fatal("tool retry created different source")
	}
}

func TestMajorAliasCannotDowngrade(t *testing.T) {
	root, source := toolFixture(t)
	snapshot, err := PrepareTool(root, "1.1.0", source, "refs/heads/main", "main")
	if err != nil {
		t.Fatal(err)
	}
	if err := snapshot.Push(); err != nil {
		t.Fatal(err)
	}
	testGit(t, root, "push", "origin", snapshot.Commit+":refs/tags/v1")
	if _, _, err := majorRef(root, "1.0.1"); err == nil {
		t.Fatal("major alias downgrade accepted")
	}
	if _, _, err := majorRef(root, "1.1.0"); err != nil {
		t.Fatal(err)
	}
}
