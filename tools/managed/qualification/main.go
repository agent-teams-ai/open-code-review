//go:build linux

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 OpenCodeReview fork contributors

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	outcomeSchema = "reviewrouter.ocr-outcome/v1"
	requestLimit  = 12
	bodyLimit     = 1024 * 1024
	outputLimit   = 16 * 1024 * 1024
	processLimit  = 45 * time.Second
	fixtureText   = "add() subtracts b, so add(2, 3) returns -1 instead of 5. Restore addition."
)

type caseReceipt struct {
	Name          string `json:"name"`
	Passed        bool   `json:"passed"`
	Requests      int    `json:"http_requests"`
	ExitCode      int    `json:"exit_code"`
	Findings      int    `json:"findings"`
	TerminalState string `json:"terminal_state"`
	Error         string `json:"error,omitempty"`
}

type receipt struct {
	SchemaVersion        string        `json:"schema_version"`
	SourceCommit         string        `json:"source_commit"`
	SourceTree           string        `json:"source_tree"`
	ReleaseVersion       string        `json:"release_version"`
	VersionOutput        string        `json:"version_output"`
	BinarySHA256         string        `json:"binary_sha256"`
	ManifestSHA256       string        `json:"manifest_sha256"`
	OutcomeSchemaVersion string        `json:"outcome_schema_version"`
	ModelFree            bool          `json:"model_free"`
	NetworkBoundary      string        `json:"network_boundary"`
	CaseDeadlineSeconds  int           `json:"case_deadline_seconds"`
	RequestLimitPerCase  int           `json:"request_limit_per_case"`
	Cases                []caseReceipt `json:"qualification_cases"`
	Passed               bool          `json:"passed"`
}

type resultDocument struct {
	Comments []struct {
		Path     string `json:"path"`
		Content  string `json:"content"`
		Severity string `json:"severity"`
	} `json:"comments"`
	Summary struct {
		InputTokens int `json:"input_tokens"`
	} `json:"summary"`
	Manifest struct {
		TerminalState string `json:"terminal_state"`
		Input         struct {
			Base string `json:"resolved_base"`
			Head string `json:"resolved_head"`
		} `json:"input"`
		Coverage struct {
			Selected []struct{ Path string } `json:"selected"`
		} `json:"coverage"`
	} `json:"manifest"`
	Outcome struct {
		Schema string `json:"schema_version"`
		Groups []struct {
			Paths         []string `json:"paths"`
			MainCompleted bool     `json:"main_completed"`
			StopCause     *string  `json:"stop_cause"`
			FilterValid   *bool    `json:"filter_decision_valid"`
			Phases        struct {
				Filter struct {
					State string `json:"state"`
				} `json:"filter"`
			} `json:"phases"`
		} `json:"groups"`
	} `json:"reviewrouter_outcome"`
}

type fakeProvider struct {
	mu        sync.Mutex
	protocol  string
	model     string
	token     string
	malformed bool
	requests  int
	mainTurns int
	problem   error
}

func main() {
	if err := qualify(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "managed qualification:", err)
		os.Exit(1)
	}
}

func qualify(args []string) error {
	if len(args) != 2 || !filepath.IsAbs(args[0]) {
		return errors.New("expected absolute binary path and receipt path")
	}
	binary, receiptPath := args[0], args[1]
	if _, err := os.Lstat(receiptPath); !errors.Is(err, os.ErrNotExist) {
		return errors.New("receipt destination must not already exist")
	}
	manifestBytes, err := regularFile(filepath.Join(filepath.Dir(binary), "MANIFEST"), 64*1024)
	if err != nil {
		return err
	}
	manifest, err := parseManifest(manifestBytes)
	if err != nil {
		return err
	}
	hash, err := fileHash(binary)
	if err != nil {
		return err
	}
	head, err := gitRead("rev-parse", "HEAD")
	if err != nil {
		return err
	}
	tree, err := gitRead("rev-parse", "HEAD^{tree}")
	if err != nil {
		return err
	}
	status, err := gitRead("status", "--porcelain", "--untracked-files=normal")
	if err != nil || status != "" {
		return errors.New("qualification requires a clean committed checkout")
	}
	if manifest["manifest_schema"] != "open-code-review.build/v1" ||
		manifest["source_commit"] != head || manifest["source_tree"] != tree ||
		manifest["binary_sha256"] != hash || manifest["binary_name"] != filepath.Base(binary) ||
		manifest["outcome_schema_version"] != outcomeSchema ||
		manifest["repository"] != "agent-teams-ai/open-code-review" || manifest["cgo_enabled"] != "0" || manifest["goamd64"] != "v1" ||
		manifest["toolchain"] != "go1.26.10" || manifest["goos"] != "linux" || manifest["goarch"] != "amd64" {
		return errors.New("manifest does not match this binary, source checkout or qualified profile")
	}
	info, err := os.Stat(binary)
	if err != nil || manifest["binary_size_bytes"] != strconv.FormatInt(info.Size(), 10) {
		return errors.New("binary size differs from manifest")
	}
	version := manifest["release_version"]
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+-rr\.[0-9]+$`).MatchString(version) {
		return errors.New("invalid managed release version")
	}
	workspace, err := os.MkdirTemp("", "ocr-qualification-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workspace)
	env := childEnvironment(workspace, "")
	versionOutput, code, err := runProcess(binary, []string{"--version"}, workspace, env, 10*time.Second)
	versionFields := strings.Fields(string(versionOutput))
	if err != nil || code != 0 || len(versionFields) < 2 || versionFields[0] != "open-code-review" || versionFields[1] != version {
		return fmt.Errorf("binary version does not match manifest: exit=%d error=%v", code, err)
	}
	manifestSum := sha256.Sum256(manifestBytes)
	report := receipt{
		SchemaVersion: "opencodereview.managed-qualification/v1", SourceCommit: head, SourceTree: tree,
		ReleaseVersion: version, VersionOutput: strings.TrimSpace(string(versionOutput)), BinarySHA256: hash,
		ManifestSHA256: hex.EncodeToString(manifestSum[:]), OutcomeSchemaVersion: outcomeSchema, ModelFree: true,
		NetworkBoundary:     "loopback fake endpoints and credential-free child environment; no OS network isolation",
		CaseDeadlineSeconds: int(processLimit.Seconds()), RequestLimitPerCase: requestLimit,
		Cases: make([]caseReceipt, 0, 4), Passed: true,
	}
	for _, protocol := range []string{"openai-responses", "anthropic"} {
		for _, malformed := range []bool{false, true} {
			observation := runCase(binary, protocol, malformed)
			report.Cases = append(report.Cases, observation)
			report.Passed = report.Passed && observation.Passed
		}
	}
	finalHash, err := fileHash(binary)
	if err != nil || finalHash != hash {
		return errors.New("binary changed during qualification")
	}
	finalHead, err := gitRead("rev-parse", "HEAD")
	if err != nil || finalHead != head {
		return errors.New("source commit changed during qualification")
	}
	finalStatus, err := gitRead("status", "--porcelain", "--untracked-files=normal")
	if err != nil || finalStatus != "" {
		return errors.New("checkout changed during qualification")
	}
	finalManifest, err := regularFile(filepath.Join(filepath.Dir(binary), "MANIFEST"), 64*1024)
	if err != nil || !bytes.Equal(finalManifest, manifestBytes) {
		return errors.New("manifest changed during qualification")
	}
	if err := writeReceipt(receiptPath, report); err != nil {
		return err
	}
	if !report.Passed {
		return errors.New("one or more cases failed; inspect the receipt")
	}
	fmt.Printf("Passed %d model-free CLI cases; receipt: %s\n", len(report.Cases), receiptPath)
	return nil
}

func runCase(binary, protocol string, malformed bool) (observation caseReceipt) {
	observation.Name = protocol + "/finding"
	if malformed {
		observation.Name = protocol + "/missing-terminal"
	}
	observation.ExitCode = -1
	fail := func(err error) caseReceipt { observation.Error = err.Error(); return observation }
	workspace, err := os.MkdirTemp("", "ocr-case-")
	if err != nil {
		return fail(err)
	}
	defer os.RemoveAll(workspace)
	home, repo := filepath.Join(workspace, "home"), filepath.Join(workspace, "repo")
	for _, dir := range []string{filepath.Join(home, ".opencodereview"), repo} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fail(err)
		}
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return fail(err)
	}
	token := hex.EncodeToString(tokenBytes)
	env := childEnvironment(home, token)
	base, head, err := makeFixture(repo, env)
	if err != nil {
		return fail(err)
	}
	model := "stub-model"
	if protocol == "anthropic" {
		model = "claude-sonnet-4-6"
	}
	fake := &fakeProvider{protocol: protocol, model: model, token: token, malformed: malformed}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return fail(err)
	}
	server := &http.Server{Handler: fake, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()
	extra := map[string]any{"service_tier": "default", "reasoning": map[string]string{"effort": "medium"}}
	if protocol == "anthropic" {
		extra = map[string]any{"output_config": map[string]string{"effort": "high"}}
	}
	config := map[string]any{
		"provider": "reviewrouter", "language": "en", "telemetry": map[string]bool{"enabled": false},
		"custom_providers": map[string]any{"reviewrouter": map[string]any{
			"protocol": protocol, "url": "http://" + listener.Addr().String(), "api_key": token,
			"model": model, "timeout_sec": 10, "extra_body": extra,
		}},
	}
	if err := writeJSON(filepath.Join(home, ".opencodereview", "config.json"), config); err != nil {
		return fail(err)
	}
	rulePath := filepath.Join(workspace, "rule.json")
	rule := map[string]any{"merge_system_rule": true, "include": []string{"calc.ts"}, "rules": []map[string]string{{"id": "reviewrouter-managed-v1", "content": "Report only issues introduced by the base-to-head diff in the assigned changed paths. Write findings in English."}}}
	if err := writeJSON(rulePath, rule); err != nil {
		return fail(err)
	}
	resultPath := filepath.Join(workspace, "result.json")
	args := []string{"review", "--reviewrouter-managed", "--repo", repo, "--from", base, "--to", head,
		"--provider", "reviewrouter", "--model", model, "--rule", rulePath, "--effort", "medium",
		"--concurrency", "1", "--format", "json", "--audience", "agent", "--timeout", "1",
		"--max-tokens", "32768", "--output", resultPath}
	_, observation.ExitCode, err = runProcess(binary, args, workspace, env, processLimit)
	_ = server.Close()
	fake.mu.Lock()
	observation.Requests = fake.requests
	providerErr := fake.problem
	fake.mu.Unlock()
	if err != nil {
		return fail(err)
	}
	if providerErr != nil {
		return fail(providerErr)
	}
	if observation.Requests < 1 || observation.Requests > requestLimit {
		return fail(errors.New("HTTP request count outside case budget"))
	}
	data, err := regularFile(resultPath, outputLimit)
	if err != nil {
		return fail(fmt.Errorf("missing bounded result JSON: %w", err))
	}
	var result resultDocument
	if err := json.Unmarshal(data, &result); err != nil {
		return fail(err)
	}
	observation.Findings = len(result.Comments)
	observation.TerminalState = result.Manifest.TerminalState
	if result.Outcome.Schema != outcomeSchema || result.Manifest.Input.Base != base || result.Manifest.Input.Head != head ||
		len(result.Outcome.Groups) != 1 || len(result.Outcome.Groups[0].Paths) != 1 || result.Outcome.Groups[0].Paths[0] != "calc.ts" ||
		len(result.Manifest.Coverage.Selected) != 1 || result.Manifest.Coverage.Selected[0].Path != "calc.ts" {
		return fail(errors.New("result schema, input identity or selected paths mismatch"))
	}
	group := result.Outcome.Groups[0]
	if malformed {
		if observation.Requests != 1 || observation.Findings != 0 || group.MainCompleted ||
			group.StopCause == nil || *group.StopCause != "provider_error" || result.Manifest.TerminalState != "failed" || observation.ExitCode == 0 {
			return fail(errors.New("malformed terminal was accepted or replayed"))
		}
	} else {
		if observation.ExitCode != 0 || observation.Requests < 3 || len(result.Comments) != 1 ||
			result.Comments[0].Path != "calc.ts" || result.Comments[0].Severity != "high" ||
			result.Comments[0].Content != fixtureText || !group.MainCompleted || group.StopCause != nil ||
			group.FilterValid == nil || !*group.FilterValid || group.Phases.Filter.State != "completed" ||
			result.Summary.InputTokens <= 0 || result.Manifest.TerminalState != "complete" {
			return fail(errors.New("finding, filter, usage or completion contract mismatch"))
		}
	}
	observation.Passed = true
	return observation
}

func (f *fakeProvider) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	fail := func(message string) {
		if f.problem == nil {
			f.problem = errors.New(message)
		}
		http.Error(w, "fixture rejected request", http.StatusBadRequest)
	}
	expectedPath := "/responses"
	if f.protocol == "anthropic" {
		expectedPath = "/v1/messages"
	}
	if f.requests > requestLimit || request.Method != http.MethodPost || request.URL.RequestURI() != expectedPath ||
		request.Header.Get("Authorization") != "Bearer "+f.token || request.Header.Get("X-Api-Key") != "" {
		fail("fake-provider request budget, route or authentication mismatch")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, request.Body, bodyLimit))
	if err != nil {
		fail("fake-provider request body exceeded bounds")
		return
	}
	var input struct {
		Model        string                  `json:"model"`
		Stream       bool                    `json:"stream"`
		Tools        []struct{ Name string } `json:"tools"`
		Reasoning    struct{ Effort string } `json:"reasoning"`
		OutputConfig struct{ Effort string } `json:"output_config"`
	}
	if json.Unmarshal(body, &input) != nil || input.Model != f.model || (f.protocol == "anthropic" && input.Stream) {
		fail("fake-provider model or stream mismatch")
		return
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		fail("fake-provider invalid JSON")
		return
	}
	for _, name := range []string{"temperature", "top_p"} {
		if _, exists := raw[name]; exists {
			fail("unexpected sampling parameter")
			return
		}
	}
	if (f.protocol == "anthropic" && input.OutputConfig.Effort != "high") ||
		(f.protocol == "openai-responses" && input.Reasoning.Effort != "medium") {
		fail("configured reasoning effort was not preserved")
		return
	}
	tool, args := "", map[string]any{}
	for _, offered := range input.Tools {
		if offered.Name == "task_done" {
			f.mainTurns++
			tool = "task_done"
			if f.mainTurns == 1 {
				tool = "code_comment"
				args["comments"] = []map[string]string{{"path": "calc.ts", "content": fixtureText, "severity": "high"}}
			}
			break
		}
		if offered.Name == "approve_all_comments" {
			tool = "approve_all_comments"
		}
	}
	if f.protocol == "anthropic" {
		if _, exists := raw["input"]; exists {
			fail("Responses fields sent to Messages route")
			return
		}
		content := []map[string]any{{"type": "text", "text": "The affected expression is on line 1 of calc.ts."}}
		stop := "end_turn"
		if tool != "" {
			content = []map[string]any{{"type": "tool_use", "id": fmt.Sprintf("tool_%d", f.requests), "name": tool, "input": args}}
			stop = "tool_use"
		}
		response := map[string]any{"id": fmt.Sprintf("msg_%d", f.requests), "type": "message", "role": "assistant", "model": f.model,
			"content": content, "usage": map[string]int{"input_tokens": 10, "output_tokens": 5, "cache_read_input_tokens": 2}}
		if !f.malformed {
			response["stop_reason"] = stop
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	if f.malformed {
		_, _ = io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":")
		return
	}
	output := []map[string]any{{"type": "message", "id": fmt.Sprintf("msg_%d", f.requests), "status": "completed", "role": "assistant",
		"content": []map[string]any{{"type": "output_text", "text": "The affected expression is on line 1 of calc.ts.", "annotations": []string{}}}}}
	if tool != "" {
		arguments, _ := json.Marshal(args)
		output = []map[string]any{{"type": "function_call", "id": fmt.Sprintf("fc_%d", f.requests), "call_id": fmt.Sprintf("call_%d", f.requests), "name": tool, "arguments": string(arguments)}}
	}
	response := map[string]any{"type": "response.completed", "response": map[string]any{
		"id": fmt.Sprintf("resp_%d", f.requests), "object": "response", "status": "completed", "model": f.model,
		"output": output, "usage": map[string]int{"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}}}
	data, _ := json.Marshal(response)
	_, _ = fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", data)
}

func childEnvironment(home, token string) []string {
	return []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
		"XDG_CACHE_HOME=" + filepath.Join(home, ".cache"), "LC_ALL=C", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_TERMINAL_PROMPT=0", "REVIEWROUTER_LOCAL_MODEL_TOKEN=Bearer " + token}
}

type boundedOutput struct {
	bytes.Buffer
	limit int
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if b.Len() < b.limit {
		remaining := b.limit - b.Len()
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = b.Buffer.Write(p)
	}
	return n, nil
}

func runProcess(program string, args []string, cwd string, env []string, timeout time.Duration) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, program, args...)
	command.Dir, command.Env = cwd, env
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	command.WaitDelay = 2 * time.Second
	output := &boundedOutput{limit: 64 * 1024}
	command.Stdout, command.Stderr = output, output
	err := command.Run()
	if ctx.Err() != nil {
		return output.Bytes(), -1, errors.New("process deadline exceeded")
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return output.Bytes(), exitError.ExitCode(), nil
	}
	if err != nil {
		return output.Bytes(), -1, err
	}
	return output.Bytes(), 0, nil
}

func makeFixture(repo string, env []string) (string, string, error) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return "", "", err
	}
	git := func(args ...string) (string, error) {
		flags := []string{"-c", "core.hooksPath=/dev/null", "-c", "user.name=OCR qualification fixture", "-c", "user.email=fixture@test.invalid", "-c", "commit.gpgsign=false"}
		data, code, err := runProcess(gitPath, append(flags, args...), repo, env, 10*time.Second)
		if err != nil || code != 0 {
			return "", fmt.Errorf("fixture git command failed: exit=%d error=%v", code, err)
		}
		return strings.TrimSpace(string(data)), nil
	}
	if _, err := git("init", "-q"); err != nil {
		return "", "", err
	}
	file := filepath.Join(repo, "calc.ts")
	if err := os.WriteFile(file, []byte("export function add(a: number, b: number) { return a + b; }\n"), 0o600); err != nil {
		return "", "", err
	}
	for _, args := range [][]string{{"add", "calc.ts"}, {"commit", "-qm", "base"}} {
		if _, err := git(args...); err != nil {
			return "", "", err
		}
	}
	base, err := git("rev-parse", "HEAD")
	if err != nil {
		return "", "", err
	}
	if err := os.WriteFile(file, []byte("export function add(a: number, b: number) { return a - b; }\n"), 0o600); err != nil {
		return "", "", err
	}
	if _, err := git("commit", "-qam", "head"); err != nil {
		return "", "", err
	}
	head, err := git("rev-parse", "HEAD")
	return base, head, err
}

func gitRead(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "git", args...).Output()
	return strings.TrimSpace(string(output)), err
}

func parseManifest(data []byte) (map[string]string, error) {
	manifest := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if _, duplicate := manifest[key]; !found || key == "" || duplicate {
			return nil, errors.New("malformed or duplicate build manifest field")
		}
		manifest[key] = value
	}
	return manifest, nil
}

func regularFile(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("input must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if int64(len(data)) > limit {
		return nil, errors.New("input exceeded size bound while reading")
	}
	return data, err
}

func fileHash(path string) (string, error) {
	data, err := regularFile(path, 128*1024*1024)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

func writeReceipt(path string, value receipt) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(append(data, '\n'))
	closeErr := file.Close()
	return errors.Join(writeErr, closeErr)
}
