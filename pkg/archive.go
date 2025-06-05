package eget2

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/mholt/archives"
	"io/fs"
)

func extractArchive(archivePath, destDir string) error {
	ctx := context.TODO()

	fsys, err := archives.FileSystem(ctx, archivePath, nil)
	if err != nil {
		return fmt.Errorf("create file system: %w", err)
	}

	err = fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		destPath := filepath.Join(destDir, path)
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return err
		}
		out, err := os.Create(destPath)
		if err != nil {
			return err
		}
		defer out.Close()
		in, err := fsys.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		_, err = io.Copy(out, in)
		return err
	})
	if err != nil {
		return fmt.Errorf("extract archive: %w", err)
	}

	return nil
}
