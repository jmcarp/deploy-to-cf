package main

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
)

func Untar(reader io.Reader, dest string) error {
	gzipReader, err := gzip.NewReader(reader)
	if err != nil {
		return err
	}
	tarReader := tar.NewReader(gzipReader)

	for {
		header, err := tarReader.Next()
		if err != nil {
			if err == io.EOF {
				break
			} else {
				return err
			}
		}

		path := filepath.Join(dest, header.Name)
		info := header.FileInfo()

		if info.IsDir() {
			err = os.MkdirAll(path, info.Mode())
			if err != nil {
				return err
			}
		} else {
			file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode())
			if err != nil {
				return err
			}
			defer file.Close()

			_, err = io.Copy(file, tarReader)
			if err != nil {
				return err
			}
		}
	}
	return nil
}
