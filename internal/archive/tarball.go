package archive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// LinkError is a symlink `afy deploy` will not pack: it is written as an absolute path, or it does not
// land inside the code being deployed (the agent's folder and the paths its aetherfy.yaml includes). The
// sentence is the control plane's for a push (shared/code_tree.link_outside_message).
type LinkError struct{ Name, Target string }

func (e *LinkError) Error() string {
	return fmt.Sprintf("The link '%s' points to '%s', outside the code Aetherfy deploys for this agent. A link is "+
		"deployed only when it is written as a relative path and lands inside the agent's folder or a path "+
		"its aetherfy.yaml includes. To deploy code that another folder of the repository holds, list that "+
		"folder under `include:` in aetherfy.yaml; otherwise replace the link with the file it points to.",
		e.Name, e.Target)
}

// tree is one folder (or file) packed into the archive under name ("" is the archive's root).
type tree struct{ name, path string }

// CreateTarball creates a gzipped tarball (.tar.gz) of a directory: the agent's folder, rooted at it.
func CreateTarball(sourceDir string, ignorePatterns []string) ([]byte, error) {
	return writeTarball([]tree{{"", sourceDir}}, ignorePatterns)
}

// CreateRepositoryTarball creates the archive of an agent whose aetherfy.yaml includes repository paths:
// rooted at the repository, holding the agent's folder at codeRoot and each include at its own path, so
// the image mirrors the repository. Each is read from its real folder (an include that is a link inside
// the repository is packed as what it leads to, under the name the yaml used). The ignore patterns are
// the agent folder's and apply below each packed path, never to the path itself.
func CreateRepositoryTarball(repoRoot, codeRoot string, includePaths []string, ignorePatterns []string) ([]byte, error) {
	trees := []tree{{codeRoot, realPath(filepath.Join(repoRoot, filepath.FromSlash(codeRoot)))}}
	for _, p := range includePaths {
		trees = append(trees, tree{p, realPath(filepath.Join(repoRoot, filepath.FromSlash(p)))})
	}
	return writeTarball(trees, ignorePatterns)
}

func writeTarball(trees []tree, ignorePatterns []string) ([]byte, error) {
	// Combine default and custom ignore patterns
	allPatterns := append(append([]string{}, DefaultIgnorePatterns...), ignorePatterns...)

	packed := make([]string, len(trees))
	for i, t := range trees {
		packed[i] = realPath(t.path)
	}

	buf := new(bytes.Buffer)
	gzipWriter := gzip.NewWriter(buf)
	tarWriter := tar.NewWriter(gzipWriter)

	for _, t := range trees {
		if err := writeTree(tarWriter, t, allPatterns, packed); err != nil {
			return nil, err
		}
	}

	// Close writers in correct order
	if err := tarWriter.Close(); err != nil {
		return nil, fmt.Errorf("failed to close tar writer: %w", err)
	}
	if err := gzipWriter.Close(); err != nil {
		return nil, fmt.Errorf("failed to close gzip writer: %w", err)
	}

	return buf.Bytes(), nil
}

// linkTarget is what a symlink at path holds, if it may be packed: written as a relative path, and
// landing (followed to its end, or as written when it leads nowhere) inside one of the packed trees.
func linkTarget(path, name string, packed []string) (string, error) {
	target, err := os.Readlink(path)
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(target) || filepath.VolumeName(target) != "" || (len(target) > 0 && (target[0] == '/' || target[0] == '\\')) {
		return "", &LinkError{Name: name, Target: filepath.ToSlash(target)}
	}
	lands := filepath.Clean(filepath.Join(filepath.Dir(path), target))
	if _, statErr := os.Stat(path); statErr == nil {
		lands = realPath(path)
	}
	for _, root := range packed {
		if within(lands, root) {
			return filepath.ToSlash(target), nil
		}
	}
	return "", &LinkError{Name: name, Target: filepath.ToSlash(target)}
}

func writeTree(tarWriter *tar.Writer, t tree, allPatterns []string, packed []string) error {
	err := filepath.Walk(t.path, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Get relative path
		relPath, err := filepath.Rel(t.path, path)
		if err != nil {
			return err
		}

		// Skip the root directory itself when it is the archive's root
		if relPath == "." && t.name == "" {
			return nil
		}

		// Check if should be ignored (below the packed path only: its own name is what was asked for)
		if relPath != "." && shouldIgnore(relPath, info.IsDir(), allPatterns) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		name := filepath.ToSlash(relPath)
		if t.name != "" {
			name = t.name
			if relPath != "." {
				name = t.name + "/" + filepath.ToSlash(relPath)
			}
		}

		// A symlink is written as a link, never followed, and only when it stays inside what is packed.
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			if link, err = linkTarget(path, name, packed); err != nil {
				return err
			}
		}

		// Create tar header
		header, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}

		// Use forward slashes and relative path
		header.Name = name

		// Write header
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}

		// Write file content (skip directories and links)
		if !info.IsDir() && link == "" {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()

			if _, err := io.Copy(tarWriter, file); err != nil {
				return err
			}
		}

		return nil
	})

	if err != nil {
		if _, isLink := err.(*LinkError); isLink {
			return err
		}
		return fmt.Errorf("failed to walk directory: %w", err)
	}
	return nil
}

// CreateTarballWithValidation creates a tarball with aetherfy.yaml validation
func CreateTarballWithValidation(sourceDir string) ([]byte, error) {
	// Validate aetherfy.yaml exists
	if err := ValidateAetherfyConfig(sourceDir); err != nil {
		return nil, err
	}

	// Load custom ignore patterns
	patterns, err := LoadIgnorePatterns(sourceDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load .afyignore: %w", err)
	}

	return CreateTarball(sourceDir, patterns)
}
