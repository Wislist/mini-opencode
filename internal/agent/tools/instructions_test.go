package tools

import (
	"strings"
	"testing"
)

func TestRenderToolInstructionsInjectsData(t *testing.T) {
	instructions, err := RenderToolInstructions(BashToolName, InstructionData{
		BannedCommands:  "rm -rf /, git push",
		MaxOutputLength: 1234,
		MaxResults:      50,
		RgAvailable:     true,
	})
	if err != nil {
		t.Fatalf("RenderToolInstructions() error = %v", err)
	}

	for _, want := range []string{
		"rm -rf /, git push",
		"1234",
		"Ripgrep (`rg`) is available",
	} {
		if !strings.Contains(instructions, want) {
			t.Fatalf("instructions missing %q", want)
		}
	}
}

func TestRenderToolInstructionsReadsStaticMarkdown(t *testing.T) {
	instructions, err := RenderToolInstructions(EditToolName, InstructionData{})
	if err != nil {
		t.Fatalf("RenderToolInstructions() error = %v", err)
	}
	if !strings.Contains(instructions, "Edit an existing file") {
		t.Fatalf("unexpected edit instructions: %q", instructions)
	}
}

func TestRenderToolInstructionsRejectsUnknownTool(t *testing.T) {
	_, err := RenderToolInstructions("missing", InstructionData{})
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}
