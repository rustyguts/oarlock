// Package container implements ContainerRuntime backends. docker.go is the
// local backend: it drives the Docker Engine API over the unix socket with raw
// HTTP (no heavyweight SDK), pinned to a stable API version. Files are staged
// via the archive (docker cp) API rather than bind mounts, so it works even
// when the engine itself runs in a container (bind-mount host paths are resolved
// by the daemon, not the caller — the classic socket-in-container gotcha).
//
// Security: socket access is host-root-equivalent. This backend is intended for
// local/single-tenant dev only; multi-tenant production uses the k8s backend.
package container

import (
	"archive/tar"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/rustyguts/oarlock/engine/internal/steps"
)

const (
	dockerAPIVersion = "/v1.41" // Docker 20.10+; covers create/start/logs/archive
	maxStdoutCapture = 1 << 20  // 1MB stdout captured for structured output
	maxStderrCapture = 64 << 10 // 64KB stderr tail for logs
)

// Docker is a ContainerRuntime backed by a local Docker daemon.
type Docker struct {
	http   *http.Client
	store  steps.ArtifactStore
	log    *slog.Logger
	socket string
}

// NewDocker returns a Docker runtime dialing the given socket (default
// /var/run/docker.sock). It pings the daemon so misconfiguration fails fast.
func NewDocker(ctx context.Context, socket string, store steps.ArtifactStore, log *slog.Logger) (*Docker, error) {
	if socket == "" {
		socket = "/var/run/docker.sock"
	}
	d := &Docker{
		socket: socket,
		store:  store,
		log:    log,
		http: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "unix", socket)
				},
			},
		},
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	resp, err := d.do(pingCtx, http.MethodGet, "/_ping", nil, nil, "")
	if err != nil {
		return nil, fmt.Errorf("docker: cannot reach daemon at %s: %w", socket, err)
	}
	resp.Body.Close()
	return d, nil
}

func (d *Docker) do(ctx context.Context, method, p string, query url.Values, body io.Reader, contentType string) (*http.Response, error) {
	u := "http://docker" + dockerAPIVersion + p
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return d.http.Do(req)
}

// readErr drains an error response body into an error.
func readErr(resp *http.Response, op string) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	resp.Body.Close()
	msg := strings.TrimSpace(string(b))
	var m struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(b, &m) == nil && m.Message != "" {
		msg = m.Message
	}
	return fmt.Errorf("docker %s: %s (%d)", op, msg, resp.StatusCode)
}

func (d *Docker) Backend() string { return "docker" }

func (d *Docker) Submit(ctx context.Context, spec steps.ContainerSpec) (steps.Handle, error) {
	if err := d.pull(ctx, spec.Image, spec.Registry); err != nil {
		return nil, err
	}
	id, err := d.create(ctx, spec)
	if err != nil {
		return nil, err
	}
	// Stage inputs (and create /oarlock/{in,out}) before start.
	if err := d.stageInputs(ctx, id, spec.Inputs); err != nil {
		_ = d.removeContainer(context.WithoutCancel(ctx), id)
		return nil, err
	}
	if err := d.start(ctx, id); err != nil {
		_ = d.removeContainer(context.WithoutCancel(ctx), id)
		return nil, err
	}
	globs := spec.OutputGlobs
	if len(globs) == 0 {
		globs = []string{"*"}
	}
	gAny := make([]any, len(globs))
	for i, g := range globs {
		gAny[i] = g
	}
	return steps.Handle{
		"kind":         "docker",
		"id":           id,
		"workspace_id": spec.WorkspaceID.String(),
		"run_id":       spec.RunID.String(),
		"task_id":      spec.TaskID.String(),
		"output_globs": gAny,
		"deadline":     float64(time.Now().Add(time.Duration(spec.TimeoutSec) * time.Second).Unix()),
	}, nil
}

func (d *Docker) Poll(ctx context.Context, h steps.Handle) (steps.ContainerStatus, error) {
	id := handleStr(h, "id")
	st, err := d.inspect(ctx, id)
	if err != nil {
		return steps.ContainerStatus{}, err
	}
	// Enforce the timeout: kill an overrunning container so the slot/cost stops.
	if deadline := int64(handleFloat(h, "deadline")); deadline > 0 && st.State.Running && time.Now().Unix() > deadline {
		_ = d.Cancel(ctx, h)
		code := 124
		return steps.ContainerStatus{Phase: steps.PhaseFailed, ExitCode: &code, Message: "container timed out"}, nil
	}
	switch st.State.Status {
	case "created", "running", "restarting", "paused":
		ph := steps.PhaseRunning
		if st.State.Status == "created" {
			ph = steps.PhasePending
		}
		return steps.ContainerStatus{Phase: ph}, nil
	case "exited", "dead":
		code := st.State.ExitCode
		ph := steps.PhaseSucceeded
		if code != 0 {
			ph = steps.PhaseFailed
		}
		return steps.ContainerStatus{Phase: ph, ExitCode: &code}, nil
	default:
		return steps.ContainerStatus{Phase: steps.PhaseRunning, Message: st.State.Status}, nil
	}
}

func (d *Docker) Result(ctx context.Context, h steps.Handle) (steps.ContainerResult, error) {
	id := handleStr(h, "id")
	st, err := d.inspect(ctx, id)
	if err != nil {
		return steps.ContainerResult{}, err
	}
	stdout, stderr, err := d.captureLogs(ctx, id)
	if err != nil {
		d.log.Warn("docker: capture logs failed", "id", id, "error", err)
	}
	outputs, err := d.collectOutputs(ctx, h)
	if err != nil {
		d.log.Warn("docker: collect outputs failed", "id", id, "error", err)
	}
	started, _ := time.Parse(time.RFC3339Nano, st.State.StartedAt)
	finished, _ := time.Parse(time.RFC3339Nano, st.State.FinishedAt)
	return steps.ContainerResult{
		ExitCode:   st.State.ExitCode,
		Stdout:     stdout,
		StderrTail: stderr,
		Outputs:    outputs,
		StartedAt:  started,
		FinishedAt: finished,
	}, nil
}

func (d *Docker) Logs(ctx context.Context, h steps.Handle, since time.Time) (io.ReadCloser, error) {
	stdout, stderr, err := d.captureLogs(ctx, handleStr(h, "id"))
	if err != nil {
		return nil, err
	}
	combined := append(append([]byte{}, stderr...), stdout...)
	return io.NopCloser(strings.NewReader(string(combined))), nil
}

func (d *Docker) Cancel(ctx context.Context, h steps.Handle) error {
	id := handleStr(h, "id")
	resp, err := d.do(ctx, http.MethodPost, "/containers/"+id+"/kill", nil, nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// 404 (gone) and 409 (not running) are fine — idempotent.
	if resp.StatusCode >= 400 && resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusConflict {
		return readErr(resp, "kill")
	}
	return nil
}

func (d *Docker) Cleanup(ctx context.Context, h steps.Handle) error {
	return d.removeContainer(ctx, handleStr(h, "id"))
}

// --- Docker API calls ---

func (d *Docker) pull(ctx context.Context, image string, auth *steps.RegistryAuth) error {
	q := url.Values{}
	if ref, tag, ok := strings.Cut(image, "@"); ok {
		q.Set("fromImage", ref)
		q.Set("tag", "@"+tag) // pull by digest
	} else {
		name, tag := splitTag(image)
		q.Set("fromImage", name)
		q.Set("tag", tag)
	}
	resp, err := d.doWithRegistryAuth(ctx, http.MethodPost, "/images/create", q, nil, "", auth)
	if err != nil {
		return fmt.Errorf("docker pull %s: %w", image, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return readErr(resp, "pull "+image)
	}
	// The body streams pull progress as JSON lines; drain to completion so the
	// image is present before create.
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

func (d *Docker) doWithRegistryAuth(ctx context.Context, method, p string, q url.Values, body io.Reader, ct string, auth *steps.RegistryAuth) (*http.Response, error) {
	u := "http://docker" + dockerAPIVersion + p
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	if auth != nil && (auth.Username != "" || auth.Password != "") {
		cfg, _ := json.Marshal(map[string]string{"username": auth.Username, "password": auth.Password})
		req.Header.Set("X-Registry-Auth", base64.URLEncoding.EncodeToString(cfg))
	}
	return d.http.Do(req)
}

func (d *Docker) create(ctx context.Context, spec steps.ContainerSpec) (string, error) {
	env := make([]string, 0, len(spec.Env))
	for k, v := range spec.Env {
		env = append(env, k+"="+v)
	}
	body := map[string]any{
		"Image":      spec.Image,
		"Env":        env,
		"Tty":        false,
		"WorkingDir": steps.ContainerOutputDir,
		"HostConfig": map[string]any{
			"AutoRemove": false,
			"Memory":     int64(spec.MemoryMB) * 1024 * 1024,
			"NanoCpus":   nanoCPUs(spec.CPU),
		},
	}
	// Command overrides the image ENTRYPOINT; Args become the Cmd. This lets a
	// step run a different binary in an image (e.g. ffprobe in an ffmpeg image).
	if len(spec.Command) > 0 {
		body["Entrypoint"] = spec.Command
	}
	if len(spec.Args) > 0 {
		body["Cmd"] = spec.Args
	}
	raw, _ := json.Marshal(body)
	resp, err := d.do(ctx, http.MethodPost, "/containers/create", nil, strings.NewReader(string(raw)), "application/json")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", readErr(resp, "create")
	}
	var out struct {
		ID string `json:"Id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// stageInputs PUTs a tar that creates /oarlock/{in,out} and writes each input
// artifact into /oarlock/in. Bytes stream straight from the artifact store.
func (d *Docker) stageInputs(ctx context.Context, id string, inputs []steps.ArtifactMount) error {
	pr, pw := io.Pipe()
	go func() {
		tw := tar.NewWriter(pw)
		writeDir := func(name string) error {
			return tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeDir, Mode: 0o777})
		}
		var ferr error
		defer func() { pw.CloseWithError(ferr) }()
		if ferr = writeDir("oarlock/"); ferr != nil {
			return
		}
		if ferr = writeDir("oarlock/in/"); ferr != nil {
			return
		}
		if ferr = writeDir("oarlock/out/"); ferr != nil {
			return
		}
		for _, in := range inputs {
			var rc io.ReadCloser
			rc, ferr = d.store.Download(ctx, in.Key)
			if ferr != nil {
				return
			}
			if ferr = tw.WriteHeader(&tar.Header{
				Name: "oarlock/in/" + safeBase(in.DestName),
				Mode: 0o644, Size: in.Size, Typeflag: tar.TypeReg,
			}); ferr != nil {
				rc.Close()
				return
			}
			_, ferr = io.Copy(tw, rc)
			rc.Close()
			if ferr != nil {
				return
			}
		}
		ferr = tw.Close()
	}()

	q := url.Values{"path": {"/"}}
	resp, err := d.do(ctx, http.MethodPut, "/containers/"+id+"/archive", q, pr, "application/x-tar")
	if err != nil {
		return fmt.Errorf("docker stage inputs: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return readErr(resp, "stage inputs")
	}
	return nil
}

func (d *Docker) start(ctx context.Context, id string) error {
	resp, err := d.do(ctx, http.MethodPost, "/containers/"+id+"/start", nil, nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 && resp.StatusCode != http.StatusNotModified {
		return readErr(resp, "start")
	}
	return nil
}

type inspectResult struct {
	State struct {
		Status     string `json:"Status"`
		Running    bool   `json:"Running"`
		ExitCode   int    `json:"ExitCode"`
		StartedAt  string `json:"StartedAt"`
		FinishedAt string `json:"FinishedAt"`
	} `json:"State"`
}

func (d *Docker) inspect(ctx context.Context, id string) (inspectResult, error) {
	var out inspectResult
	resp, err := d.do(ctx, http.MethodGet, "/containers/"+id+"/json", nil, nil, "")
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return out, readErr(resp, "inspect")
	}
	return out, json.NewDecoder(resp.Body).Decode(&out)
}

func (d *Docker) captureLogs(ctx context.Context, id string) (stdout, stderr []byte, err error) {
	q := url.Values{"stdout": {"1"}, "stderr": {"1"}}
	resp, err := d.do(ctx, http.MethodGet, "/containers/"+id+"/logs", q, nil, "")
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, nil, readErr(resp, "logs")
	}
	return demux(resp.Body, maxStdoutCapture, maxStderrCapture)
}

// collectOutputs GETs the /oarlock/out tar, uploads files matching the globs,
// and records artifact rows.
func (d *Docker) collectOutputs(ctx context.Context, h steps.Handle) ([]steps.ArtifactRef, error) {
	id := handleStr(h, "id")
	globs := handleStrSlice(h, "output_globs")
	if len(globs) == 0 {
		globs = []string{"*"}
	}
	ws, _ := uuidFrom(handleStr(h, "workspace_id"))
	run, _ := uuidFrom(handleStr(h, "run_id"))
	task, _ := uuidFrom(handleStr(h, "task_id"))

	q := url.Values{"path": {steps.ContainerOutputDir}}
	resp, err := d.do(ctx, http.MethodGet, "/containers/"+id+"/archive", q, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // no output dir
	}
	if resp.StatusCode >= 400 {
		return nil, readErr(resp, "get outputs")
	}

	var refs []steps.ArtifactRef
	tr := tar.NewReader(resp.Body)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return refs, err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		// Entry names are prefixed with the base dir, e.g. "out/result.mp4".
		name := strings.TrimPrefix(hdr.Name, "out/")
		if name == "" || strings.Contains(name, "/") {
			continue // top-level files only in v0
		}
		if !matchAny(globs, name) {
			continue
		}
		key := d.store.OutputKey(ws, run, task, name)
		if err := d.store.Upload(ctx, key, tr, hdr.Size, contentType(name)); err != nil {
			return refs, err
		}
		ref, err := d.store.Record(ctx, steps.ArtifactRecord{
			WorkspaceID: ws, RunID: &run, TaskID: &task,
			Key: key, Name: name, Size: hdr.Size, ContentType: contentType(name), Source: "output",
		})
		if err != nil {
			return refs, err
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

func (d *Docker) removeContainer(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	q := url.Values{"force": {"1"}, "v": {"1"}}
	resp, err := d.do(ctx, http.MethodDelete, "/containers/"+id, q, nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 && resp.StatusCode != http.StatusNotFound {
		return readErr(resp, "remove")
	}
	return nil
}

// --- helpers ---

// demux splits Docker's multiplexed log stream (8-byte frame headers) into
// stdout and stderr, each capped.
func demux(r io.Reader, maxOut, maxErr int) (stdout, stderr []byte, err error) {
	var hdr [8]byte
	for {
		if _, e := io.ReadFull(r, hdr[:]); e != nil {
			if e == io.EOF || e == io.ErrUnexpectedEOF {
				return stdout, stderr, nil
			}
			return stdout, stderr, e
		}
		n := binary.BigEndian.Uint32(hdr[4:])
		payload := make([]byte, n)
		if _, e := io.ReadFull(r, payload); e != nil {
			return stdout, stderr, nil
		}
		switch hdr[0] {
		case 2: // stderr
			if len(stderr) < maxErr {
				stderr = append(stderr, payload...)
			}
		default: // stdout (1) or tty (0)
			if len(stdout) < maxOut {
				stdout = append(stdout, payload...)
			}
		}
	}
}

func splitTag(image string) (name, tag string) {
	// Split on the last ":" only if it's after the last "/" (else it's a port).
	slash := strings.LastIndex(image, "/")
	colon := strings.LastIndex(image, ":")
	if colon > slash {
		return image[:colon], image[colon+1:]
	}
	return image, "latest"
}

func nanoCPUs(cpu string) int64 {
	cpu = strings.TrimSpace(cpu)
	if cpu == "" {
		return 0
	}
	if strings.HasSuffix(cpu, "m") {
		m, _ := strconv.ParseFloat(strings.TrimSuffix(cpu, "m"), 64)
		return int64(m * 1e6)
	}
	c, _ := strconv.ParseFloat(cpu, 64)
	return int64(c * 1e9)
}

func contentType(name string) string {
	if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

func matchAny(globs []string, name string) bool {
	for _, g := range globs {
		if ok, _ := path.Match(g, name); ok {
			return true
		}
	}
	return false
}

func safeBase(name string) string {
	b := path.Base(strings.ReplaceAll(name, "\\", "/"))
	if b == "" || b == "." || b == ".." {
		return "file"
	}
	return b
}

func uuidFrom(s string) (uuid.UUID, error) { return uuid.Parse(s) }

func handleStr(h steps.Handle, k string) string {
	if v, ok := h[k].(string); ok {
		return v
	}
	return ""
}

func handleFloat(h steps.Handle, k string) float64 {
	if v, ok := h[k].(float64); ok {
		return v
	}
	return 0
}

func handleStrSlice(h steps.Handle, k string) []string {
	raw, ok := h[k].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
