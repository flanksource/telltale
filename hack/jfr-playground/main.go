package main

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/flanksource/telltale/pkg/tracing/jfr/decode"
)

type fixture struct {
	Name         string               `json:"name"`
	File         string               `json:"file"`
	OriginalRows int                  `json:"originalRows"`
	Rows         []decode.StackSample `json:"rows"`
}

func main() {
	var fixtures []fixture
	for _, file := range os.Args[1:] {
		input, err := os.Open(file)
		if err != nil {
			panic(err)
		}
		reader, err := gzip.NewReader(input)
		if err != nil {
			panic(err)
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			panic(err)
		}
		if err := reader.Close(); err != nil {
			panic(err)
		}
		if err := input.Close(); err != nil {
			panic(err)
		}
		profile, err := decode.Decode(data, decode.DecodeOptions{Threads: true})
		if err != nil {
			panic(err)
		}
		sort.SliceStable(profile.Samples, func(i, j int) bool { return profile.Samples[i].Value > profile.Samples[j].Value })
		counts := map[string]int{}
		rows := []decode.StackSample{}
		for _, sample := range profile.Samples {
			if counts[sample.Event] < 12 {
				rows = append(rows, sample)
				counts[sample.Event]++
			}
		}
		name := filepath.Base(file)
		fmt.Fprintf(os.Stderr, "%s: %d decoded rows; retained %d: %v\n", name, len(profile.Samples), len(rows), counts)
		fixtures = append(fixtures, fixture{Name: name, File: file, OriginalRows: len(profile.Samples), Rows: rows})
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(fixtures); err != nil {
		panic(err)
	}
}
