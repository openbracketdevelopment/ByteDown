package storage

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
)

func ID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func Init(root string) error {
	for _, dir := range []string{"originals", "staging", "outputs", "uploads"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			return err
		}
	}
	directory, err := os.Open(root)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func Check(root string) error {
	for _, directory := range []string{"uploads", "originals", "staging", "outputs"} {
		f, e := os.CreateTemp(filepath.Join(root, directory), "ready-")
		if e != nil {
			return e
		}
		name := f.Name()
		if e = f.Close(); e != nil {
			_ = os.Remove(name)
			return e
		}
		if e = os.Remove(name); e != nil {
			return e
		}
	}
	return nil
}

func Path(root, relative string) (string, error) {
	if !filepath.IsLocal(relative) {
		return "", errors.New("invalid storage path")
	}
	return filepath.Join(root, relative), nil
}
