package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"lingma-ipc-proxy/internal/lingmaipc"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 使用自动发现的传输方式
	opts, err := lingmaipc.ResolveDialOptions(lingmaipc.TransportAuto, "", "")
	if err != nil {
		log.Fatalf("Failed to resolve dial options: %v", err)
	}

	fmt.Printf("Connecting to Lingma IPC...\n")
	fmt.Printf("Transport: %s\n", opts.Transport)
	fmt.Printf("PipePath: %s\n", opts.PipePath)
	fmt.Printf("WebSocketURL: %s\n", opts.WebSocketURL)
	fmt.Println()

	client, err := lingmaipc.Connect(ctx, opts)
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer client.Close()

	// 初始化
	if err := client.Request(ctx, "initialize", map[string]any{
		"protocolVersion":    1,
		"clientCapabilities": map[string]any{},
		"timestamp":          time.Now().UnixMilli(),
	}, nil); err != nil {
		log.Fatalf("Failed to initialize: %v", err)
	}
	fmt.Println("Initialized successfully")
	fmt.Println()

	// 查询模型
	var raw any
	if err := client.Request(ctx, "config/queryModels", map[string]any{}, &raw); err != nil {
		log.Fatalf("Failed to query models: %v", err)
	}

	// 打印原始结果
	fmt.Println("=== Raw IPC Response ===")
	rawJSON, _ := json.MarshalIndent(raw, "", "  ")
	fmt.Println(string(rawJSON))
	fmt.Println()

	// 提取并打印模型列表
	fmt.Println("=== Extracted Models ===")
	models := extractModels(raw)
	for _, m := range models {
		fmt.Printf("ID: %s, Name: %s, Scene: %s\n", m.ID, m.Name, m.Scene)
	}
}

type Model struct {
	ID    string
	Name  string
	Scene string
}

func extractModels(raw any) []Model {
	seen := make(map[string]Model)
	var walk func(scene string, value any)
	walk = func(scene string, value any) {
		switch typed := value.(type) {
		case map[string]any:
			id := firstString(typed, "id", "modelId", "key")
			name := firstString(typed, "name", "label", "displayName", "title")
			currentScene := scene
			if currentScene == "" {
				currentScene = firstString(typed, "scene", "sceneId", "category")
			}
			if id != "" && (name != "" || likelyModelID(id)) {
				if name == "" {
					name = id
				}
				seen[id] = Model{ID: id, Name: name, Scene: currentScene}
			}
			for key, child := range typed {
				nextScene := currentScene
				if nextScene == "" || isSceneKey(key) {
					nextScene = key
				}
				walk(nextScene, child)
			}
		case []any:
			for _, item := range typed {
				walk(scene, item)
			}
		}
	}
	walk("", raw)

	models := make([]Model, 0, len(seen))
	for _, model := range seen {
		models = append(models, model)
	}
	return models
}

func likelyModelID(id string) bool {
	lowered := id
	return contains(lowered, "qwen") || contains(lowered, "model") || contains(lowered, "auto") || contains(lowered, "coder")
}

func isSceneKey(key string) bool {
	switch key {
	case "assistant", "chat", "developer", "inline", "quest":
		return true
	default:
		return false
	}
}

func firstString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := m[key]; ok {
			switch typed := value.(type) {
			case string:
				if typed != "" {
					return typed
				}
			}
		}
	}
	return ""
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
