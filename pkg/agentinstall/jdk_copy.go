package agentinstall

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

type jdkCopy struct {
	Source, Target string
	Major          int
}

func copyJDK(ctx context.Context, transport Transport, options jdkCopy) error {
	archive, err := jdkArchive(options.Source)
	if err != nil {
		return err
	}
	defer func() {
		_ = archive.Close()
		_ = os.Remove(archive.Name())
	}()
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	staged := options.Target + ".stage-" + hex.EncodeToString(nonce[:])
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = transport.Run(cleanup, []string{"rm", "-rf", "--", staged}, nil)
	}()
	if _, err := transport.Run(ctx, []string{"install", "-d", "-m", "700", staged}, nil); err != nil {
		return err
	}
	if _, err := transport.Run(ctx, []string{"tar", "-x", "-z", "-m", "--no-same-owner", "-f", "-", "-C", staged}, archive); err != nil {
		return fmt.Errorf("copy JDK into target: %w", err)
	}
	info, err := inspectJavaHome(ctx, transport, staged)
	if err != nil {
		return err
	}
	if !info.Present || info.Major != options.Major || !info.Attach {
		return fmt.Errorf("copied JDK lacks the Java %d Attach API", options.Major)
	}
	if _, err := transport.Run(ctx, []string{"mv", "-T", staged, options.Target}, nil); err != nil {
		return fmt.Errorf("publish copied JDK: %w", err)
	}
	return nil
}

func jdkArchive(root string) (*os.File, error) {
	if err := os.MkdirAll(".tmp/telltale-jdk", 0o700); err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(".tmp/telltale-jdk", "transfer-*.tar.gz")
	if err != nil {
		return nil, err
	}
	compressed := gzip.NewWriter(file)
	archive := tar.NewWriter(compressed)
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || path == root {
			return err
		}
		return archiveJDKFile(archive, root, path, entry)
	})
	err = errors.Join(err, archive.Close(), compressed.Close())
	if err == nil {
		_, err = file.Seek(0, io.SeekStart)
	}
	if err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return nil, err
	}
	return file, nil
}

func archiveJDKFile(archive *tar.Writer, root, path string, entry fs.DirEntry) error {
	info, err := entry.Info()
	if err != nil {
		return err
	}
	name, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	var link string
	if info.Mode()&os.ModeSymlink != 0 {
		link, err = os.Readlink(path)
		if err != nil {
			return err
		}
		if filepath.IsAbs(link) || !filepath.IsLocal(filepath.Join(filepath.Dir(name), link)) {
			return fmt.Errorf("JDK symlink %q escapes its directory", name)
		}
	}
	header, err := tar.FileInfoHeader(info, link)
	if err != nil {
		return err
	}
	header.Name = filepath.ToSlash(name)
	if err := archive.WriteHeader(header); err != nil || !info.Mode().IsRegular() {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	_, err = io.Copy(archive, file)
	return errors.Join(err, file.Close())
}
