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
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const toolVersionFile = "VERSION"

func toolVersionFiles(root, version string) (map[string][]byte, error) {
	data, err := os.ReadFile(filepath.Join(root, toolVersionFile))
	if err != nil {
		return nil, err
	}
	current := strings.TrimSpace(string(data))
	if _, err := ParseVersion(current); err != nil {
		return nil, err
	}
	cmp, err := CompareVersions(version, current)
	if err != nil {
		return nil, err
	}
	if cmp < 0 {
		return nil, fmt.Errorf("requested tool version is older than VERSION")
	}
	return map[string][]byte{toolVersionFile: []byte(version + "\n")}, nil
}

// PrepareTool updates only VERSION. Reusable workflows check out job.workflow_sha,
// so releases never need workflow-file writes that GITHUB_TOKEN cannot authorize.
func PrepareTool(root, version, source, ref, branch string) (*Snapshot, error) {
	if _, err := ParseVersion(version); err != nil {
		return nil, err
	}
	if branch == "" || ref != "refs/heads/"+branch {
		return nil, fmt.Errorf("release the tool from its default branch")
	}
	if !regexp.MustCompile(`^[a-fA-F0-9]{40}$`).MatchString(source) {
		return nil, fmt.Errorf("source SHA must be a full commit SHA")
	}
	status, err := git(root, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	if status != "" {
		return nil, fmt.Errorf("tool release checkout must be clean")
	}
	head, err := git(root, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	if head != source {
		return nil, fmt.Errorf("tool checkout does not match run source")
	}
	files, err := toolVersionFiles(root, version)
	if err != nil {
		return nil, err
	}
	tag := "refs/tags/v" + version
	exists, err := hasTag(root, tag)
	if err != nil {
		return nil, err
	}
	if exists {
		commit, err := git(root, "rev-parse", tag+"^{commit}")
		if err != nil {
			return nil, err
		}
		if commit != source {
			parents, err := git(root, "rev-list", "--parents", "-n", "1", commit)
			if err != nil {
				return nil, err
			}
			fields := strings.Fields(parents)
			if len(fields) != 2 || fields[1] != source {
				return nil, fmt.Errorf("tool version already belongs to different source")
			}
			changed, err := git(root, "diff", "--name-only", source, commit)
			if err != nil {
				return nil, err
			}
			for _, path := range strings.Split(changed, "\n") {
				if _, ok := files[path]; !ok {
					return nil, fmt.Errorf("tool release commit changed unexpected file %s", path)
				}
			}
		}
		for path, expected := range files {
			actual, err := command(root, "git", "show", commit+":"+path)
			if err != nil {
				return nil, err
			}
			if !bytes.Equal(actual, expected) {
				return nil, fmt.Errorf("tool tag contains a different VERSION")
			}
		}
		if _, err := git(root, "checkout", "--detach", commit); err != nil {
			return nil, err
		}
		return &Snapshot{Root: root, Branch: branch, Version: version, Commit: commit}, nil
	}
	remote, err := git(root, "ls-remote", "--exit-code", "origin", "refs/heads/"+branch)
	if err != nil {
		return nil, err
	}
	if fields := strings.Fields(remote); len(fields) != 2 || fields[0] != source {
		return nil, fmt.Errorf("tool default branch changed; start a new run")
	}
	for path, content := range files {
		if err := os.WriteFile(filepath.Join(root, path), content, 0644); err != nil {
			return nil, err
		}
	}
	if _, err := git(root, "add", "--", toolVersionFile); err != nil {
		return nil, err
	}
	changed, err := git(root, "diff", "--cached", "--name-only")
	if err != nil {
		return nil, err
	}
	if changed != "" {
		if _, err := git(root, "-c", "user.name=github-actions[bot]", "-c", "user.email=41898282+github-actions[bot]@users.noreply.github.com", "commit", "-m", "Release tool v"+version); err != nil {
			return nil, err
		}
	}
	commit, err := git(root, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	return &Snapshot{Root: root, Branch: branch, Version: version, Commit: commit, NeedsPush: true}, nil
}

func majorRef(root, version string) (string, string, error) {
	v, err := ParseVersion(version)
	if err != nil {
		return "", "", err
	}
	ref := fmt.Sprintf("refs/tags/v%d", v[0])
	remote, err := git(root, "ls-remote", "--refs", "origin", ref)
	if err != nil {
		return "", "", err
	}
	if remote == "" {
		return ref, "", nil
	}
	fields := strings.Fields(remote)
	if len(fields) != 2 {
		return "", "", fmt.Errorf("invalid remote major tag")
	}
	if _, err := git(root, "fetch", "origin", ref); err != nil {
		return "", "", err
	}
	data, err := command(root, "git", "show", fields[0]+":"+toolVersionFile)
	if err != nil {
		return "", "", err
	}
	current := strings.TrimSpace(string(data))
	cmp, err := CompareVersions(version, current)
	if err != nil {
		return "", "", err
	}
	if cmp < 0 {
		return "", "", fmt.Errorf("refusing to move %s back from %s to %s", ref, current, version)
	}
	return ref, fields[0], nil
}

func ReleaseTool(root, repository, version, source, ref, branch string) error {
	if !repositoryName.MatchString(repository) {
		return fmt.Errorf("invalid owner/repository")
	}
	major, previous, err := majorRef(root, version)
	if err != nil {
		return err
	}
	snapshot, err := PrepareTool(root, version, source, ref, branch)
	if err != nil {
		return err
	}
	if err := snapshot.Push(); err != nil {
		return err
	}
	store := GitHub{Repository: repository}
	tag := "v" + version
	release, err := store.Lookup(tag)
	if err != nil {
		return err
	}
	if release == nil {
		release, err = store.Create(tag, snapshot.Commit)
		if err != nil {
			return err
		}
	}
	if release == nil {
		return fmt.Errorf("tool release was not found")
	}
	if release.Draft {
		if err := store.Publish(tag, true); err != nil {
			return err
		}
	}
	// Update only the selected major alias, with a lease to detect outside changes.
	_, err = git(root, "push", "--force-with-lease="+major+":"+previous, "origin", snapshot.Commit+":"+major)
	if err != nil {
		return err
	}
	fmt.Printf("Released %s and updated %s. No plugin releases were triggered.\n", tag, major)
	return nil
}
