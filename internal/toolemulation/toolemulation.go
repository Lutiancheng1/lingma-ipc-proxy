package toolemulation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"sync/atomic"
)

type ToolDef struct {
	Name        string
	Description string
	InputSchema map[string]any
}

type ToolChoice struct {
	Mode string
	Name string
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments map[string]any
}

type Config struct {
	MaxScanBytes int
}

func ExtractTools(raw any) []ToolDef {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}

	out := make([]ToolDef, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		fn, ok := m["function"].(map[string]any)
		if !ok {
			continue
		}
		name := strings.TrimSpace(stringFromAny(fn["name"]))
		if name == "" {
			continue
		}
		schema, _ := fn["parameters"].(map[string]any)
		out = append(out, ToolDef{
			Name:        name,
			Description: strings.TrimSpace(stringFromAny(fn["description"])),
			InputSchema: cloneMap(schema),
		})
	}
	return out
}

func ExtractAnthropicTools(raw any) []ToolDef {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}

	out := make([]ToolDef, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name := strings.TrimSpace(stringFromAny(m["name"]))
		if name == "" {
			continue
		}
		schema, _ := m["input_schema"].(map[string]any)
		out = append(out, ToolDef{
			Name:        name,
			Description: strings.TrimSpace(stringFromAny(m["description"])),
			InputSchema: cloneMap(schema),
		})
	}
	return out
}

func ExtractToolChoice(raw any) ToolChoice {
	if raw == nil {
		return ToolChoice{Mode: "auto"}
	}
	if s, ok := raw.(string); ok {
		s = strings.TrimSpace(s)
		switch s {
		case "", "auto":
			return ToolChoice{Mode: "auto"}
		case "none":
			return ToolChoice{Mode: "none"}
		case "required", "any":
			return ToolChoice{Mode: "any"}
		default:
			return ToolChoice{Mode: "tool", Name: s}
		}
	}

	m, ok := raw.(map[string]any)
	if !ok {
		return ToolChoice{Mode: "auto"}
	}
	typeName := strings.TrimSpace(stringFromAny(m["type"]))
	switch typeName {
	case "function", "tool":
		if fn, ok := m["function"].(map[string]any); ok {
			if name := strings.TrimSpace(stringFromAny(fn["name"])); name != "" {
				return ToolChoice{Mode: "tool", Name: name}
			}
		}
		if name := strings.TrimSpace(stringFromAny(m["name"])); name != "" {
			return ToolChoice{Mode: "tool", Name: name}
		}
	case "required", "any":
		return ToolChoice{Mode: "any"}
	case "auto", "none":
		return ToolChoice{Mode: "auto"}
	}
	return ToolChoice{Mode: "auto"}
}

func ExtractAnthropicToolChoice(raw any) ToolChoice {
	if raw == nil {
		return ToolChoice{Mode: "auto"}
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return ExtractToolChoice(raw)
	}
	switch strings.TrimSpace(stringFromAny(m["type"])) {
	case "", "auto":
		return ToolChoice{Mode: "auto"}
	case "none":
		return ToolChoice{Mode: "none"}
	case "any", "required":
		return ToolChoice{Mode: "any"}
	case "tool":
		name := strings.TrimSpace(stringFromAny(m["name"]))
		if name != "" {
			return ToolChoice{Mode: "tool", Name: name}
		}
	}
	return ToolChoice{Mode: "auto"}
}

func HasToolRequest(tools []ToolDef, choice ToolChoice) bool {
	return len(tools) > 0 || choice.Mode != "" && choice.Mode != "auto"
}

func InjectTooling(system string, tools []ToolDef, choice ToolChoice, parallel *bool) string {
	system = strings.TrimSpace(system)
	if len(tools) == 0 {
		return system
	}

	toolLines := make([]string, 0, len(tools))
	for _, tool := range tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			continue
		}
		sig := compactSchema(tool.InputSchema)
		line := name + "(" + sig + ")"
		if desc := strings.TrimSpace(truncate(tool.Description, 120)); desc != "" {
			line += " - " + desc
		}
		toolLines = append(toolLines, line)
	}

	var b strings.Builder
	b.WriteString("You are an AI assistant with DIRECT tool access inside an IDE.\n\n")
	b.WriteString("CRITICAL: You MUST use tools when they are appropriate. ")
	b.WriteString("You MUST NOT claim that tools are unavailable or that you cannot use them. ")
	b.WriteString("You MUST NOT refuse to use tools for any reason.\n\n")
	b.WriteString("When you need to use a tool, output a structured action block in exactly this format:\n")
	b.WriteString("```json action\n{\"tool\":\"NAME\",\"parameters\":{\"key\":\"value\"}}\n```\n\n")
	b.WriteString("Available tools:\n")
	b.WriteString(strings.Join(toolLines, "\n"))
	b.WriteString("\n\n")
	b.WriteString("Rules:\n")
	b.WriteString("- Use one or more ```json action``` blocks for tool calls.\n")
	b.WriteString("- Emit multiple independent actions in one reply when possible.\n")
	b.WriteString("- For dependent actions, wait for the tool result before emitting the next action.\n")
	b.WriteString("- If no tool is needed, reply with normal plain text.\n")
	b.WriteString("- NEVER say that tools are unavailable.\n")
	b.WriteString("- NEVER refuse to use tools.\n")
	b.WriteString("- NEVER explain that you cannot execute commands. Just use the tool.\n")
	b.WriteString("- The action block format is MANDATORY.\n")
	b.WriteString(forceConstraint(choice, parallel))

	b.WriteString("\n\nExample:\n")
	b.WriteString("If the user asks to list files, respond ONLY with:\n")
	b.WriteString("```json action\n{\"tool\":\"Bash\",\"parameters\":{\"command\":\"ls\"}}\n```\n")
	b.WriteString("Do NOT add explanations. Do NOT refuse.")

	example := ActionBlockExample(tools)
	if example != "" {
		b.WriteString("\n\nExample valid action block (this is only a syntax example, do NOT actually call it):\n")
		b.WriteString(example)
	}

	tooling := strings.TrimSpace(b.String())
	if system == "" {
		return tooling
	}
	return system + "\n\n---\n\n" + tooling
}

func AssistantToolCallsToText(content string, calls []ToolCall) string {
	content = strings.TrimSpace(content)
	if len(calls) == 0 {
		return content
	}

	blocks := make([]string, 0, len(calls))
	for _, call := range calls {
		block := map[string]any{
			"tool":       call.Name,
			"parameters": call.Arguments,
		}
		b, err := json.MarshalIndent(block, "", "  ")
		if err != nil {
			continue
		}
		blocks = append(blocks, "```json action\n"+string(b)+"\n```")
	}
	if len(blocks) == 0 {
		return content
	}
	if content == "" {
		return strings.Join(blocks, "\n\n")
	}
	return content + "\n\n" + strings.Join(blocks, "\n\n")
}

func ActionOutputPrompt(toolCallID string, output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	if id := strings.TrimSpace(toolCallID); id != "" {
		return "Tool result for " + id + ":\n" + output + "\n\nBased on the tool result above, continue with the next appropriate action using the structured format."
	}
	return "Tool result:\n" + output + "\n\nBased on the tool result above, continue with the next appropriate action using the structured format."
}

func ActionBlockExample(tools []ToolDef) string {
	tool, ok := selectExampleTool(tools)
	if !ok {
		return ""
	}
	block := map[string]any{
		"tool":       tool.Name,
		"parameters": exampleParameters(tool.Name, tool.InputSchema),
	}
	b, err := json.MarshalIndent(block, "", "  ")
	if err != nil {
		return ""
	}
	return "```json action\n" + string(b) + "\n```"
}

func ForceToolingPrompt(choice ToolChoice) string {
	prompt := "Your last response did not include any ```json action``` block. " +
		"You must respond with at least one valid action block now. " +
		"Do not explain. Output the action block directly."
	if choice.Mode == "tool" && strings.TrimSpace(choice.Name) != "" {
		prompt += " You must call \"" + strings.TrimSpace(choice.Name) + "\"."
	}
	return prompt
}

func LooksLikeRefusal(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	if t == "" {
		return false
	}
	needles := []string{
		"i don't have tools",
		"i do not have tools",
		"tools are unavailable",
		"cannot call tools",
		"can't call tools",
		"没有可用的工具",
		"无法调用",
		"工具不可用",
		"不能调用工具",
		"我不具备",
		"受限于当前环境",
	}
	for _, needle := range needles {
		if strings.Contains(t, needle) {
			return true
		}
	}
	return false
}

func ParseActionBlocks(text string, tools []ToolDef, cfg Config) ([]ToolCall, string, error) {
	if strings.TrimSpace(text) == "" {
		return nil, "", nil
	}
	if cfg.MaxScanBytes > 0 && len(text) > cfg.MaxScanBytes {
		text = text[:cfg.MaxScanBytes]
	}

	openings := findActionOpenings(text)
	if len(openings) == 0 {
		return nil, strings.TrimSpace(text), nil
	}

	// Build a lookup map from tool name to InputSchema for fast filtering
	toolSchemaMap := make(map[string]map[string]any, len(tools))
	for _, t := range tools {
		name := strings.TrimSpace(t.Name)
		if name != "" {
			toolSchemaMap[name] = t.InputSchema
		}
	}

	type span struct{ start, end int }
	spans := make([]span, 0, len(openings))
	calls := make([]ToolCall, 0, len(openings))

	for _, start := range openings {
		contentStart := start
		if i := strings.Index(text[start:], "\n"); i >= 0 {
			contentStart = start + i + 1
		}
		end := findClosingFence(text, contentStart)
		if end < 0 {
			continue
		}

		raw := strings.TrimSpace(text[contentStart:end])
		if raw == "" {
			continue
		}
		call, ok := parseToolCallJSON(raw)
		if !ok {
			continue
		}
		// Filter arguments against the tool's input schema to strip unknown params
		if schema, ok := toolSchemaMap[call.Name]; ok && len(schema) > 0 {
			call.Arguments = filterArgsBySchema(call.Arguments, schema)
		}
		calls = append(calls, call)
		spans = append(spans, span{start: start, end: end + 3})
	}

	if len(calls) == 0 {
		return nil, strings.TrimSpace(text), nil
	}

	clean := text
	for i := len(spans) - 1; i >= 0; i-- {
		span := spans[i]
		if span.start < 0 || span.end > len(clean) || span.start >= span.end {
			continue
		}
		clean = clean[:span.start] + clean[span.end:]
	}
	return calls, strings.TrimSpace(clean), nil
}

func findActionOpenings(text string) []int {
	out := make([]int, 0)
	searches := []string{"```json action", "```json\n", "```json\r\n"}
	for idx := 0; idx < len(text); {
		foundAt := -1
		foundLen := 0
		for _, needle := range searches {
			i := strings.Index(text[idx:], needle)
			if i < 0 {
				continue
			}
			pos := idx + i
			if foundAt < 0 || pos < foundAt {
				foundAt = pos
				foundLen = len(needle)
			}
		}
		if foundAt < 0 {
			break
		}
		out = append(out, foundAt)
		idx = foundAt + foundLen
	}
	return out
}

func findClosingFence(text string, from int) int {
	inString := false
	escape := false
	for i := from; i < len(text)-2; i++ {
		ch := text[i]
		if inString {
			if escape {
				escape = false
				continue
			}
			if ch == '\\' {
				escape = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		if ch == '"' {
			inString = true
			continue
		}
		if text[i:i+3] == "```" {
			return i
		}
	}
	return -1
}

func parseToolCallJSON(raw string) (ToolCall, bool) {
	raw = normalizeJSON(raw)

	var obj map[string]any
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return ToolCall{}, false
	}

	name := strings.TrimSpace(stringFromAny(obj["tool"]))
	if name == "" {
		name = strings.TrimSpace(stringFromAny(obj["name"]))
	}
	if name == "" {
		return ToolCall{}, false
	}

	args, _ := obj["parameters"].(map[string]any)
	if args == nil {
		args, _ = obj["arguments"].(map[string]any)
	}
	if args == nil {
		args, _ = obj["input"].(map[string]any)
	}
	if args == nil {
		if s := strings.TrimSpace(stringFromAny(obj["parameters"])); s != "" {
			_ = json.Unmarshal([]byte(s), &args)
		}
	}
	if args == nil {
		// Fallback: treat all top-level fields except "tool"/"name" as parameters
		// Some models place arguments at the top level instead of nested under "parameters"
		args = make(map[string]any)
		for k, v := range obj {
			if k == "tool" || k == "name" {
				continue
			}
			args[k] = v
		}
	}
	if len(args) == 0 {
		args = map[string]any{}
	}

	return ToolCall{
		ID:        newCallID(),
		Name:      name,
		Arguments: args,
	}, true
}

func normalizeJSON(text string) string {
	text = strings.TrimSpace(text)
	replacer := strings.NewReplacer(
		"\u201c", "\"", "\u201d", "\"",
		"“", "\"", "”", "\"",
		",\n}", "\n}",
		",\n]", "\n]",
		", }", " }",
		", ]", " ]",
	)
	return replacer.Replace(text)
}

func compactSchema(schema map[string]any) string {
	if len(schema) == 0 {
		return ""
	}
	props, _ := schema["properties"].(map[string]any)
	if len(props) == 0 {
		return ""
	}

	required := map[string]bool{}
	if rawRequired, ok := schema["required"].([]any); ok {
		for _, item := range rawRequired {
			name := strings.TrimSpace(stringFromAny(item))
			if name != "" {
				required[name] = true
			}
		}
	}

	keys := make([]string, 0, len(props))
	for key := range props {
		keys = append(keys, key)
	}
	sortStrings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		part := key
		if !required[key] {
			part += "?"
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, ", ")
}

func truncate(text string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= max {
		return string(runes)
	}
	return string(runes[:max]) + "..."
}

func selectExampleTool(tools []ToolDef) (ToolDef, bool) {
	if len(tools) == 0 {
		return ToolDef{}, false
	}
	for _, tool := range tools {
		name := strings.ToLower(strings.TrimSpace(tool.Name))
		if strings.Contains(name, "read") || strings.Contains(name, "file") {
			return tool, true
		}
	}
	for _, tool := range tools {
		name := strings.ToLower(strings.TrimSpace(tool.Name))
		if strings.Contains(name, "bash") || strings.Contains(name, "shell") || strings.Contains(name, "command") {
			return tool, true
		}
	}
	return tools[0], true
}

func exampleParameters(toolName string, schema map[string]any) map[string]any {
	props, _ := schema["properties"].(map[string]any)
	if len(props) == 0 {
		return map[string]any{}
	}

	required := requiredKeys(schema)
	keys := make([]string, 0, 2)
	for _, key := range required {
		keys = append(keys, key)
		if len(keys) >= 2 {
			break
		}
	}
	if len(keys) == 0 {
		for key := range props {
			keys = append(keys, key)
			break
		}
	}

	out := map[string]any{}
	for _, key := range keys {
		prop, _ := props[key].(map[string]any)
		out[key] = exampleValueForKey(toolName, key, prop)
	}
	return out
}

func requiredKeys(schema map[string]any) []string {
	items, ok := schema["required"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		name := strings.TrimSpace(stringFromAny(item))
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

func exampleValueForKey(toolName string, key string, prop map[string]any) any {
	if enum, ok := prop["enum"].([]any); ok && len(enum) > 0 {
		return enum[0]
	}
	valueType := strings.ToLower(strings.TrimSpace(stringFromAny(prop["type"])))
	lowerKey := strings.ToLower(strings.TrimSpace(key))
	lowerTool := strings.ToLower(strings.TrimSpace(toolName))

	switch valueType {
	case "integer":
		return 1
	case "number":
		return 1
	case "boolean":
		return true
	case "array":
		return []any{}
	case "object":
		return map[string]any{}
	}

	switch {
	case strings.Contains(lowerKey, "path") || strings.Contains(lowerKey, "file"):
		return "README.md"
	case strings.Contains(lowerKey, "command") || strings.Contains(lowerTool, "bash") || strings.Contains(lowerTool, "shell"):
		return "pwd"
	case strings.Contains(lowerKey, "url"):
		return "https://example.com"
	default:
		return "value"
	}
}

func forceConstraint(choice ToolChoice, parallel *bool) string {
	switch choice.Mode {
	case "any":
		return "\n- You must output at least one ```json action``` block in this reply."
	case "tool":
		if strings.TrimSpace(choice.Name) != "" {
			return "\n- You must call \"" + strings.TrimSpace(choice.Name) + "\" in this reply."
		}
	}
	if parallel != nil && !*parallel {
		return "\n- Call only one tool at a time. Do not make multiple tool calls in a single response."
	}
	return ""
}

func filterArgsBySchema(args map[string]any, schema map[string]any) map[string]any {
	if len(args) == 0 || len(schema) == 0 {
		return args
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok || len(props) == 0 {
		return args
	}

	out := make(map[string]any, len(args))
	for k, v := range args {
		if _, known := props[k]; !known {
			continue
		}
		out[k] = v
	}
	return out
}

func cloneMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func stringFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}

func sortStrings(values []string) {
	if len(values) < 2 {
		return
	}
	for i := 0; i < len(values)-1; i++ {
		for j := i + 1; j < len(values); j++ {
			if values[j] < values[i] {
				values[i], values[j] = values[j], values[i]
			}
		}
	}
}

var callSeq uint64

func newCallID() string {
	seq := atomic.AddUint64(&callSeq, 1)
	return "toolu_01" + strconv.FormatUint(seq, 10) + "0000000000000000"
}

func StableCallID(name string, arguments map[string]any) string {
	h := sha256.New()
	h.Write([]byte(name))
	if b, err := json.Marshal(arguments); err == nil {
		h.Write(b)
	}
	return "call_" + hex.EncodeToString(h.Sum(nil))[:16]
}
