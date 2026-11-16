package jvm

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
)

// paramFileLimit bounds a file read into one operation param, matching the
// agent's 16 MiB extension upload limit.
const paramFileLimit = 16 << 20

// ReadParamFile is a local file's content as an operation param: its text, or
// base64 for a binary param such as an extension JAR.
func ReadParamFile(path string, encode bool) (content string, err error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	data, err := io.ReadAll(io.LimitReader(file, paramFileLimit+1))
	if err != nil {
		return "", err
	}
	if len(data) > paramFileLimit {
		return "", fmt.Errorf("%s exceeds 16 MiB", path)
	}
	if encode {
		return base64.StdEncoding.EncodeToString(data), nil
	}
	return string(data), nil
}
