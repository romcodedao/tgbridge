#!/usr/bin/env bash
# Fake `claude -p` stream-json: one canned turn per stdin line.
# Content "CRASH" exits mid-turn; "SLOW" waits 1s before the result; "TOKENS=N" reports N context tokens.
sid="fake-${RESUME:-$$}"
for a in "$@"; do [ "$prev" = "--resume" ] && sid="$a"; prev="$a"; done
echo "{\"type\":\"system\",\"subtype\":\"init\",\"session_id\":\"$sid\"}"
while IFS= read -r line; do
  if [[ "$line" == *control_request* ]]; then
    echo "{\"type\":\"result\",\"subtype\":\"error_during_execution\",\"is_error\":true,\"session_id\":\"$sid\",\"usage\":{\"input_tokens\":1}}"
    continue
  fi
  content=$(printf '%s' "$line" | sed -n 's/.*"content":"\([^"]*\)".*/\1/p')
  echo "{\"type\":\"stream_event\",\"event\":{\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"echo:$content\"}}}"
  [[ "$content" == CRASH* ]] && exit 3
  [[ "$content" == *SLOW* ]] && sleep 1
  [[ "$content" == "Write a handoff"* ]] && sleep 0.5
  tok=100
  [[ "$content" =~ TOKENS=([0-9]+) ]] && tok="${BASH_REMATCH[1]}"
  echo "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"echo:$content\"}],\"usage\":{\"input_tokens\":1,\"cache_read_input_tokens\":$tok}}}"
  echo "{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"session_id\":\"$sid\",\"duration_ms\":5,\"usage\":{\"input_tokens\":1,\"cache_read_input_tokens\":$tok,\"output_tokens\":2},\"result\":\"echo:$content\"}"
done
