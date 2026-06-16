package container

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"path"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/rustyguts/oarlock/engine/internal/steps"
)

// K8sConfig configures the Kubernetes Jobs backend.
type K8sConfig struct {
	Kubeconfig  string // path; empty => in-cluster, then default kubeconfig
	Namespace   string
	RunnerImage string // oarlock-runner image (emissary)
	// Pod-facing object store (may differ from the engine's endpoint).
	S3Endpoint  string
	S3AccessKey string
	S3SecretKey string
	S3Bucket    string
	S3Region    string
	S3UseSSL    bool
}

// K8s runs each container step as a batch/v1 Job. An init container (the runner)
// stages inputs into a shared emptyDir and copies the runner binary in; the main
// (user) container runs the copied runner, which wraps the user command, uploads
// outputs, and writes a result manifest to object storage (the "emissary"
// pattern). The worker stays stateless (hard rule 1) — only the object store is
// shared. gVisor isolation comes from the compute target's RuntimeClass.
type K8s struct {
	cs    *kubernetes.Clientset
	store steps.ArtifactStore
	log   *slog.Logger
	cfg   K8sConfig
}

// NewK8s constructs the Kubernetes Jobs backend (emissary init-container +
// static oarlock-runner + result-via-object-store).
func NewK8s(ctx context.Context, store steps.ArtifactStore, cfg K8sConfig, log *slog.Logger) (steps.ContainerRuntime, error) {
	if cfg.RunnerImage == "" {
		return nil, fmt.Errorf("k8s runtime requires OARLOCK_RUNNER_IMAGE")
	}
	if cfg.Namespace == "" {
		cfg.Namespace = "default"
	}
	restCfg, err := loadRESTConfig(cfg.Kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("k8s config: %w", err)
	}
	cs, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("k8s client: %w", err)
	}
	// Fail fast on an unreachable API server.
	if _, err := cs.Discovery().ServerVersion(); err != nil {
		return nil, fmt.Errorf("k8s unreachable: %w", err)
	}
	return &K8s{cs: cs, store: store, cfg: cfg, log: log}, nil
}

func loadRESTConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	if c, err := rest.InClusterConfig(); err == nil {
		return c, nil
	}
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{}).ClientConfig()
}

func (k *K8s) Backend() string { return "k8s" }

func (k *K8s) Submit(ctx context.Context, spec steps.ContainerSpec) (steps.Handle, error) {
	argv := append(append([]string{}, spec.Command...), spec.Args...)
	if len(argv) == 0 {
		return nil, fmt.Errorf("k8s container steps must specify a command")
	}
	jobName := "oarlock-" + spec.TaskID.String()
	secretName := jobName + "-env"

	// Object keys (hard rule 7: ws/{workspace_id}/...), derived from the store.
	outX := k.store.OutputKey(spec.WorkspaceID, spec.RunID, spec.TaskID, "x")
	outputPrefix := path.Dir(outX)            // ws/.../tasks/<task>/out
	resultKey := path.Dir(outputPrefix) + "/.oarlock-result.json"

	inputs := make([]map[string]string, 0, len(spec.Inputs))
	for _, in := range spec.Inputs {
		inputs = append(inputs, map[string]string{"key": in.Key, "dest": in.DestName})
	}
	globs := spec.OutputGlobs
	if len(globs) == 0 {
		globs = []string{"*"}
	}

	plain := map[string]string{
		"OARLOCK_S3_ENDPOINT":   k.cfg.S3Endpoint,
		"OARLOCK_S3_BUCKET":     k.cfg.S3Bucket,
		"OARLOCK_S3_REGION":     k.cfg.S3Region,
		"OARLOCK_S3_USE_SSL":    fmt.Sprintf("%t", k.cfg.S3UseSSL),
		"OARLOCK_INPUTS":        jsonStr(inputs),
		"OARLOCK_OUTPUT_PREFIX": outputPrefix,
		"OARLOCK_OUTPUT_GLOBS":  jsonStr(globs),
		"OARLOCK_RESULT_KEY":    resultKey,
		"OARLOCK_ARGV":          jsonStr(argv),
	}
	// Secret env: object-store credentials + the user's (possibly secret) env.
	secretEnv := map[string]string{
		"OARLOCK_S3_ACCESS_KEY": k.cfg.S3AccessKey,
		"OARLOCK_S3_SECRET_KEY": k.cfg.S3SecretKey,
	}
	for key, val := range spec.Env {
		secretEnv[key] = val
	}

	// Per-Job Secret (env), owner-referenced to the Job for GC, also deleted on Cleanup.
	if _, err := k.cs.CoreV1().Secrets(k.cfg.Namespace).Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: secretName, Labels: labels(spec)},
		StringData: secretEnv,
	}, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return nil, fmt.Errorf("create env secret: %w", err)
	}

	var pullSecrets []corev1.LocalObjectReference
	if spec.Registry != nil && (spec.Registry.Username != "" || spec.Registry.Password != "") {
		pullName := jobName + "-pull"
		if err := k.createPullSecret(ctx, pullName, spec); err != nil {
			return nil, err
		}
		pullSecrets = append(pullSecrets, corev1.LocalObjectReference{Name: pullName})
	}

	envFrom := []corev1.EnvFromSource{{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: secretName}}}}
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: jobName, Labels: labels(spec)},
		Spec: batchv1.JobSpec{
			BackoffLimit:            int32Ptr(0), // the engine owns retries
			TTLSecondsAfterFinished: int32Ptr(600),
			ActiveDeadlineSeconds:   int64PtrIf(spec.TimeoutSec),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels(spec)},
				Spec: corev1.PodSpec{
					RestartPolicy:    corev1.RestartPolicyNever,
					RuntimeClassName: runtimeClass(spec.RuntimeClass),
					ImagePullSecrets: pullSecrets,
					Volumes: []corev1.Volume{{
						Name:         "oarlock",
						VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
					}},
					InitContainers: []corev1.Container{{
						Name:         "prepare",
						Image:        k.cfg.RunnerImage,
						Command:      []string{"/usr/local/bin/oarlock-runner", "prepare"},
						Env:          envVars(plain),
						EnvFrom:      envFrom,
						VolumeMounts: []corev1.VolumeMount{{Name: "oarlock", MountPath: "/oarlock"}},
					}},
					Containers: []corev1.Container{{
						Name:         "main",
						Image:        spec.Image,
						Command:      []string{"/oarlock/bin/runner", "exec"},
						WorkingDir:   steps.ContainerOutputDir,
						Env:          envVars(plain),
						EnvFrom:      envFrom,
						VolumeMounts: []corev1.VolumeMount{{Name: "oarlock", MountPath: "/oarlock"}},
						Resources:    resources(spec),
					}},
				},
			},
		},
	}
	created, err := k.cs.BatchV1().Jobs(k.cfg.Namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		_ = k.cs.CoreV1().Secrets(k.cfg.Namespace).Delete(context.WithoutCancel(ctx), secretName, metav1.DeleteOptions{})
		return nil, fmt.Errorf("create job: %w", err)
	}
	// Own the secret by the job so a stray job deletion reaps it too.
	k.adoptSecret(ctx, secretName, created)

	return steps.Handle{
		"kind":         "k8s",
		"namespace":    k.cfg.Namespace,
		"job":          jobName,
		"secret":       secretName,
		"result_key":   resultKey,
		"workspace_id": spec.WorkspaceID.String(),
		"run_id":       spec.RunID.String(),
		"task_id":      spec.TaskID.String(),
		"deadline":     float64(time.Now().Add(time.Duration(spec.TimeoutSec) * time.Second).Unix()),
	}, nil
}

func (k *K8s) Poll(ctx context.Context, h steps.Handle) (steps.ContainerStatus, error) {
	job, err := k.cs.BatchV1().Jobs(k.ns(h)).Get(ctx, handleStr(h, "job"), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return steps.ContainerStatus{Phase: steps.PhaseFailed, Message: "job not found"}, nil
	}
	if err != nil {
		return steps.ContainerStatus{}, err
	}
	if job.Status.Succeeded > 0 {
		code := 0
		return steps.ContainerStatus{Phase: steps.PhaseSucceeded, ExitCode: &code}, nil
	}
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			return steps.ContainerStatus{Phase: steps.PhaseFailed, Message: c.Reason + ": " + c.Message}, nil
		}
	}
	if job.Status.Failed > 0 {
		return steps.ContainerStatus{Phase: steps.PhaseFailed, Message: "pod failed"}, nil
	}
	return steps.ContainerStatus{Phase: steps.PhaseRunning}, nil
}

func (k *K8s) Result(ctx context.Context, h steps.Handle) (steps.ContainerResult, error) {
	var res steps.ContainerResult
	rc, err := k.store.Download(ctx, handleStr(h, "result_key"))
	if err != nil {
		// Runner never wrote a result (crashed early). Fall back to pod logs.
		logs := k.podLogs(ctx, h)
		return steps.ContainerResult{ExitCode: 1, StderrTail: []byte(logs)}, nil
	}
	defer rc.Close()
	var rr runnerResult
	if err := json.NewDecoder(rc).Decode(&rr); err != nil {
		return res, fmt.Errorf("parse result manifest: %w", err)
	}
	ws, _ := uuidFrom(handleStr(h, "workspace_id"))
	run, _ := uuidFrom(handleStr(h, "run_id"))
	task, _ := uuidFrom(handleStr(h, "task_id"))
	var outs []steps.ArtifactRef
	for _, o := range rr.Outputs {
		ref, err := k.store.Record(ctx, steps.ArtifactRecord{
			WorkspaceID: ws, RunID: &run, TaskID: &task,
			Key: o.Key, Name: o.Name, Size: o.Size, ContentType: o.ContentType, Source: "output",
		})
		if err != nil {
			k.log.Warn("k8s: record output failed", "key", o.Key, "error", err)
			continue
		}
		outs = append(outs, ref)
	}
	started, _ := time.Parse(time.RFC3339Nano, rr.StartedAt)
	finished, _ := time.Parse(time.RFC3339Nano, rr.FinishedAt)
	return steps.ContainerResult{
		ExitCode:   rr.ExitCode,
		Stdout:     []byte(rr.Stdout),
		StderrTail: []byte(rr.StderrTail),
		Outputs:    outs,
		StartedAt:  started,
		FinishedAt: finished,
	}, nil
}

func (k *K8s) Logs(ctx context.Context, h steps.Handle, since time.Time) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(k.podLogs(ctx, h))), nil
}

func (k *K8s) Cancel(ctx context.Context, h steps.Handle) error {
	return k.deleteJobAndSecrets(ctx, h)
}

func (k *K8s) Cleanup(ctx context.Context, h steps.Handle) error {
	return k.deleteJobAndSecrets(ctx, h)
}

// --- helpers ---

type runnerResult struct {
	ExitCode   int    `json:"exit_code"`
	Stdout     string `json:"stdout"`
	StderrTail string `json:"stderr_tail"`
	Outputs    []struct {
		Name        string `json:"name"`
		Key         string `json:"key"`
		Size        int64  `json:"size"`
		ContentType string `json:"content_type"`
	} `json:"outputs"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
}

func (k *K8s) ns(h steps.Handle) string {
	if n := handleStr(h, "namespace"); n != "" {
		return n
	}
	return k.cfg.Namespace
}

func (k *K8s) deleteJobAndSecrets(ctx context.Context, h steps.Handle) error {
	ns := k.ns(h)
	prop := metav1.DeletePropagationBackground
	job := handleStr(h, "job")
	if err := k.cs.BatchV1().Jobs(ns).Delete(ctx, job, metav1.DeleteOptions{PropagationPolicy: &prop}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	_ = k.cs.CoreV1().Secrets(ns).Delete(ctx, handleStr(h, "secret"), metav1.DeleteOptions{})
	_ = k.cs.CoreV1().Secrets(ns).Delete(ctx, job+"-pull", metav1.DeleteOptions{})
	return nil
}

func (k *K8s) podLogs(ctx context.Context, h steps.Handle) string {
	ns := k.ns(h)
	pods, err := k.cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: "job-name=" + handleStr(h, "job")})
	if err != nil || len(pods.Items) == 0 {
		return ""
	}
	req := k.cs.CoreV1().Pods(ns).GetLogs(pods.Items[0].Name, &corev1.PodLogOptions{Container: "main"})
	stream, err := req.Stream(ctx)
	if err != nil {
		return ""
	}
	defer stream.Close()
	b, _ := io.ReadAll(io.LimitReader(stream, maxStderrCapture))
	return string(b)
}

func (k *K8s) createPullSecret(ctx context.Context, name string, spec steps.ContainerSpec) error {
	reg := registryHost(spec.Image)
	auth := base64.StdEncoding.EncodeToString([]byte(spec.Registry.Username + ":" + spec.Registry.Password))
	dockercfg := map[string]any{"auths": map[string]any{reg: map[string]string{
		"username": spec.Registry.Username, "password": spec.Registry.Password, "auth": auth,
	}}}
	raw, _ := json.Marshal(dockercfg)
	_, err := k.cs.CoreV1().Secrets(k.cfg.Namespace).Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels(spec)},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: raw},
	}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create pull secret: %w", err)
	}
	return nil
}

func (k *K8s) adoptSecret(ctx context.Context, secretName string, job *batchv1.Job) {
	sec, err := k.cs.CoreV1().Secrets(k.cfg.Namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return
	}
	t := true
	sec.OwnerReferences = []metav1.OwnerReference{{
		APIVersion: "batch/v1", Kind: "Job", Name: job.Name, UID: job.UID,
		Controller: &t, BlockOwnerDeletion: &t,
	}}
	_, _ = k.cs.CoreV1().Secrets(k.cfg.Namespace).Update(ctx, sec, metav1.UpdateOptions{})
}

func labels(spec steps.ContainerSpec) map[string]string {
	return map[string]string{
		"app":               "oarlock",
		"oarlock/workspace": spec.WorkspaceID.String(),
		"oarlock/run":       spec.RunID.String(),
		"oarlock/task":      spec.TaskID.String(),
	}
}

func envVars(m map[string]string) []corev1.EnvVar {
	out := make([]corev1.EnvVar, 0, len(m))
	for k, v := range m {
		out = append(out, corev1.EnvVar{Name: k, Value: v})
	}
	return out
}

func resources(spec steps.ContainerSpec) corev1.ResourceRequirements {
	lim := corev1.ResourceList{}
	if q, err := resource.ParseQuantity(strings.TrimSpace(spec.CPU)); err == nil && !q.IsZero() {
		lim[corev1.ResourceCPU] = q
	}
	if spec.MemoryMB > 0 {
		lim[corev1.ResourceMemory] = resource.MustParse(fmt.Sprintf("%dMi", spec.MemoryMB))
	}
	if len(lim) == 0 {
		return corev1.ResourceRequirements{}
	}
	return corev1.ResourceRequirements{Limits: lim}
}

func runtimeClass(rc string) *string {
	if strings.TrimSpace(rc) == "" {
		return nil
	}
	return &rc
}

func registryHost(image string) string {
	first := strings.SplitN(image, "/", 2)[0]
	if strings.ContainsAny(first, ".:") {
		return first
	}
	return "https://index.docker.io/v1/"
}

func jsonStr(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func int32Ptr(i int32) *int32 { return &i }
func int64PtrIf(sec int) *int64 {
	if sec <= 0 {
		return nil
	}
	v := int64(sec)
	return &v
}
