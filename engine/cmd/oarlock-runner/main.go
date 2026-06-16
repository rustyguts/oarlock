// oarlock-runner is the emissary injected into Kubernetes container-step pods.
// An init container runs it in "prepare" mode (copy itself into the shared
// volume + download input artifacts); the main (user) container runs the copied
// binary in "exec" mode (run the user command, upload outputs, write a result
// manifest to object storage). It does all S3 I/O itself so pods need only
// object-store access, never cluster credentials or the oarlock DB.
//
// Contract via env (set by the K8s runtime):
//   OARLOCK_S3_ENDPOINT/_ACCESS_KEY/_SECRET_KEY/_BUCKET/_REGION/_USE_SSL
//   OARLOCK_INPUTS         JSON [{"key","dest"}]    (prepare)
//   OARLOCK_ARGV           JSON []string            (exec)
//   OARLOCK_OUTPUT_PREFIX  object-key prefix        (exec)
//   OARLOCK_OUTPUT_GLOBS   JSON []string            (exec)
//   OARLOCK_RESULT_KEY     object key for result    (exec)
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

const (
	inDir   = "/oarlock/in"
	outDir  = "/oarlock/out"
	binDest = "/oarlock/bin/runner"
	maxCap  = 1 << 20 // 1MB stdout/stderr captured into the result manifest
)

type inputSpec struct {
	Key  string `json:"key"`
	Dest string `json:"dest"`
}

type outputRef struct {
	Name        string `json:"name"`
	Key         string `json:"key"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
}

type result struct {
	ExitCode   int         `json:"exit_code"`
	Stdout     string      `json:"stdout"`
	StderrTail string      `json:"stderr_tail"`
	Outputs    []outputRef `json:"outputs"`
	StartedAt  string      `json:"started_at"`
	FinishedAt string      `json:"finished_at"`
}

func main() {
	if len(os.Args) < 2 {
		fatal("usage: oarlock-runner <prepare|exec>")
	}
	switch os.Args[1] {
	case "prepare":
		prepare()
	case "exec":
		execMode()
	default:
		fatal("unknown mode %q", os.Args[1])
	}
}

func prepare() {
	// Copy this binary into the shared volume for the main (user) container.
	self, err := os.Executable()
	must(err, "locate self")
	must(os.MkdirAll(path.Dir(binDest), 0o755), "mkdir bin")
	copyFileToPath(self, binDest)
	must(os.Chmod(binDest, 0o755), "chmod runner")
	must(os.MkdirAll(inDir, 0o777), "mkdir in")
	must(os.MkdirAll(outDir, 0o777), "mkdir out")

	mc, bucket := s3client()
	var inputs []inputSpec
	parseEnvJSON("OARLOCK_INPUTS", &inputs)
	ctx := context.Background()
	for _, in := range inputs {
		obj, err := mc.GetObject(ctx, bucket, in.Key, minio.GetObjectOptions{})
		must(err, "get input %s", in.Key)
		dst := filepath.Join(inDir, safeBase(in.Dest))
		f, err := os.Create(dst)
		must(err, "create %s", dst)
		_, err = io.Copy(f, obj)
		f.Close()
		obj.Close()
		must(err, "download %s", in.Key)
		fmt.Fprintf(os.Stderr, "[oarlock] staged %s -> %s\n", in.Key, dst)
	}
}

func execMode() {
	var argv []string
	parseEnvJSON("OARLOCK_ARGV", &argv)
	if len(argv) == 0 {
		fatal("OARLOCK_ARGV is empty; k8s container steps must specify a command")
	}
	_ = os.MkdirAll(outDir, 0o777)

	started := time.Now().UTC()
	outBuf := &capped{max: maxCap}
	errBuf := &capped{max: maxCap}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = outDir
	cmd.Stdout = io.MultiWriter(os.Stdout, outBuf)
	cmd.Stderr = io.MultiWriter(os.Stderr, errBuf)
	runErr := cmd.Run()
	finished := time.Now().UTC()
	exitCode := 0
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			exitCode = 127
			fmt.Fprintf(os.Stderr, "[oarlock] exec error: %v\n", runErr)
		}
	}

	outputs := uploadOutputs()
	res := result{
		ExitCode:   exitCode,
		Stdout:     string(outBuf.b),
		StderrTail: string(errBuf.b),
		Outputs:    outputs,
		StartedAt:  started.Format(time.RFC3339Nano),
		FinishedAt: finished.Format(time.RFC3339Nano),
	}
	writeResult(res)
	os.Exit(exitCode)
}

func uploadOutputs() []outputRef {
	mc, bucket := s3client()
	prefix := os.Getenv("OARLOCK_OUTPUT_PREFIX")
	var globs []string
	parseEnvJSON("OARLOCK_OUTPUT_GLOBS", &globs)
	if len(globs) == 0 {
		globs = []string{"*"}
	}
	entries, err := os.ReadDir(outDir)
	if err != nil {
		return nil
	}
	ctx := context.Background()
	var refs []outputRef
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !matchAny(globs, name) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		f, err := os.Open(filepath.Join(outDir, name))
		if err != nil {
			continue
		}
		ct := contentType(name)
		key := strings.TrimRight(prefix, "/") + "/" + name
		_, err = mc.PutObject(ctx, bucket, key, f, info.Size(), minio.PutObjectOptions{ContentType: ct})
		f.Close()
		if err != nil {
			fmt.Fprintf(os.Stderr, "[oarlock] upload %s failed: %v\n", key, err)
			continue
		}
		refs = append(refs, outputRef{Name: name, Key: key, Size: info.Size(), ContentType: ct})
		fmt.Fprintf(os.Stderr, "[oarlock] uploaded %s (%d bytes)\n", key, info.Size())
	}
	return refs
}

func writeResult(res result) {
	key := os.Getenv("OARLOCK_RESULT_KEY")
	if key == "" {
		return
	}
	mc, bucket := s3client()
	body, _ := json.Marshal(res)
	_, err := mc.PutObject(context.Background(), bucket, key, bytes.NewReader(body), int64(len(body)),
		minio.PutObjectOptions{ContentType: "application/json"})
	if err != nil {
		fmt.Fprintf(os.Stderr, "[oarlock] write result failed: %v\n", err)
	}
}

func s3client() (*minio.Client, string) {
	endpoint := os.Getenv("OARLOCK_S3_ENDPOINT")
	mc, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(os.Getenv("OARLOCK_S3_ACCESS_KEY"), os.Getenv("OARLOCK_S3_SECRET_KEY"), ""),
		Secure: os.Getenv("OARLOCK_S3_USE_SSL") == "true",
		Region: os.Getenv("OARLOCK_S3_REGION"),
	})
	must(err, "s3 client")
	return mc, os.Getenv("OARLOCK_S3_BUCKET")
}

// capped is an io.Writer that retains at most max bytes.
type capped struct {
	b   []byte
	max int
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.max - len(c.b); room > 0 {
		if len(p) <= room {
			c.b = append(c.b, p...)
		} else {
			c.b = append(c.b, p[:room]...)
		}
	}
	return len(p), nil
}

func parseEnvJSON(key string, v any) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return
	}
	if err := json.Unmarshal([]byte(raw), v); err != nil {
		fatal("invalid %s: %v", key, err)
	}
}

func copyFileToPath(src, dst string) {
	in, err := os.Open(src)
	must(err, "open self")
	defer in.Close()
	out, err := os.Create(dst)
	must(err, "create %s", dst)
	defer out.Close()
	_, err = io.Copy(out, in)
	must(err, "copy self")
}

func matchAny(globs []string, name string) bool {
	for _, g := range globs {
		if ok, _ := path.Match(g, name); ok {
			return true
		}
	}
	return false
}

func contentType(name string) string {
	if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

func safeBase(name string) string {
	b := path.Base(strings.ReplaceAll(name, "\\", "/"))
	if b == "" || b == "." || b == ".." {
		return "file"
	}
	return b
}

func must(err error, format string, args ...any) {
	if err != nil {
		fatal(format+": %v", append(args, err)...)
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[oarlock-runner] "+format+"\n", args...)
	os.Exit(1)
}
