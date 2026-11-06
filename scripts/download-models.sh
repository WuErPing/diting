#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
MODELS="${1:-$ROOT/models}"
BASE_URL="https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models"
ASR_DIR="sherpa-onnx-streaming-zipformer-zh-int8-2025-06-30"
REFINE_DIR="sherpa-onnx-sense-voice-zh-en-ja-ko-yue-int8-2024-07-17"

mkdir -p "$MODELS"
cd "$MODELS"

if [[ ! -f silero_vad.onnx ]]; then
  curl --fail --location --retry 3 --output silero_vad.onnx.download "$BASE_URL/silero_vad.onnx"
  mv silero_vad.onnx.download silero_vad.onnx
fi

for MODEL_DIR in "$ASR_DIR" "$REFINE_DIR"; do
  if [[ ! -d "$MODEL_DIR" ]]; then
    curl --fail --location --retry 3 --output "$MODEL_DIR.tar.bz2.download" "$BASE_URL/$MODEL_DIR.tar.bz2"
    mv "$MODEL_DIR.tar.bz2.download" "$MODEL_DIR.tar.bz2"
    tar -xjf "$MODEL_DIR.tar.bz2"
    rm "$MODEL_DIR.tar.bz2"
  fi
done

printf 'Models ready in %s\n' "$MODELS"
