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
	"strings"
	"testing"
)

func toolFixture(t *testing.T) (string, string) {
	t.Helper()
	root, _, _ := gitFixture(t)
	for _, name := range toolWorkflows {
		if err := os.WriteFile(filepath.Join(root, name), []byte("steps:\n  - with:\n      ref: v1.0.0 # release-tool-version\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	testGit(t, root, "add", ".")
	testGit(t, root, "commit", "-m", "Add tool workflows")
	testGit(t, root, "push", "origin", "main")
	return root, testGit(t, root, "rev-parse", "HEAD")
}

func TestToolReleasePinsScriptsAndRetries(t *testing.T) {
	root, source := toolFixture(t)
	first, err := PrepareTool(root, "1.0.1", source, "refs/heads/main", "main")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range toolWorkflows {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "ref: v1.0.1 # release-tool-version") {
			t.Fatalf("workflow did not pin its own tool version: %s", data)
		}
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
