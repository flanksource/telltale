package deploy

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
)

// warConfigEntry is where telltale-diag.war reads an embedded configuration.
const warConfigEntry = "WEB-INF/telltale-diag.properties"

// configureWAR is war with config embedded as its properties file, so a
// manager upload needs no file on the target's filesystem.
func configureWAR(war []byte, config map[string]string) ([]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(war), int64(len(war)))
	if err != nil {
		return nil, fmt.Errorf("read telltale-diag.war: %w", err)
	}
	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	for _, file := range reader.File {
		if file.Name == warConfigEntry {
			return nil, fmt.Errorf("telltale-diag.war already carries %s", warConfigEntry)
		}
		if err := copyEntry(writer, file); err != nil {
			return nil, fmt.Errorf("copy %s: %w", file.Name, err)
		}
	}
	entry, err := writer.Create(warConfigEntry)
	if err != nil {
		return nil, err
	}
	if _, err := io.WriteString(entry, properties(config)); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// copyEntry copies a file's compressed bytes as they are; a directory entry,
// which carries none, is re-created from its header.
func copyEntry(writer *zip.Writer, file *zip.File) error {
	if !file.FileInfo().IsDir() {
		return writer.Copy(file)
	}
	header := file.FileHeader
	header.Method = zip.Store
	header.CompressedSize64, header.UncompressedSize64, header.CRC32 = 0, 0, 0
	_, err := writer.CreateHeader(&header)
	return err
}

// properties is config as a java.util.Properties file, keys sorted.
func properties(config map[string]string) string {
	escape := strings.NewReplacer(`\`, `\\`, "\n", `\n`, "\r", `\r`, "\t", `\t`, "=", `\=`, ":", `\:`, "#", `\#`, "!", `\!`)
	var out strings.Builder
	for _, key := range slices.Sorted(maps.Keys(config)) {
		out.WriteString(escape.Replace(key) + "=" + escape.Replace(config[key]) + "\n")
	}
	return out.String()
}
