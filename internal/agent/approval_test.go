package agent

import (
	"bytes"
	"testing"

	"forge/internal/agent/tools"
)

func TestYoloApproval(t *testing.T) {
	approve := YoloApproval()
	ok, err := approve(tools.Action{Tool: "write_file", Summary: "write main.go"})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("yolo should always approve")
	}
}

func TestYoloApprovalDestructive(t *testing.T) {
	approve := YoloApproval()
	ok, _ := approve(tools.Action{Tool: "run_command", Summary: "rm -rf /"})
	if !ok {
		t.Error("pure yolo approves everything")
	}
}

func TestInteractiveApprovalYes(t *testing.T) {
	input := bytes.NewBufferString("y\n")
	output := &bytes.Buffer{}
	approve := InteractiveApproval(input, output)

	ok, err := approve(tools.Action{
		Tool:    "write_file",
		Summary: "write main.go",
		Detail:  "+package main",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("should approve on 'y'")
	}
}

func TestInteractiveApprovalNo(t *testing.T) {
	input := bytes.NewBufferString("n\n")
	output := &bytes.Buffer{}
	approve := InteractiveApproval(input, output)

	ok, err := approve(tools.Action{Tool: "write_file", Summary: "write main.go"})
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("should deny on 'n'")
	}
}
