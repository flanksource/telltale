package agentinstall

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/flanksource/deps"
)

func downloadJDK(ctx context.Context, request JDKRequest) (JDK, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return JDK{}, err
	}
	root := filepath.Join(cache, "telltale", "jdks", request.OS+"-"+request.Arch, fmt.Sprint(request.Major))
	if err := os.MkdirAll(".tmp/telltale-jdk", 0o700); err != nil {
		return JDK{}, err
	}
	result, err := deps.InstallWithContext(ctx, fmt.Sprintf("openjdk%d", request.Major), "latest",
		deps.WithOS(request.OS, request.Arch), deps.WithStrictChecksum(true),
		deps.WithAppDir(filepath.Join(root, "apps")), deps.WithBinDir(filepath.Join(root, "bin")),
		deps.WithTmpDir(".tmp/telltale-jdk"), deps.WithCacheDir(filepath.Join(root, "downloads")),
		deps.WithTimeout(30*time.Minute))
	if err != nil {
		return JDK{}, err
	}
	if result == nil || result.Package.Name == "" || result.Version.Version == "" {
		return JDK{}, fmt.Errorf("deps returned no installed JDK package or version")
	}
	return JDK{Home: filepath.Join(root, "apps", result.Package.FolderName(result.Version.Version)), Version: result.Version.Version}, nil
}
