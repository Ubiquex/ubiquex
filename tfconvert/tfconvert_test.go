package tfconvert

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/blueprint"
)

func writeModule(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func findQuestion(res *Result, substr string) bool {
	for _, q := range res.Questions {
		if strings.Contains(q.Text, substr) {
			return true
		}
	}
	return false
}

func TestConvert_ParamsAndDefaults(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"main.tf": `
variable "name" {
  type    = string
  default = "app"
}
variable "count_val" {
  type    = number
  default = 3
}
variable "flag" {
  type    = bool
  default = true
}
variable "azs" {
  type = list(string)
}
variable "tags" {
  type    = map(string)
  default = {}
}
`,
	})

	res, err := Convert(dir, "myblueprint")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(res.Params) != 4 {
		t.Fatalf("expected 4 params (map dropped), got %d: %+v", len(res.Params), res.Params)
	}
	byName := map[string]blueprint.Param{}
	for _, p := range res.Params {
		byName[p.Name] = p
	}
	if p := byName["name"]; p.Type != blueprint.ParamString || p.Required || p.Default != "app" {
		t.Fatalf("name param = %+v", p)
	}
	if p := byName["count_val"]; p.Type != blueprint.ParamNumber || p.Default != 3 {
		t.Fatalf("count_val param = %+v", p)
	}
	if p := byName["flag"]; p.Type != blueprint.ParamBool || p.Default != true {
		t.Fatalf("flag param = %+v", p)
	}
	if p := byName["azs"]; p.Type != blueprint.ParamListString || !p.Required {
		t.Fatalf("azs param = %+v", p)
	}
	if !findQuestion(res, `variable "tags" declares type map(string)`) {
		t.Fatalf("expected a question about the unsupported map(string) type, got: %+v", res.Questions)
	}
}

func TestConvert_ListParamWithDefault_DowngradedToRequired(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"main.tf": `
variable "azs" {
  type    = list(string)
  default = []
}
`,
	})
	res, err := Convert(dir, "b")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(res.Params) != 1 || !res.Params[0].Required || res.Params[0].Default != nil {
		t.Fatalf("azs param = %+v, want required with no default", res.Params)
	}
	if !findQuestion(res, "must always be required") {
		t.Fatalf("expected a question naming the default->required downgrade, got: %+v", res.Questions)
	}
}

func TestConvert_ResourceRef_And_ParamToken(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"main.tf": `
variable "cidr" {
  type    = string
  default = "10.0.0.0/16"
}

resource "aws_vpc" "this" {
  cidr_block = var.cidr
}

resource "aws_subnet" "a" {
  vpc_id     = aws_vpc.this.id
  cidr_block = "10.0.0.0/24"
}
`,
	})
	res, err := Convert(dir, "mynet")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(res.Questions) != 0 {
		t.Fatalf("expected zero questions, got: %+v", res.Questions)
	}
	if len(res.Intent.Resources) != 2 {
		t.Fatalf("expected 2 resources, got %d", len(res.Intent.Resources))
	}
	var vpc, subnet map[string]any
	for _, ri := range res.Intent.Resources {
		var cfg map[string]any
		if err := json.Unmarshal(ri.Config, &cfg); err != nil {
			t.Fatal(err)
		}
		switch ri.Type {
		case "aws_vpc":
			vpc = cfg
		case "aws_subnet":
			subnet = cfg
		}
	}
	if vpc["cidr_block"] != "{cidr}" {
		t.Fatalf("aws_vpc.cidr_block = %v, want the bare {cidr} token", vpc["cidr_block"])
	}
	ref, ok := subnet["vpc_id"].(map[string]any)
	if !ok {
		t.Fatalf("aws_subnet.vpc_id = %v, want a $ref marker", subnet["vpc_id"])
	}
	inner, _ := ref["$ref"].(map[string]any)
	if inner["to"] != "mynet.aws_vpc.this.id" {
		t.Fatalf("$ref.to = %v, want %q", inner["to"], "mynet.aws_vpc.this.id")
	}
}

func TestConvert_CountLength_BecomesForEach(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"main.tf": `
variable "azs" {
  type = list(string)
}

resource "aws_subnet" "private" {
  count = length(var.azs)

  availability_zone = element(var.azs, count.index)
  cidr_block         = "10.0.${count.index}.0/24"
}
`,
	})
	res, err := Convert(dir, "b")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(res.Questions) != 0 {
		t.Fatalf("expected zero questions, got: %+v", res.Questions)
	}
	if len(res.Intent.Resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(res.Intent.Resources))
	}
	ri := res.Intent.Resources[0]
	if ri.ForEach != "azs" {
		t.Fatalf("ForEach = %q, want %q", ri.ForEach, "azs")
	}
	if ri.Name != "private-{azs}" {
		t.Fatalf("Name = %q, want a templated name referencing {azs}", ri.Name)
	}
	var cfg map[string]any
	json.Unmarshal(ri.Config, &cfg)
	if cfg["availability_zone"] != "{azs}" {
		t.Fatalf("availability_zone = %v, want the bare {azs} token", cfg["availability_zone"])
	}
	if cfg["cidr_block"] != "10.0.{azs_index}.0/24" {
		t.Fatalf("cidr_block = %v, want the mixed {azs_index} token form", cfg["cidr_block"])
	}
}

func TestConvert_ForEachToset_BecomesForEach(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"main.tf": `
variable "azs" {
  type = list(string)
}

resource "aws_subnet" "this" {
  for_each = toset(var.azs)

  availability_zone = each.value
}
`,
	})
	res, err := Convert(dir, "b")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(res.Intent.Resources) != 1 || res.Intent.Resources[0].ForEach != "azs" {
		t.Fatalf("expected one for_each resource over azs, got: %+v", res.Intent.Resources)
	}
}

func TestConvert_UnrecognizedCount_Skipped(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"main.tf": `
resource "aws_instance" "x" {
  count = 3
}
`,
	})
	res, err := Convert(dir, "b")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(res.Intent.Resources) != 0 {
		t.Fatalf("expected the resource to be skipped, got: %+v", res.Intent.Resources)
	}
	if len(res.SkippedResources) != 1 || res.SkippedResources[0] != "aws_instance.x" {
		t.Fatalf("SkippedResources = %+v", res.SkippedResources)
	}
	if !findQuestion(res, "isn't a recognized shape") {
		t.Fatalf("expected a question naming the unrecognized count shape, got: %+v", res.Questions)
	}
}

func TestConvert_Cidrsubnet_Marker(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"main.tf": `
variable "cidr" {
  type    = string
  default = "10.0.0.0/16"
}
variable "azs" {
  type = list(string)
}

resource "aws_subnet" "private" {
  count      = length(var.azs)
  cidr_block = cidrsubnet(var.cidr, 8, count.index)
}
`,
	})
	res, err := Convert(dir, "b")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(res.Questions) != 0 {
		t.Fatalf("expected zero questions, got: %+v", res.Questions)
	}
	var cfg map[string]any
	json.Unmarshal(res.Intent.Resources[0].Config, &cfg)
	fn, ok := cfg["cidr_block"].(map[string]any)
	if !ok {
		t.Fatalf("cidr_block = %v, want a $fn marker", cfg["cidr_block"])
	}
	inner := fn["$fn"].(map[string]any)
	if inner["name"] != "cidrsubnet" {
		t.Fatalf("$fn.name = %v", inner["name"])
	}
	args := inner["args"].([]any)
	if len(args) != 3 || args[0] != "{cidr}" || args[1] != float64(8) || args[2] != "{azs_index}" {
		t.Fatalf("$fn.args = %+v", args)
	}
}

func TestConvert_Outputs_Plain(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"main.tf": `
resource "aws_vpc" "this" {
  cidr_block = "10.0.0.0/16"
}

output "vpc_id" {
  value = aws_vpc.this.id
}
`,
	})
	res, err := Convert(dir, "b")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(res.Outputs) != 1 || res.Outputs[0].Name != "vpc_id" || res.Outputs[0].Target != "this.id" {
		t.Fatalf("Outputs = %+v", res.Outputs)
	}
}

func TestConvert_Outputs_ForEachConflict_Declined(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"main.tf": `
variable "azs" {
  type = list(string)
}

resource "aws_vpc" "this" {
  cidr_block = "10.0.0.0/16"
}

resource "aws_subnet" "private" {
  count             = length(var.azs)
  availability_zone = element(var.azs, count.index)
}

output "vpc_id" {
  value = aws_vpc.this.id
}
`,
	})
	res, err := Convert(dir, "b")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(res.Outputs) != 0 {
		t.Fatalf("expected zero outputs (for_each+outputs is a hard boundary), got: %+v", res.Outputs)
	}
	if !findQuestion(res, "can't combine a for_each resource with outputs") {
		t.Fatalf("expected a question naming the boundary, got: %+v", res.Questions)
	}
}

func TestConvert_Locals_SimpleInlined(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"main.tf": `
variable "name" {
  type    = string
  default = "app"
}
locals {
  full_name = "${var.name}-vpc"
}
resource "aws_vpc" "this" {
  cidr_block = local.full_name
}
`,
	})
	res, err := Convert(dir, "b")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(res.Questions) != 0 {
		t.Fatalf("expected zero questions, got: %+v", res.Questions)
	}
	var cfg map[string]any
	json.Unmarshal(res.Intent.Resources[0].Config, &cfg)
	if cfg["cidr_block"] != "{name}-vpc" {
		t.Fatalf("cidr_block = %v, want the inlined local's own mixed-token text", cfg["cidr_block"])
	}
}

func TestConvert_Locals_Cycle_Declined(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"main.tf": `
locals {
  a = local.b
  b = local.a
}
resource "aws_vpc" "this" {
  cidr_block = local.a
}
`,
	})
	res, err := Convert(dir, "b")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(res.Intent.Resources) != 1 {
		t.Fatalf("expected the resource to still exist with one dropped attribute, got %+v", res.Intent.Resources)
	}
	var cfg map[string]any
	json.Unmarshal(res.Intent.Resources[0].Config, &cfg)
	if _, ok := cfg["cidr_block"]; ok {
		t.Fatalf("expected cidr_block to be dropped, got %v", cfg["cidr_block"])
	}
	if !findQuestion(res, "dependency cycle") {
		t.Fatalf("expected a question naming the local cycle, got: %+v", res.Questions)
	}
}

func TestConvert_RequiredProviders(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"main.tf": `
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "6.58.0"
    }
  }
}
`,
	})
	res, err := Convert(dir, "b")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(res.RequiredProviders) != 1 {
		t.Fatalf("RequiredProviders = %+v", res.RequiredProviders)
	}
	rp := res.RequiredProviders[0]
	if rp.LocalName != "aws" || rp.Source != "hashicorp/aws" || rp.Version != "6.58.0" {
		t.Fatalf("RequiredProviders[0] = %+v", rp)
	}
}

func TestConvert_NoTfFiles_Error(t *testing.T) {
	dir := t.TempDir()
	if _, err := Convert(dir, "b"); err == nil {
		t.Fatal("expected an error for a directory with no .tf files")
	}
}

func TestConvert_DependsOn(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"main.tf": `
resource "aws_vpc" "this" {
  cidr_block = "10.0.0.0/16"
}

resource "aws_subnet" "a" {
  cidr_block = "10.0.0.0/24"
  depends_on = [aws_vpc.this]
}
`,
	})
	res, err := Convert(dir, "mynet")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	for _, ri := range res.Intent.Resources {
		if ri.Type == "aws_subnet" {
			if len(ri.DependsOn) != 1 || ri.DependsOn[0] != "mynet.aws_vpc.this" {
				t.Fatalf("DependsOn = %+v", ri.DependsOn)
			}
		}
	}
}

func TestConvert_ConditionalExpression_Declined(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"main.tf": `
variable "flag" {
  type    = bool
  default = true
}

resource "aws_vpc" "this" {
  cidr_block = var.flag ? "10.0.0.0/16" : "10.1.0.0/16"
}
`,
	})
	res, err := Convert(dir, "b")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	var cfg map[string]any
	json.Unmarshal(res.Intent.Resources[0].Config, &cfg)
	if _, ok := cfg["cidr_block"]; ok {
		t.Fatalf("expected cidr_block to be dropped (conditional expr), got %v", cfg["cidr_block"])
	}
	if !findQuestion(res, `resource "aws_vpc.this" attribute "cidr_block"`) {
		t.Fatalf("expected a question naming the dropped attribute, got: %+v", res.Questions)
	}
}

func TestConvert_LifecycleBlock_Question(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"main.tf": `
resource "aws_vpc" "this" {
  cidr_block = "10.0.0.0/16"

  lifecycle {
    prevent_destroy = true
  }
}
`,
	})
	res, err := Convert(dir, "b")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(res.Intent.Resources) != 1 {
		t.Fatalf("expected the resource to still convert, got %+v", res.Intent.Resources)
	}
	if !findQuestion(res, "lifecycle {} block") {
		t.Fatalf("expected a question naming the lifecycle block, got: %+v", res.Questions)
	}
}

func TestConvert_DataSourceReference_Declined(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"main.tf": `
resource "aws_instance" "x" {
  ami = data.aws_ami.ubuntu.id
}
`,
	})
	res, err := Convert(dir, "b")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	var cfg map[string]any
	json.Unmarshal(res.Intent.Resources[0].Config, &cfg)
	if _, ok := cfg["ami"]; ok {
		t.Fatalf("expected ami to be dropped (data source), got %v", cfg["ami"])
	}
	if !findQuestion(res, "data source references aren't supported") {
		t.Fatalf("expected a question naming data sources as unsupported, got: %+v", res.Questions)
	}
}
