//go:build !unix

package store

import "os"

func acquireLock(*os.File) error {
	return nil
}

func releaseLock(*os.File) error {
	return nil
}
