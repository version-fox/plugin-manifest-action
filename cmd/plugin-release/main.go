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

package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/version-fox/plugin-manifest-action/internal/release"
)

func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: plugin-release check|publish|release-tool [options]")
	}
	flags := flag.NewFlagSet(os.Args[1], flag.ContinueOnError)
	root := flags.String("source", ".", "plugin source directory")
	out := flags.String("out", "dist", "artifact directory outside plugin source")
	repository := flags.String("repository", os.Getenv("GITHUB_REPOSITORY"), "owner/repository")
	version := flags.String("version", os.Getenv("RELEASE_VERSION"), "stable version without v")
	if err := flags.Parse(os.Args[2:]); err != nil {
		return err
	}
	if !validCommand(os.Args[1]) {
		return fmt.Errorf("unknown command %q", os.Args[1])
	}
	absRoot, err := filepath.Abs(*root)
	if err != nil {
		return err
	}
	absOut, err := filepath.Abs(*out)
	if err != nil {
		return err
	}
	if os.Args[1] == "release-tool" {
		return release.ReleaseTool(absRoot, *repository, *version, os.Getenv("GITHUB_SHA"), os.Getenv("GITHUB_REF"), os.Getenv("DEFAULT_BRANCH"))
	}
	var snapshot *release.Snapshot
	if os.Args[1] == "publish" {
		snapshot, err = release.Prepare(release.Request{Root: absRoot, Version: *version, SourceSHA: os.Getenv("GITHUB_SHA"), Branch: os.Getenv("DEFAULT_BRANCH"), Event: os.Getenv("GITHUB_EVENT_NAME"), Ref: os.Getenv("GITHUB_REF")})
		if err != nil {
			return err
		}
		*version = snapshot.Version
	}
	bundle, err := release.Build(absRoot, absOut, *repository, *version)
	if err != nil {
		return err
	}
	if snapshot != nil {
		if err := snapshot.Push(); err != nil {
			return err
		}
		if err := release.Publish(release.GitHub{Repository: *repository}, bundle, snapshot.Commit); err != nil {
			return err
		}
	}
	fmt.Printf("Validated plugin version %s\nArchive: %s\nManifest: %s\nSHA256: %s\n", bundle.Version, bundle.Archive, bundle.Manifest, bundle.SHA256)
	if snapshot != nil {
		fmt.Printf("Published: https://github.com/%s/releases/tag/v%s\n", *repository, bundle.Version)
	}
	if summary := os.Getenv("GITHUB_STEP_SUMMARY"); summary != "" {
		file, err := os.OpenFile(summary, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)
		if err != nil {
			return err
		}
		_, writeErr := fmt.Fprintf(file, "Plugin version: `%s`\n\nArchive SHA256: `%s`\n", bundle.Version, bundle.SHA256)
		closeErr := file.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func validCommand(command string) bool {
	return command == "check" || command == "publish" || command == "release-tool"
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
