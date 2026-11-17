package agentinstall

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type jdkTransport struct {
	home     string
	tars     int
	tarError error
}

func (t *jdkTransport) Run(ctx context.Context, args []string, input io.Reader) ([]byte, error) {
	if args[0] == "readlink" {
		return []byte(t.home + "/bin/java\n"), nil
	}
	if args[0] == "uname" {
		return []byte("x86_64\n"), nil
	}
	if args[0] == "tar" {
		t.tars++
		if t.tarError != nil {
			return nil, t.tarError
		}
	}
	if args[0] == "mv" {
		return nil, os.Rename(args[len(args)-2], args[len(args)-1])
	}
	return (CommandTransport{Destination: Destination{Kind: "local"}}).Run(ctx, args, input)
}

func javaFixture(root, version string, tools bool) string {
	Expect(os.MkdirAll(filepath.Join(root, "bin"), 0o700)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(root, "release"), []byte("JAVA_VERSION=\""+version+"\"\n"), 0o600)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(root, "bin", "java"), []byte("fixture executable"), 0o755)).To(Succeed())
	if tools {
		Expect(os.Mkdir(filepath.Join(root, "lib"), 0o700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(root, "lib", "tools.jar"), []byte("fixture attach API"), 0o600)).To(Succeed())
	}
	return root
}

var _ = Describe("automatic attach JDK provisioning", func() {
	var transport *jdkTransport
	var installer Installer
	var options Options
	var downloads int
	BeforeEach(func() {
		root := GinkgoT().TempDir()
		transport = &jdkTransport{home: javaFixture(filepath.Join(root, "runtime"), "1.8.0_212", false)}
		options = Options{PID: 321, Directory: filepath.Join(root, "target")}
		downloads = 0
		installer = Installer{DownloadJDK: func(_ context.Context, request JDKRequest) (JDK, error) {
			downloads++
			Expect(request).To(Equal(JDKRequest{Major: 8, OS: "linux", Arch: "amd64"}))
			home := javaFixture(filepath.Join(root, "download"), "1.8.0_512", true)
			Expect(os.Symlink("java", filepath.Join(home, "bin", "java_g"))).To(Succeed())
			return JDK{Home: home, Version: "8.0.512+9"}, nil
		}}
	})

	It("copies a compatible JDK with symlinks and executable modes and reuses it without downloading", func() {
		home, err := installer.javaHome(context.Background(), transport, options)
		Expect(err).NotTo(HaveOccurred())
		Expect(home).To(Equal(filepath.Join(options.Directory, "jdk-8-amd64")))
		data, err := os.ReadFile(filepath.Join(home, "lib", "tools.jar"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(Equal("fixture attach API"))
		link, err := os.Readlink(filepath.Join(home, "bin", "java_g"))
		Expect(err).NotTo(HaveOccurred())
		Expect(link).To(Equal("java"))
		info, err := os.Stat(filepath.Join(home, "bin", "java"))
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o755)))
		again, err := installer.javaHome(context.Background(), transport, options)
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(Equal(home))
		Expect([]int{downloads, transport.tars}).To(Equal([]int{1, 1}))
	})

	It("keeps a full target JDK without downloading", func() {
		transport.home = javaFixture(filepath.Join(GinkgoT().TempDir(), "full"), "1.8.0_212", true)
		home, err := installer.javaHome(context.Background(), transport, options)
		Expect(err).NotTo(HaveOccurred())
		Expect(home).To(Equal(transport.home))
		Expect(downloads).To(BeZero())
	})

	It("honors an explicit JDK and refuses an explicit JRE", func() {
		options.JavaHome = javaFixture(filepath.Join(GinkgoT().TempDir(), "explicit"), "1.8.0_512", true)
		home, err := installer.javaHome(context.Background(), transport, options)
		Expect(err).NotTo(HaveOccurred())
		Expect(home).To(Equal(options.JavaHome))
		options.JavaHome = transport.home
		_, err = installer.javaHome(context.Background(), transport, options)
		Expect(err).To(MatchError(ContainSubstring("--java-home")))
		Expect(downloads).To(BeZero())
	})

	It("reports download failures without staging an incomplete JDK", func() {
		installer.DownloadJDK = func(context.Context, JDKRequest) (JDK, error) { return JDK{}, errors.New("download denied") }
		_, err := installer.javaHome(context.Background(), transport, options)
		Expect(err).To(MatchError(ContainSubstring("download denied")))
		Expect(transport.tars).To(BeZero())
	})

	It("refuses a downloaded JRE without an Attach API", func() {
		installer.DownloadJDK = func(context.Context, JDKRequest) (JDK, error) { return JDK{Home: transport.home, Version: "8"}, nil }
		_, err := installer.javaHome(context.Background(), transport, options)
		Expect(err).To(MatchError(ContainSubstring("Attach API")))
		Expect(transport.tars).To(BeZero())
	})

	It("cleans a failed copy without publishing an incomplete JDK", func() {
		transport.tarError = errors.New("incomplete tar stream")
		_, err := installer.javaHome(context.Background(), transport, options)
		Expect(err).To(MatchError(ContainSubstring("incomplete tar stream")))
		Expect(filepath.Glob(filepath.Join(options.Directory, "jdk-*"))).To(BeEmpty())
	})

	It("refuses a staged JDK from another Java major without downloading over it", func() {
		staged := javaFixture(filepath.Join(options.Directory, "jdk-8-amd64"), "17.0.17", false)
		Expect(os.WriteFile(filepath.Join(staged, "release"), []byte("JAVA_VERSION=\"17.0.17\"\nMODULES=\"java.base jdk.attach\"\n"), 0o600)).To(Succeed())
		_, err := installer.javaHome(context.Background(), transport, options)
		Expect(err).To(MatchError(ContainSubstring("Java 8 Attach API")))
		Expect(downloads).To(BeZero())
	})

	It("recognizes a modular Attach API and rejects missing version metadata", func() {
		options.JavaHome = javaFixture(filepath.Join(GinkgoT().TempDir(), "modular"), "17.0.17", false)
		Expect(os.WriteFile(filepath.Join(options.JavaHome, "release"), []byte("JAVA_VERSION=\"17.0.17\"\nMODULES=\"java.base jdk.attach\"\n"), 0o600)).To(Succeed())
		home, err := installer.javaHome(context.Background(), transport, options)
		Expect(err).NotTo(HaveOccurred())
		Expect(home).To(Equal(options.JavaHome))
		Expect(os.WriteFile(filepath.Join(options.JavaHome, "release"), nil, 0o600)).To(Succeed())
		_, err = installer.javaHome(context.Background(), transport, options)
		Expect(err).To(MatchError(ContainSubstring("JAVA_VERSION")))
	})
})
