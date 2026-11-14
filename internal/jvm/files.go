package jvm

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

type ScriptOptions struct {
	Source         string
	File           string
	Args           string
	Loader         string
	TimeoutSeconds int
}

func ScriptParameters(options ScriptOptions) (json.RawMessage, error) {
	if options.Source != "" && options.File != "" {
		return nil, fmt.Errorf("choose either source or file")
	}
	if options.File != "" {
		source, err := os.ReadFile(options.File)
		if err != nil {
			return nil, fmt.Errorf("read script: %w", err)
		}
		options.Source = string(source)
	}
	if strings.TrimSpace(options.Source) == "" {
		return nil, fmt.Errorf("script source is required")
	}
	if options.Args == "" {
		options.Args = "{}"
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal([]byte(options.Args), &args); err != nil {
		return nil, fmt.Errorf("script arguments: %w", err)
	}
	if args == nil {
		return nil, fmt.Errorf("script arguments must be an object")
	}
	params := map[string]any{"source": options.Source, "args": args}
	if options.Loader != "" {
		params["loader"] = options.Loader
	}
	if options.TimeoutSeconds != 0 {
		params["timeoutSeconds"] = options.TimeoutSeconds
	}
	return json.Marshal(params)
}

func ExtensionParameters(path string) (params json.RawMessage, err error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	const limit = 16 << 20
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, fmt.Errorf("extension exceeds 16 MiB")
	}
	return json.Marshal(map[string]string{"jar": base64.StdEncoding.EncodeToString(data)})
}
