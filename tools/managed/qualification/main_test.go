//go:build linux

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 OpenCodeReview fork contributors

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestQualificationEndToEnd(t *testing.T) {
	rootBytes, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatal(err)
	}
	root := strings.TrimSpace(string(rootBytes))
	t.Chdir(root)
	headBytes, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(string(headBytes))
	out := filepath.Join(t.TempDir(), "managed")
	buildEnv := append(os.Environ(), "GOMAXPROCS=1", "GOGC=20")
	output, code, err := runProcess("/bin/bash", []string{filepath.Join(root, "tools/managed/build.sh"), out}, root, buildEnv, 8*time.Minute)
	if err != nil || code != 0 {
		t.Fatalf("exact managed build failed (requires a clean committed checkout): exit=%d error=%v\n%s", code, err, output)
	}
	binary := filepath.Join(out, "opencodereview-linux-amd64")
	manifestPath := filepath.Join(out, "MANIFEST")
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}

	for _, field := range []string{"source_commit", "binary_sha256"} {
		t.Run("reject_"+field, func(t *testing.T) {
			lines := strings.Split(string(manifestBytes), "\n")
			found := false
			for i, line := range lines {
				if strings.HasPrefix(line, field+"=") {
					lines[i] = field + "=" + strings.Repeat("0", len(strings.TrimPrefix(line, field+"=")))
					found = true
				}
			}
			if !found {
				t.Fatalf("builder omitted %s", field)
			}
			if err := os.WriteFile(manifestPath, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := os.WriteFile(manifestPath, manifestBytes, 0o600); err != nil {
					t.Error(err)
				}
			}()
			rejectedReceipt := filepath.Join(out, "rejected-"+field+".json")
			if err := qualify([]string{binary, rejectedReceipt}); err == nil || !strings.Contains(err.Error(), "manifest does not match") {
				t.Fatalf("mismatched build identity was not rejected: %v", err)
			}
			if _, err := os.Stat(rejectedReceipt); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("rejected artifact produced a receipt: %v", err)
			}
		})
	}

	receiptPath := filepath.Join(out, "qualification.json")
	if err := qualify([]string{binary, receiptPath}); err != nil {
		data, _ := os.ReadFile(receiptPath)
		t.Fatalf("real CLI qualification failed: %v\n%s", err, data)
	}
	data, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	var observed receipt
	if err := json.Unmarshal(data, &observed); err != nil {
		t.Fatal(err)
	}
	binaryBytes, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	binarySum, manifestSum := sha256.Sum256(binaryBytes), sha256.Sum256(manifestBytes)
	if !observed.Passed || !observed.ModelFree || observed.SourceCommit != head ||
		observed.BinarySHA256 != hex.EncodeToString(binarySum[:]) ||
		observed.ManifestSHA256 != hex.EncodeToString(manifestSum[:]) ||
		observed.SchemaVersion != "opencodereview.managed-qualification/v1" ||
		observed.OutcomeSchemaVersion != "reviewrouter.ocr-outcome/v1" || len(observed.Cases) != 4 {
		t.Fatalf("receipt is not bound to the built source and binary: %s", data)
	}
	want := []caseReceipt{
		{Name: "openai-responses/finding", Passed: true, Requests: 5, ExitCode: 0, Findings: 1, TerminalState: "complete"},
		{Name: "openai-responses/missing-terminal", Passed: true, Requests: 1, ExitCode: 1, Findings: 0, TerminalState: "failed"},
		{Name: "anthropic/finding", Passed: true, Requests: 5, ExitCode: 0, Findings: 1, TerminalState: "complete"},
		{Name: "anthropic/missing-terminal", Passed: true, Requests: 1, ExitCode: 1, Findings: 0, TerminalState: "failed"},
	}
	for i, expected := range want {
		if observed.Cases[i] != expected {
			t.Errorf("case %d: got %+v, want %+v", i, observed.Cases[i], expected)
		}
	}
	if err := qualify([]string{binary, receiptPath}); err == nil {
		t.Fatal("existing qualification receipt was overwritten")
	}
	after, err := os.ReadFile(receiptPath)
	if err != nil || !bytes.Equal(data, after) {
		t.Fatal("existing qualification receipt changed")
	}
}

func TestManifestRejectsAmbiguousIdentity(t *testing.T) {
	for _, data := range []string{"source_commit=one\nsource_commit=two\n", "source_commit\n", "=value\n"} {
		if _, err := parseManifest([]byte(data)); err == nil {
			t.Fatalf("accepted ambiguous manifest: %q", data)
		}
	}
	fields, err := parseManifest([]byte("# comment\nsource_commit=one\nbuild_flags=-X=main.Version=1.2.3\n"))
	if err != nil || fields["build_flags"] != "-X=main.Version=1.2.3" {
		t.Fatalf("valid manifest rejected: %v", err)
	}
}

func TestFileBoundsRejectSymlinksAndOversizedOutputs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result.json")
	if err := os.WriteFile(path, []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "redirected.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{path, link, dir} {
		if _, err := regularFile(input, 4); err == nil {
			t.Errorf("accepted oversized or non-regular input %s", input)
		}
	}
	data, err := regularFile(path, 5)
	if err != nil || string(data) != "12345" {
		t.Fatalf("exact file bound rejected: %q, %v", data, err)
	}
}

func TestReceiptCannotReplaceExistingEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipt.json")
	if err := writeReceipt(path, receipt{Passed: false}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeReceipt(path, receipt{Passed: true}); !errors.Is(err, os.ErrExist) {
		t.Fatalf("replacement not rejected: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("existing evidence changed")
	}
}

func TestProcessDeadlineAndDiagnosticBound(t *testing.T) {
	output, code, err := runProcess("/bin/sh", []string{"-c", "printf started; sleep 10"}, t.TempDir(), childEnvironment(t.TempDir(), ""), 500*time.Millisecond)
	if err == nil || code != -1 || string(output) != "started" {
		t.Fatalf("deadline not enforced: %q %d %v", output, code, err)
	}
	buffer := boundedOutput{limit: 10}
	data := bytes.Repeat([]byte("x"), 1000)
	for i := 0; i < 10; i++ {
		n, err := buffer.Write(data)
		if n != len(data) || err != nil {
			t.Fatalf("bounded drain failed: %d %v", n, err)
		}
	}
	if buffer.Len() != 10 {
		t.Fatalf("diagnostic bound exceeded: %d", buffer.Len())
	}
}

func TestFakeProviderRejectsWrongRouteAuthenticationAndBudget(t *testing.T) {
	for _, mutation := range []string{"route", "authorization", "budget", "model"} {
		t.Run(mutation, func(t *testing.T) {
			fake := &fakeProvider{protocol: "openai-responses", model: "stub-model", token: "fixture-token"}
			if mutation == "budget" {
				fake.requests = requestLimit
			}
			server := httptest.NewServer(fake)
			defer server.Close()
			path, auth, model := "/responses", "Bearer fixture-token", "stub-model"
			switch mutation {
			case "route":
				path = "/v1/responses"
			case "authorization":
				auth = "Bearer Bearer fixture-token"
			case "model":
				model = "unexpected-model"
			}
			request, err := http.NewRequest(http.MethodPost, server.URL+path, strings.NewReader(`{"model":"`+model+`","reasoning":{"effort":"medium"}}`))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", auth)
			response, err := server.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if _, err := io.Copy(io.Discard, response.Body); err != nil {
				t.Fatal(err)
			}
			fake.mu.Lock()
			defer fake.mu.Unlock()
			if response.StatusCode != http.StatusBadRequest || fake.problem == nil {
				t.Fatalf("invalid request was accepted: %d %v", response.StatusCode, fake.problem)
			}
		})
	}
}
