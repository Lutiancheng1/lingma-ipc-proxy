package toolemulation

import (
	"strings"
	"testing"
)

func TestLooksLikeMissedToolUseDetectsLocalToolAvoidance(t *testing.T) {
	cases := []string{
		"我需要使用终端工具来查看内存。",
		"由于当前环境限制，请手动运行 top。",
		"当前环境限制，我无法直接执行系统命令查看你的内存占用。",
		"你可以在终端中运行 top -l 1 | grep PhysMem。",
		"I need to read the file first.",
		"Let me use the web search tool.",
		"You can run the following command in your terminal.",
		"现在我需要切换到计划模式。",
	}
	for _, tc := range cases {
		if !LooksLikeMissedToolUse(tc) {
			t.Fatalf("LooksLikeMissedToolUse(%q) = false", tc)
		}
	}
}

func TestLooksLikeRefusalDetectsLocalAccessRefusals(t *testing.T) {
	cases := []string{
		"当前环境限制，我无法直接执行系统命令查看你的内存占用。",
		"我无法访问你的电脑或本机文件。",
		"I cannot execute commands in your local machine.",
		"I can't access your computer directly.",
	}
	for _, tc := range cases {
		if !LooksLikeRefusal(tc) {
			t.Fatalf("LooksLikeRefusal(%q) = false", tc)
		}
	}
}

func TestInferToolCallsFromTextConvertsMemoryRefusalToBash(t *testing.T) {
	calls := InferToolCallsFromText("当前无法执行系统命令。你可以运行 vm_stat 查看内存占用。", []ToolDef{{
		Name: "Bash",
		InputSchema: map[string]any{
			"properties": map[string]any{
				"command": map[string]any{"type": "string"},
			},
			"required": []any{"command"},
		},
	}})
	if len(calls) != 1 {
		t.Fatalf("call count = %d", len(calls))
	}
	if calls[0].Name != "Bash" {
		t.Fatalf("tool name = %q", calls[0].Name)
	}
	command, _ := calls[0].Arguments["command"].(string)
	if !strings.Contains(command, "vm_stat") || !strings.Contains(command, "memory_pressure") {
		t.Fatalf("unexpected command = %q", command)
	}
}

func TestLooksLikeMissedToolUseIgnoresFinalAnswers(t *testing.T) {
	text := "这个文件负责 HTTP API 路由和 OpenAI 兼容响应。"
	if LooksLikeMissedToolUse(text) {
		t.Fatalf("LooksLikeMissedToolUse(%q) = true", text)
	}
}

func TestInjectToolingIncludesAutoToolGuidance(t *testing.T) {
	prompt := InjectTooling("", []ToolDef{{
		Name:        "read_file",
		Description: "Read a text file.",
		InputSchema: map[string]any{
			"properties": map[string]any{
				"path": map[string]any{"type": "string"},
			},
			"required": []any{"path"},
		},
	}}, ToolChoice{Mode: "auto"}, nil)
	if prompt == "" {
		t.Fatal("empty prompt")
	}
	for _, want := range []string{
		"tool_choice=auto means you must decide",
		"inspect a local file path",
		"Core tool examples",
		"NEVER ask the user to run a command",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestParseActionBlocksMapsCommonToolAliases(t *testing.T) {
	text := "```json action\n{\"tool\":\"Bash\",\"parameters\":{\"command\":\"pwd\",\"extra\":true}}\n```"
	calls, clean, err := ParseActionBlocks(text, []ToolDef{{
		Name: "terminal",
		InputSchema: map[string]any{
			"properties": map[string]any{
				"command": map[string]any{"type": "string"},
			},
		},
	}}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if clean != "" {
		t.Fatalf("clean = %q", clean)
	}
	if len(calls) != 1 {
		t.Fatalf("call count = %d", len(calls))
	}
	if calls[0].Name != "terminal" {
		t.Fatalf("tool name = %q", calls[0].Name)
	}
	if _, ok := calls[0].Arguments["command"]; !ok {
		t.Fatalf("missing command arg: %+v", calls[0].Arguments)
	}
	if _, ok := calls[0].Arguments["extra"]; ok {
		t.Fatalf("unexpected extra arg: %+v", calls[0].Arguments)
	}
}

func TestParseActionBlocksMapsReadAlias(t *testing.T) {
	text := "```json action\n{\"name\":\"Read\",\"arguments\":{\"path\":\"/tmp/a.txt\"}}\n```"
	calls, _, err := ParseActionBlocks(text, []ToolDef{{Name: "read_file"}}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].Name != "read_file" {
		t.Fatalf("calls = %+v", calls)
	}
}
