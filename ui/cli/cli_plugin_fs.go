// Plugin filesystem / archive plumbing: extension whitelisting,
// zip extraction with path-escape safeguards, manifest-entry
// validation, and bounded copy/remove helpers used by the plugin
// install/remove path. Extracted from cli_plugin_skill.go.

package cli

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func pluginFileExtSupported(ext string) bool {
	switch strings.ToLower(strings.TrimSpace(ext)) {
	case ".so", ".dll", ".dylib", ".wasm", ".js", ".mjs", ".py", ".sh":
		return true
	default:
		return false
	}
}

func pluginSourceFileExtSupported(ext string) bool {
	if pluginFileExtSupported(ext) {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(ext), ".zip")
}

func expandPluginSourceIfArchive(path string) (string, func(), error) {
	if !strings.EqualFold(filepath.Ext(path), ".zip") {
		return path, nil, nil
	}
	tmpDir, err := os.MkdirTemp("", "dfmc-plugin-zip-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }
	if err := extractZipArchive(path, tmpDir); err != nil {
		cleanup()
		return "", nil, err
	}
	root := archiveRootDir(tmpDir)
	return root, cleanup, nil
}

func archiveRootDir(tmpDir string) string {
	entries, err := os.ReadDir(tmpDir)
	if err != nil || len(entries) != 1 || !entries[0].IsDir() {
		return tmpDir
	}
	return filepath.Join(tmpDir, entries[0].Name())
}

func extractZipArchive(srcZip, dstDir string) error {
	r, err := zip.OpenReader(srcZip)
	if err != nil {
		return err
	}
	defer r.Close()
	if len(r.File) == 0 {
		return fmt.Errorf("zip archive is empty")
	}
	for _, f := range r.File {
		cleanName := filepath.Clean(f.Name)
		if cleanName == "." || cleanName == "" {
			continue
		}
		if filepath.IsAbs(cleanName) || cleanName == ".." || strings.HasPrefix(cleanName, ".."+string(filepath.Separator)) {
			return fmt.Errorf("zip archive contains unsafe path: %s", f.Name)
		}
		targetPath, err := resolvePathWithinBase(dstDir, filepath.Join(dstDir, cleanName))
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, 0o755); err != nil {
				return err
			}
			continue
		}
		if f.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("zip archive contains symlink entry: %s", f.Name)
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		if err := writeFileFromReader(targetPath, rc); err != nil {
			_ = rc.Close()
			return err
		}
		_ = rc.Close()
	}
	return nil
}

func writeFileFromReader(path string, r io.Reader) error {
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, r); err != nil {
		return err
	}
	return out.Close()
}

func validatePluginManifestEntry(pluginPath string) error {
	mf, _, ok := readPluginManifest(pluginPath)
	if !ok {
		return nil
	}
	entry := strings.TrimSpace(mf.Entry)
	if entry == "" {
		return nil
	}
	entryPath, err := resolvePathWithinBase(pluginPath, filepath.Join(pluginPath, entry))
	if err != nil {
		return fmt.Errorf("plugin manifest entry invalid: %w", err)
	}
	st, err := os.Stat(entryPath)
	if err != nil {
		return fmt.Errorf("plugin manifest entry not found: %s", entry)
	}
	if st.IsDir() {
		return fmt.Errorf("plugin manifest entry points to directory: %s", entry)
	}
	if ext := strings.ToLower(filepath.Ext(entryPath)); ext != "" && !pluginFileExtSupported(ext) {
		return fmt.Errorf("plugin manifest entry has unsupported extension: %s", ext)
	}
	return nil
}

func resolvePathWithinBase(base, target string) (string, error) {
	baseAbs, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	targetAbs := target
	if !filepath.IsAbs(targetAbs) {
		targetAbs = filepath.Join(baseAbs, targetAbs)
	}
	targetAbs, err = filepath.Abs(targetAbs)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(baseAbs, targetAbs)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes plugin directory")
	}
	return targetAbs, nil
}

func removePathSafe(base, target string) error {
	targetAbs, err := resolvePathWithinBase(base, target)
	if err != nil {
		return err
	}
	if _, err := os.Stat(targetAbs); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return os.RemoveAll(targetAbs)
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}
