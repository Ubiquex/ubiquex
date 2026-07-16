package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// terraformConfigForOnboarding is a small, disposable Terraform config --
// two cheap/free real AWS resource types (matching
// conformance/aws_live_test.go's own choices for the same reason: no
// per-resource charge). Terraform is used ONLY as a test-fixture
// generator in this test: something to `apply`, so a real state file
// exists to onboard from, and `destroy`, so nothing outlives the test. It
// is never a runtime dependency of ubx itself (docs/architecture.md's
// Bulk onboarding section: ".tfstate is a border-crossing artifact, read
// once").
const terraformConfigForOnboarding = `
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
  }
}

provider "aws" {
  region = "us-east-1"
}

variable "queue_name" { type = string }
variable "topic_name" { type = string }

resource "aws_sqs_queue" "onboard_demo" {
  name = var.queue_name
}

resource "aws_sns_topic" "onboard_demo" {
  name = var.topic_name
}
`

func runTerraform(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("terraform", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("terraform %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// TestScanAll_LiveEndToEnd is UBI-18's real-world verification, run as an
// actual repeatable test (same discipline as every other real-AWS-touching
// test in this codebase): builds a small, disposable Terraform config,
// applies it for real, onboards from its real state file end to end
// (scan --all, accept every generated proposal, confirm ubx status
// --drift reports clean), then destroys everything. Gated behind
// UBX_CONFORMANCE_LIVE=1. Leaves the account as found: both resources are
// created by this test and destroyed by it, nothing pre-existing is
// touched.
func TestScanAll_LiveEndToEnd(t *testing.T) {
	requireAttributionLive(t)
	providerPath := realAWSProviderPathForAttribution(t)

	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skip("skipping: terraform binary not found on PATH (used only as a test-fixture generator here)")
	}

	tfDir := t.TempDir()
	writeFile(t, filepath.Join(tfDir, "main.tf"), terraformConfigForOnboarding)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	queueName := "ubx-onboard-demo-" + suffix
	topicName := "ubx-onboard-demo-" + suffix

	runTerraform(t, tfDir, "init", "-input=false")
	t.Cleanup(func() {
		runTerraform(t, tfDir, "destroy", "-auto-approve",
			"-var", "queue_name="+queueName, "-var", "topic_name="+topicName)
	})
	runTerraform(t, tfDir, "apply", "-auto-approve",
		"-var", "queue_name="+queueName, "-var", "topic_name="+topicName)

	statePath := filepath.Join(tfDir, "terraform.tfstate")
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("expected terraform.tfstate to exist after apply: %v", err)
	}

	ledgerDir := t.TempDir()
	outDir := filepath.Join(t.TempDir(), "proposals")

	onboardOut, err := runUbx(t, nil, "scan", "--all",
		"--tfstate", statePath,
		"--stack", "onboard-live",
		"--provider", providerPath,
		"--provider-config", `{"region":"us-east-1"}`,
		"--ledger-dir", ledgerDir,
		"--out-dir", outDir,
	)
	if err != nil {
		t.Fatalf("ubx scan --all: %v\noutput: %s", err, onboardOut)
	}
	if !strings.Contains(onboardOut, "2 adopted, 0 skipped") {
		t.Fatalf("expected both real resources adopted cleanly, got: %s", onboardOut)
	}

	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("read out-dir: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d proposal files, want 2", len(entries))
	}
	for _, e := range entries {
		if _, err := runUbx(t, nil, "accept", filepath.Join(outDir, e.Name()), "--ledger-dir", ledgerDir); err != nil {
			t.Fatalf("ubx accept %s: %v", e.Name(), err)
		}
	}

	statusOut, err := runUbx(t, nil, "status",
		"--ledger-dir", ledgerDir, "--drift",
		"--provider", providerPath, "--provider-config", `{"region":"us-east-1"}`,
	)
	if err != nil {
		t.Fatalf("ubx status --drift (expected clean): %v\noutput: %s", err, statusOut)
	}
	if !strings.Contains(statusOut, "2 resource(s), 0 drifted, 0 unreadable") {
		t.Fatalf("expected both onboarded resources reported clean, got: %s", statusOut)
	}
}
