#!/bin/bash
# lingma-ipc-proxy macOS 功能测试脚本
# 用法: ./scripts/test-macos.sh [host:port]

ENDPOINT="${1:-127.0.0.1:8095}"
MODEL="dashscope_qwen3_coder"
PASS=0
FAIL=0

assert_contains() {
    local response="$1"
    local expected="$2"
    local test_name="$3"
    if echo "$response" | grep -q "$expected"; then
        echo "  ✅ $test_name"
        PASS=$((PASS + 1))
    else
        echo "  ❌ $test_name"
        echo "     期望包含: $expected"
        echo "     实际响应: $(echo "$response" | head -c 200)"
        FAIL=$((FAIL + 1))
    fi
}

echo "========================================"
echo "lingma-ipc-proxy macOS 功能测试"
echo "端点: http://$ENDPOINT"
echo "========================================"

# 1. 测试 /v1/models
echo ""
echo "[1/4] 测试 /v1/models"
RESPONSE=$(curl -s "http://$ENDPOINT/v1/models" 2>/dev/null || echo "ERROR")
assert_contains "$RESPONSE" "dashscope_qwen3_coder" "模型列表包含 Qwen3-Coder"
assert_contains "$RESPONSE" "kmodel" "模型列表包含 Kimi"
assert_contains "$RESPONSE" '"object":"list"' "响应格式正确"

# 2. 测试 /v1/chat/completions 非流式
echo ""
echo "[2/4] 测试 /v1/chat/completions (非流式)"
RESPONSE=$(curl -s -X POST "http://$ENDPOINT/v1/chat/completions" \
    -H 'Content-Type: application/json' \
    -d "{\"model\":\"$MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"1+1=?\"}],\"stream\":false}" 2>/dev/null || echo "ERROR")
assert_contains "$RESPONSE" "2" "回答包含正确答案"
assert_contains "$RESPONSE" "chat.completion" "响应类型正确"
assert_contains "$RESPONSE" "stop" "finish_reason 为 stop"

# 3. 测试 /v1/chat/completions 流式
echo ""
echo "[3/4] 测试 /v1/chat/completions (流式 SSE)"
RESPONSE=$(curl -s -N -X POST "http://$ENDPOINT/v1/chat/completions" \
    -H 'Content-Type: application/json' \
    -d "{\"model\":\"$MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"1+1=?\"}],\"stream\":true}" 2>/dev/null || echo "ERROR")
assert_contains "$RESPONSE" "data:" "包含 SSE data: 前缀"
assert_contains "$RESPONSE" "chat.completion.chunk" "chunk 类型正确"

# 4. 测试 /v1/messages (Anthropic 格式)
echo ""
echo "[4/4] 测试 /v1/messages (Anthropic 格式)"
RESPONSE=$(curl -s -X POST "http://$ENDPOINT/v1/messages" \
    -H 'Content-Type: application/json' \
    -d "{\"model\":\"$MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"2+2=?\"}],\"stream\":false}" 2>/dev/null || echo "ERROR")
assert_contains "$RESPONSE" "4" "回答包含正确答案"
assert_contains "$RESPONSE" "end_turn" "stop_reason 为 end_turn"

# 汇总
echo ""
echo "========================================"
echo "测试结果: $PASS 通过, $FAIL 失败"
echo "========================================"

if [ "$FAIL" -gt 0 ]; then
    exit 1
fi
