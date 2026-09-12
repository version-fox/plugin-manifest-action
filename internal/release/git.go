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
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

func command(dir, program string, args ...string) ([]byte, error) {
	cmd := exec.Command(program, args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w: %s", program, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func git(root string, args ...string) (string, error) {
	out, err := command(root, "git", args...)
	return strings.TrimSpace(string(out)), err
}

func hasTag(root, ref string) (bool, error) {
	_, err := git(root, "show-ref", "--verify", "--quiet", ref)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

type Snapshot struct {
	Root, Branch, Version, Commit string
	NeedsPush                     bool
}

type Request struct {
	Root, Version, SourceSHA, Branch, Event, Ref string
}

// Prepare fixes the source commit before building. A retry can reuse only that
// commit or the exact metadata-only release commit previously made from it.
func Prepare(request Request) (*Snapshot, error) {
	if request.Event != "workflow_dispatch" && request.Event != "push" {
		return nil, fmt.Errorf("publishing requires workflow_dispatch or a vX.Y.Z tag push")
	}
	if !regexp.MustCompile(`^[a-fA-F0-9]{40}$`).MatchString(request.SourceSHA) {
		return nil, fmt.Errorf("source SHA must be a full commit SHA")
	}
	if request.Event == "push" {
		if !strings.HasPrefix(request.Ref, "refs/tags/v") {
			return nil, fmt.Errorf("push publishing requires a version tag")
		}
		tagVersion := strings.TrimPrefix(request.Ref, "refs/tags/v")
		if request.Version != "" && request.Version != tagVersion {
			return nil, fmt.Errorf("input version does not match tag")
		}
		request.Version = tagVersion
	} else if request.Ref != "refs/heads/"+request.Branch || request.Branch == "" {
		return nil, fmt.Errorf("manual releases must run from the default branch")
	}
	if _, err := ParseVersion(request.Version); err != nil {
		return nil, err
	}
	status, err := git(request.Root, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	if status != "" {
		return nil, fmt.Errorf("release checkout must be clean")
	}
	source, err := git(request.Root, "rev-parse", "--verify", request.SourceSHA+"^{commit}")
	if err != nil {
		return nil, err
	}
	head, err := git(request.Root, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	if source != head {
		return nil, fmt.Errorf("checkout HEAD does not match the requested source commit")
	}
	tag := "refs/tags/v" + request.Version
	exists, err := hasTag(request.Root, tag)
	if err != nil {
		return nil, err
	}
	if exists {
		commit, err := git(request.Root, "rev-parse", tag+"^{commit}")
		if err != nil {
			return nil, err
		}
		if request.Event == "push" && commit != source {
			return nil, fmt.Errorf("tag points to a different source commit")
		}
		if commit != source {
			parents, err := git(request.Root, "rev-list", "--parents", "-n", "1", commit)
			if err != nil {
				return nil, err
			}
			fields := strings.Fields(parents)
			changed, err := git(request.Root, "diff", "--name-only", source, commit)
			if err != nil {
				return nil, err
			}
			if len(fields) != 2 || fields[1] != source || changed != "metadata.lua" {
				return nil, fmt.Errorf("tag v%s already belongs to different source; choose a new version", request.Version)
			}
			original, err := os.ReadFile(filepath.Join(request.Root, "metadata.lua"))
			if err != nil {
				return nil, err
			}
			expected, err := replaceVersion(original, request.Version)
			if err != nil {
				return nil, err
			}
			actual, err := command(request.Root, "git", "show", commit+":metadata.lua")
			if err != nil {
				return nil, err
			}
			if !bytes.Equal(expected, actual) {
				return nil, fmt.Errorf("existing release commit contains different metadata")
			}
		}
		if _, err := git(request.Root, "checkout", "--detach", commit); err != nil {
			return nil, err
		}
		metadata, err := ReadMetadata(request.Root)
		if err != nil {
			return nil, err
		}
		if metadata["version"] != request.Version {
			return nil, fmt.Errorf("tag and metadata version differ")
		}
		return &Snapshot{Root: request.Root, Branch: request.Branch, Version: request.Version, Commit: commit}, nil
	}
	if request.Event == "push" {
		return nil, fmt.Errorf("version tag is missing from checkout")
	}
	remote, err := git(request.Root, "ls-remote", "--exit-code", "origin", "refs/heads/"+request.Branch)
	if err != nil {
		return nil, err
	}
	if fields := strings.Fields(remote); len(fields) != 2 || fields[0] != source {
		return nil, fmt.Errorf("default branch changed after this run started; start a new release run")
	}
	metadata, err := ReadMetadata(request.Root)
	if err != nil {
		return nil, err
	}
	cmp, err := CompareVersions(request.Version, metadata["version"].(string))
	if err != nil {
		return nil, err
	}
	if cmp < 0 {
		return nil, fmt.Errorf("requested version is older than metadata version")
	}
	if cmp > 0 {
		if err := UpdateVersion(request.Root, request.Version); err != nil {
			return nil, err
		}
		if _, err := git(request.Root, "add", "--", "metadata.lua"); err != nil {
			return nil, err
		}
		if _, err := git(request.Root, "-c", "user.name=github-actions[bot]", "-c", "user.email=41898282+github-actions[bot]@users.noreply.github.com", "commit", "-m", "Release v"+request.Version); err != nil {
			return nil, err
		}
	}
	commit, err := git(request.Root, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	return &Snapshot{Root: request.Root, Branch: request.Branch, Version: request.Version, Commit: commit, NeedsPush: true}, nil
}

// Push happens only after validation and packaging succeed. Atomic push prevents
// the metadata commit and version tag from being published independently.
func (s *Snapshot) Push() error {
	if !s.NeedsPush {
		return nil
	}
	tag := "v" + s.Version
	if _, err := git(s.Root, "tag", tag, s.Commit); err != nil {
		return err
	}
	_, err := git(s.Root, "push", "--atomic", "origin", s.Commit+":refs/heads/"+s.Branch, "refs/tags/"+tag)
	return err
}
