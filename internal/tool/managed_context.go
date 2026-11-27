// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package tool

import (
	"bytes"
	"fmt"
	"io"
	"path"
	"strings"

	allowedext "github.com/alibaba/open-code-review/internal/config/allowlist"
	"github.com/alibaba/open-code-review/internal/diff"
)

// SetManagedContextExclusions freezes hard exclusions for both comparison sides.
// Related files remain readable; the review inventory is not a context allowlist.
func (r *Registry) SetManagedContextExclusions(paths []string) {
	if r.frozen {
		panic("tool: managed context configured after Freeze")
	}
	excluded := make(map[string]bool, len(paths))
	for _, name := range paths {
		if name != "" && name != "/dev/null" {
			excluded[managedPath(name)] = true
		}
	}
	configure := func(reader *FileReader, access *CodeAccess) {
		if reader != nil {
			reader.managedExcluded = excluded
		}
		if access != nil {
			access.newReader.managedExcluded = excluded
			access.oldReader.managedExcluded = excluded
		}
	}
	for _, provider := range r.providers {
		switch p := provider.(type) {
		case *FileReadProvider:
			configure(p.FileReader, p.Access)
		case *CodeSearchProvider:
			configure(p.FileReader, p.Access)
		case *FileFindProvider:
			configure(p.FileReader, p.Access)
		}
	}
}

func managedPath(name string) string {
	return path.Clean(strings.ReplaceAll(name, "\\", "/"))
}

func (fr *FileReader) allowsPath(name string) bool {
	if fr.managedExcluded == nil {
		return true
	}
	name = managedPath(name)
	return name != ".." && !strings.HasPrefix(name, "../") && !path.IsAbs(name) &&
		!fr.managedExcluded[name] && !allowedext.IsSecretPath(name) && diff.ProviderDirPrefix(name) == ""
}

type managedTextReader struct{ io.Reader }

func (r managedTextReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if bytes.IndexByte(p[:n], 0) >= 0 {
		return 0, fmt.Errorf("binary content is unavailable to managed context tools")
	}
	return n, err
}

func (fr *FileReader) scanLines(r io.Reader, startLine, maxLines int) ([]string, int, error) {
	if fr.managedExcluded != nil {
		r = managedTextReader{r}
	}
	return scanLines(r, startLine, maxLines)
}

func (fr *FileReader) contextText(content []byte) (string, error) {
	if fr.managedExcluded != nil && bytes.IndexByte(content, 0) >= 0 {
		return "", fmt.Errorf("binary content is unavailable to managed context tools")
	}
	return string(content), nil
}
