package agentinstall

import (
	"context"
	_ "embed"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
)

type JDKRequest struct {
	Major    int
	OS, Arch string
}

type JDK struct {
	Home, Version string
}

//go:embed java_runtime.sh
var javaRuntimeScript string

var javaMajorPattern = regexp.MustCompile(`^(?:1\.)?([0-9]+)(?:[._+\-]|$)`)

type javaRuntime struct {
	Present, Attach bool
	Major           int
}

func (i Installer) javaHome(ctx context.Context, transport Transport, options Options) (string, error) {
	home := options.JavaHome
	if home == "" {
		var err error
		home, err = targetJavaHome(ctx, transport, options.PID)
		if err != nil {
			return "", err
		}
	}
	info, err := inspectJavaHome(ctx, transport, home)
	if err != nil {
		return "", err
	}
	if !info.Present {
		return "", fmt.Errorf("Java home %q does not exist", home)
	}
	if info.Attach {
		return home, nil
	}
	if options.JavaHome != "" {
		return "", fmt.Errorf("--java-home %q lacks the Java %d Attach API; choose a full compatible JDK", home, info.Major)
	}
	arch, err := transport.Run(ctx, []string{"uname", "-m"}, nil)
	if err != nil {
		return "", fmt.Errorf("inspect target JDK architecture: %w", err)
	}
	request := JDKRequest{Major: info.Major, OS: "linux"}
	switch strings.TrimSpace(string(arch)) {
	case "x86_64", "amd64":
		request.Arch = "amd64"
	case "aarch64", "arm64":
		request.Arch = "arm64"
	default:
		return "", fmt.Errorf("unsupported target JDK architecture %q", strings.TrimSpace(string(arch)))
	}
	return i.provisionJDK(ctx, transport, options.Directory, request)
}

func inspectJavaHome(ctx context.Context, transport Transport, home string) (javaRuntime, error) {
	output, err := transport.Run(ctx, []string{"sh", "-c", javaRuntimeScript, "telltale-java-runtime", home}, nil)
	if err != nil {
		return javaRuntime{}, fmt.Errorf("inspect Java home %q: %w", home, err)
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(output), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			values[key] = strings.Trim(value, `"`)
		}
	}
	if values["__TELLTALE_PRESENT__"] == "false" {
		return javaRuntime{}, nil
	}
	match := javaMajorPattern.FindStringSubmatch(values["JAVA_VERSION"])
	if values["__TELLTALE_PRESENT__"] != "true" || len(match) != 2 {
		return javaRuntime{}, fmt.Errorf("Java home %q has missing or invalid JAVA_VERSION metadata", home)
	}
	major, err := strconv.Atoi(match[1])
	if err != nil || major < 8 {
		return javaRuntime{}, fmt.Errorf("Java home %q has unsupported JAVA_VERSION %q", home, values["JAVA_VERSION"])
	}
	attach := values["__TELLTALE_TOOLS__"] == "true"
	if major >= 9 {
		attach = values["__TELLTALE_ATTACH_MODULE__"] == "true" || strings.Contains(" "+values["MODULES"]+" ", " jdk.attach ")
	}
	return javaRuntime{Present: true, Attach: attach, Major: major}, nil
}

func (i Installer) provisionJDK(ctx context.Context, transport Transport, directory string, request JDKRequest) (string, error) {
	home := path.Join(directory, fmt.Sprintf("jdk-%d-%s", request.Major, request.Arch))
	info, err := inspectJavaHome(ctx, transport, home)
	if err != nil {
		return "", err
	}
	if info.Present {
		if info.Major != request.Major || !info.Attach {
			return "", fmt.Errorf("staged JDK %q lacks the Java %d Attach API", home, request.Major)
		}
		return home, nil
	}
	download := i.DownloadJDK
	if download == nil {
		download = downloadJDK
	}
	jdk, err := download(ctx, request)
	if err != nil {
		return "", fmt.Errorf("download Java %d JDK with deps: %w", request.Major, err)
	}
	if jdk.Home == "" || jdk.Version == "" {
		return "", fmt.Errorf("deps returned an incomplete JDK installation")
	}
	info, err = inspectJavaHome(ctx, CommandTransport{Destination: Destination{Kind: "local"}}, jdk.Home)
	if err != nil {
		return "", err
	}
	if !info.Present || info.Major != request.Major || !info.Attach {
		return "", fmt.Errorf("downloaded JDK %q lacks the Java %d Attach API", jdk.Home, request.Major)
	}
	if err := copyJDK(ctx, transport, jdkCopy{Source: jdk.Home, Target: home, Major: request.Major}); err != nil {
		return "", err
	}
	return home, nil
}
